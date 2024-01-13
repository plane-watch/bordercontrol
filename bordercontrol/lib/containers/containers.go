/*
Package containers provides bordercontrol the ability to manage feed-in containers.

  - Feed-in containers are created on accepting a BEAST connection
  - Feed-in image can be rebuilt on signall or NATS request
  - Feed-in containers can be removed (feeder kicked) if a feeder's API Key is no longer valid
  - Feed-in containers are regularly checked. If out-of-date, they are removed (allowing an up-tp-date container to be created when feeder reconnects)
*/

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

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (

	// channel for received signals to skip container recreation delay
	chanSkipDelay chan os.Signal

	// signal to listen for to skip container recreation delay
	signalSkipContainerRecreationDelay os.Signal

	// channel for container start requests
	containersToStartRequests chan FeedInContainer

	// channel for container start responses
	containersToStartResponses chan startContainerResponse

	// set to true when Init() has been run
	initialised bool

	// mutex for initialised
	initialisedMu sync.RWMutex

	// Name of docker image for feed-in containers
	feedInImageName string

	// Build context (path) for feed-in image
	feedInImageBuildContext string

	// Dockerfile path for feed-in image
	feedInImageBuildContextDockerfile string

	// Feed-in containers will be prefixed with this. Recommend "feed-in-".
	feedInContainerPrefix string

	// container subsystem context
	ctx context.Context

	// cancel function for container subsystem context
	ctxCancel context.CancelFunc

	// the following functions are variable-ized so it can be overridden for unit testing

	// mutex for getDockerClient func
	getDockerClientMu sync.RWMutex

	// Wrapper for client.NewClientWithOpts to allow overriding for testing.
	getDockerClient = func() (cli *client.Client, err error) {
		cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		return cli, err
	}

	// Wrapper for cli.ContainerList to allow overriding for testing.
	dockerContainerList = func(ctx context.Context, cli *client.Client, options types.ContainerListOptions) ([]types.Container, error) {
		return cli.ContainerList(ctx, options)
	}

	// Wrapper for cli.ContainerRemove to allow overriding for testing.
	dockerContainerRemove = func(ctx context.Context, cli *client.Client, containerID string, options types.ContainerRemoveOptions) error {
		return cli.ContainerRemove(ctx, containerID, options)
	}

	// Wrapper for nats_io.ThisInstance to allow overriding for testing.
	natsThisInstance = func(sentToInstance string) (meantForThisInstance bool, thisInstanceName string, err error) {
		return nats_io.ThisInstance(sentToInstance)
	}

	// Wrapper for nats.Msg{}.RespondMsg() to allow overriding for testing.
	natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
		return original.RespondMsg(reply)
	}

	// Wrapper for nats.Msg{}.Ack() to allow overriding for testing.
	natsAck = func(msg *nats.Msg) error {
		return msg.Ack()
	}

	// Wrapper for nats.Msg{}.Term() to allow overriding for testing.
	natsTerm = func(msg *nats.Msg) error {
		return msg.Term()
	}
)

const (

	// nats subject to trigger feed-in image rebuild
	natsSubjFeedInImageRebuild = "pw_bordercontrol.feedinimage.rebuild"

	// nats subject to trigger feed-in container removal (feeder kick)
	natsSubjFeederKick = "pw_bordercontrol.feeder.kick"
)

// ContainerManager provides configuration and control for the container manager
type ContainerManager struct {

	// Name of docker image for feed-in containers
	FeedInImageName string

	// Build context (path) for feed-in image
	FeedInImageBuildContext string

	// Dockerfile path for feed-in image
	FeedInImageBuildContextDockerfile string

	// Feed-in containers will be prefixed with this. Recommend "feed-in-".
	FeedInContainerPrefix string

	// Name of docker network to attach feed-in containers to
	FeedInContainerNetwork string

	// Signal that will skip container recreation delay
	SignalSkipContainerRecreationDelay syscall.Signal

	// URL to pass to the --sink flag of pw-ingest in feed-in container
	PWIngestSink string

	// Logging context to use
	Logger zerolog.Logger

	// waitgroup to track running goroutines
	wg sync.WaitGroup
}

// Run runs container manager
func (conf *ContainerManager) Run() error {

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
		if err == nats_io.ErrNotInitialised {
			log.Debug().Msg("skipping init of nats as no nats connection")
		} else {
			return err
		}
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

			sleepTime, err = checkFeederContainers(checkFeederContainersConf)
			if err != nil {
				log.Err(err).Msgf("error checking %s containers", feedInImageName)
			}

			select {
			// die if context closed
			case <-ctx.Done():
				log.Debug().Msg("stopped checkFeederContainers")
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
	}()
	initialisedMu.Lock()
	defer initialisedMu.Unlock()
	initialised = true
	return nil
}

// Stop stops container manager.
func (conf *ContainerManager) Stop() error {
	// stop goroutines associated with container manager

	if !isInitialised() {
		return ErrNotInitialised
	}

	// cancel context
	ctxCancel()

	// wait for goroutines
	conf.wg.Wait()

	// close chans
	close(containersToStartRequests)
	close(containersToStartResponses)

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

// initNats will initialise nats if nats subsystem has a connection
func initNats() error {
	if nats_io.IsConnected() {
		err := nats_io.Sub(natsSubjFeedInImageRebuild, RebuildFeedInImageHandler)
		if err != nil {
			return err
		}
		err = nats_io.Sub(natsSubjFeederKick, KickFeederHandler)
		if err != nil {
			return err
		}
	} else {
		return nats_io.ErrNotInitialised
	}
	return nil
}

// isInitialised returns true if ContainerManager.Run as been run.
func isInitialised() bool {
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}

// return feed in image container name for given api key
func getFeedInContainerName(apiKey uuid.UUID) string {
	return fmt.Sprintf("%s%s", feedInContainerPrefix, apiKey.String())
}
