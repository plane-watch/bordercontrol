package containers

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"pw_bordercontrol/lib/nats_io"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	chanSkipDelay                      chan os.Signal              // channel for received signals to skip container recreation delay
	signalSkipContainerRecreationDelay os.Signal                   // signal to listen for to skip container recreation delay
	containersToStartRequests          chan FeedInContainer        // channel for container start requests
	containersToStartResponses         chan startContainerResponse // channel for container start responses

	initialised   bool         // has ContainerManager.Init() been run?
	initialisedMu sync.RWMutex // mutex for initialised

	feedInImageName                   string // Name of docker image for feed-in containers
	feedInImageBuildContext           string // Build context (path) for feed-in image
	feedInImageBuildContextDockerfile string // Dockerfile path for feed-in image

	feedInContainerPrefix string // Feed-in containers will be prefixed with this. Recommend "feed-in-".

	ctx       context.Context    // container subsystem context
	ctxCancel context.CancelFunc // cancel function for container subsystem context

	// the following functions are variable-ized so it can be overridden for unit testing

	getDockerClientMu sync.RWMutex // mutex for getDockerClient func
	getDockerClient   = func() (cli *client.Client, err error) {
		// set up docker client
		cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		return cli, err
	}
	natsThisInstance = func(sentToInstance string) (meantForThisInstance bool, thisInstanceName string, err error) {
		return nats_io.ThisInstance(sentToInstance)
	}
	natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
		return original.RespondMsg(reply)
	}
	natsAck = func(msg *nats.Msg) error {
		return msg.Ack()
	}
)

const (
	// nats subjects
	natsSubjFeedInImageRebuild = "pw_bordercontrol.feedinimage.rebuild"
	natsSubjFeederKick         = "pw_bordercontrol.feeder.kick"
)

type ContainerManager struct {
	FeedInImageName                    string         // Name of docker image for feed-in containers
	FeedInImageBuildContext            string         // Build context (path) for feed-in image
	FeedInImageBuildContextDockerfile  string         // Dockerfile path for feed-in image
	FeedInContainerPrefix              string         // Feed-in containers will be prefixed with this. Recommend "feed-in-".
	FeedInContainerNetwork             string         // Name of docker network to attach feed-in containers to
	SignalSkipContainerRecreationDelay syscall.Signal // Signal that will skip container recreation delay
	PWIngestSink                       string         // URL to pass to the --sink flag of pw-ingest in feed-in container
	Logger                             zerolog.Logger // Logging context to use
	wg                                 sync.WaitGroup // waitgroup to track running goroutines
}

func (conf *ContainerManager) Run() error {
	// start goroutines associated with container manager

	if isInitialised() {
		return ErrAlreadyInitialised
	}

	log.Info().Msg("starting feed-in container manager")

	// prepare context
	ctx, ctxCancel = context.WithCancel(context.Background())

	// TODO: check feed-in image exists
	// TODO: check feed-in network exists

	// store globals
	// TODO: could probably store these in a context?
	signalSkipContainerRecreationDelay = conf.SignalSkipContainerRecreationDelay
	feedInImageName = conf.FeedInImageName
	feedInImageBuildContext = conf.FeedInImageBuildContext
	feedInImageBuildContextDockerfile = conf.FeedInImageBuildContextDockerfile
	feedInContainerPrefix = conf.FeedInContainerPrefix

	// initialise nats if nats subsystem has a connection
	err := initNats()
	if err != nil {
		return err
	}

	// register prom metrics
	registerPromMetrics(conf.FeedInImageName, conf.FeedInContainerPrefix)

	// prep channel for signal to skip delay for out-of-date feed-in container recreation
	chanSkipDelay = make(chan os.Signal, 1)
	signal.Notify(chanSkipDelay, conf.SignalSkipContainerRecreationDelay)

	// prepare channel for container start requests
	containersToStartRequests = make(chan FeedInContainer)

	// prepare channel for container start responses
	containersToStartResponses = make(chan startContainerResponse)

	// start goroutine to create feed-in containers
	conf.wg.Add(1)
	go func() {
		defer conf.wg.Done()

		// prep config
		startFeederContainersConf := startFeederContainersConfig{
			feedInImageName:            conf.FeedInImageName,
			feedInContainerPrefix:      conf.FeedInContainerPrefix,
			feedInContainerNetwork:     conf.FeedInContainerNetwork,
			pwIngestSink:               conf.PWIngestSink,
			containersToStartRequests:  containersToStartRequests,
			containersToStartResponses: containersToStartResponses,
			logger:                     conf.Logger,
		}

		// run until context cancelled
		for {
			// order matters!
			select {
			// die if context closed
			case <-ctx.Done():
				log.Debug().Msg("stopped startFeederContainers")
				return
			// otherwise....
			case containerToStart := <-containersToStartRequests:
				response, err := startFeederContainers(startFeederContainersConf, containerToStart)
				response.Err = err
				containersToStartResponses <- response
			}
		}
	}()

	// start goroutine to check feed-in containers
	conf.wg.Add(1)
	go func() {
		defer conf.wg.Done()

		// prep config
		checkFeederContainersConf := checkFeederContainersConfig{
			feedInImageName:          conf.FeedInImageName,
			feedInContainerPrefix:    conf.FeedInContainerPrefix,
			checkFeederContainerSigs: chanSkipDelay,
			logger:                   conf.Logger,
		}

		// initial sleepTime
		sleepTime := time.Second * 30

		// run until context cancelled
		for {
			var err error

			select {
			// die if context closed
			case <-ctx.Done():
				log.Debug().Msg("stopped checkFeederContainers")
				return
			default:
				sleepTime, err = checkFeederContainers(checkFeederContainersConf)
			}

			if err != nil {
				log.Err(err).Msgf("error checking %s containers", feedInImageName)
			} else {

				select {
				// die if context closed
				case <-ctx.Done():
					return
				// skep delay if signal sent (or nats msg received, which sends a signal anyway)
				case s := <-chanSkipDelay:
					log.Info().Str("signal", s.String()).Msg("caught signal, proceeding immediately")
					continue
				// otherwise sleep for however long we need to
				case <-time.After(sleepTime):
					continue
				}
			}
		}
	}()
	initialisedMu.Lock()
	defer initialisedMu.Unlock()
	initialised = true
	return nil
}

func (conf *ContainerManager) Stop() error {
	// stop goroutines associated with container manager

	if !isInitialised() {
		return ErrNotInitialised
	}

	// cancel context
	ctxCancel()

	// close chans
	close(containersToStartRequests)
	close(containersToStartResponses)

	// wait for goroutines
	conf.wg.Wait()

	// unregister prom metrics
	ok := prometheus.Unregister(promMetricFeederContainersImageCurrent)
	if !ok {
		log.Error().Msg("could not unregister promMetricFeederContainersImageCurrent")
	}
	ok = prometheus.Unregister(promMetricFeederContainersImageNotCurrent)
	if !ok {
		log.Error().Msg("could not unregister promMetricFeederContainersImageNotCurrent")
	}

	// reset vars
	initialisedMu.Lock()
	defer initialisedMu.Unlock()
	initialised = false

	log.Info().Msg("stopped feed-in container manager")

	return nil
}

func initNats() error {
	// initialise nats if nats subsystem has a connection
	if nats_io.IsConnected() {
		err := nats_io.Sub(natsSubjFeedInImageRebuild, RebuildFeedInImageHandler)
		if err != nil {
			return err
		}
		err = nats_io.Sub(natsSubjFeederKick, KickFeederHandler)
		if err != nil {
			return err
		}
	}
	return nil
}

func isInitialised() bool {
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}

func getFeedInContainerName(apiKey uuid.UUID) string {
	// return feed in image container name for given api key
	return fmt.Sprintf("%s%s", feedInContainerPrefix, apiKey.String())
}
