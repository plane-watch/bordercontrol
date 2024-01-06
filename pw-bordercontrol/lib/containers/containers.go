package containers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"pw_bordercontrol/lib/nats_io"
	"pw_bordercontrol/lib/stats"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	chanSkipDelay                      chan os.Signal // channel for received signals to skip container recreation delay
	signalSkipContainerRecreationDelay os.Signal
	containersToStartRequests          chan FeedInContainer        // channel for container start requests
	containersToStartResponses         chan startContainerResponse // channel for container start responses
	containerManagerInitialised        bool                        // has ContainerManager.Init() been run?

	promMetricFeederContainersImageCurrent    prometheus.GaugeFunc // prom metric "feedercontainers_image_current"
	promMetricFeederContainersImageNotCurrent prometheus.GaugeFunc // prom metric "feedercontainers_image_not_current"

	feedInImageName                   string
	feedInImageBuildContext           string
	feedInImageBuildContextDockerfile string

	feedInContainerPrefix string

	getDockerClientMu sync.RWMutex
)

const (
	// nats subjects
	natsSubjFeedInImageRebuild = "pw_bordercontrol.feedinimage.rebuild"
	natsSubjFeederKick         = "pw_bordercontrol.feeder.kick"
)

// struct to hold error from docker image build process
type ErrorLine struct {
	Error       string      `json:"error"`
	ErrorDetail ErrorDetail `json:"errorDetail"`
}

// struct to hold error from docker image build process
type ErrorDetail struct {
	Message string `json:"message"`
}

// struct for responses from the startFeederContainers goroutine start a container
type startContainerResponse struct {
	Err                 error  // holds error from starting container
	ContainerStartDelay bool   // do we need to wait for container services to start? (pointer to allow calling function to read data)
	ContainerName       string // feed-in container name
	ContainerID         string // feed-in container ID
}

// the following functions are variable-ized so it can be overridden for unit testing
var (
	GetDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		// set up docker client
		cctx := context.Background()
		cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		return &cctx, cli, err
	}
	NatsThisInstance = func(sentToInstance string) (meantForThisInstance bool, thisInstanceName string, err error) {
		return nats_io.ThisInstance(sentToInstance)
	}
	NatsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
		return original.RespondMsg(reply)
	}
)

func RebuildFeedInImageHandler(msg *nats.Msg) {
	// handles request from nats to rebuild feed-in image

	// update log context
	log := log.With().
		Str("subj", natsSubjFeedInImageRebuild).
		Str("context", feedInImageBuildContext).
		Str("dockerfile", feedInImageBuildContextDockerfile).
		Str("image", feedInImageName).
		Logger()

	// get nats instance of this bordercontrol
	forUs, inst, err := nats_io.ThisInstance(string(msg.Data))
	if err != nil {
		log.Err(err).Msg("could not get nats instance")
	}

	// update log context
	log = log.With().Str("instance", inst).Logger()

	// only respond if our instance named or wildcard
	if !forUs {
		log.Debug().Msg("ignoring, not for this instance")
		return
	}

	// prep reply
	reply := nats.NewMsg(msg.Subject)
	reply.Header.Add("instance", inst)

	// perform build
	log.Debug().Msg("performing build")
	lastLine, err := RebuildFeedInImage(feedInImageName, feedInImageBuildContext, feedInImageBuildContextDockerfile)
	if err != nil {
		log.Err(err).Msg("could not build feed-in image")
		reply.Header.Add("result", "error")
		reply.Data = []byte(err.Error())
	} else {
		log.Debug().Msg("build completed")
		reply.Header.Add("result", "ok")
		reply.Data = []byte(lastLine)
	}

	// reply
	log.Debug().Msg("sending reply")
	err = NatsRespondMsg(msg, reply)
	if err != nil {
		log.Err(err).Str("subj", natsSubjFeedInImageRebuild).Msg("could not respond")
	}
}

func RebuildFeedInImage(imageName, buildContext, dockerfile string) (lastLine string, err error) {

	// ensure container manager has been initialised
	if !containerManagerInitialised {
		err := errors.New("container manager has not been initialised")
		return lastLine, err
	}

	log.Debug().Msg("starting rebuild feed-in image")

	// get docker client
	getDockerClientMu.RLock()
	ctx, cli, err := GetDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		log.Err(err).Msg("error getting docker client")
		return lastLine, err
	}
	defer cli.Close()

	// create tar archive from build context
	log.Debug().Msg("tar-ing build context")
	tar, err := archive.TarWithOptions(buildContext, &archive.TarOptions{
		ExcludePatterns: []string{"*.md"},
	})
	if err != nil {
		log.Err(err).Msg("tar-ing build context")
		return lastLine, err
	}

	// build
	log.Debug().Msg("perform image build")
	opts := types.ImageBuildOptions{
		Dockerfile: dockerfile,
		Tags:       []string{imageName},
		Remove:     true,
		PullParent: true,
	}
	res, err := cli.ImageBuild(*ctx, tar, opts)
	if err != nil {
		log.Err(err).Msg("perform image build")
		return lastLine, err
	}
	defer res.Body.Close()

	// get log
	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		lastLine = scanner.Text()

		var v map[string]interface{}
		err := json.Unmarshal([]byte(lastLine), &v)
		if err == nil {
			stream, ok := v["stream"].(string)
			if ok {
				stream = strings.ReplaceAll(stream, "\n", "")
				stream = strings.ReplaceAll(stream, "\r", "")
				if stream != "" && !strings.Contains(stream, " ---> ") {
					log.Debug().Str("stream", stream).Msg("build output")
				}
			}
		}
	}

	// get error if one exists
	errLine := &ErrorLine{}
	json.Unmarshal([]byte(lastLine), errLine)
	if errLine.Error != "" {
		return lastLine, errors.New(errLine.Error)
	}

	// kick-off updates
	chanSkipDelay <- signalSkipContainerRecreationDelay

	return lastLine, nil
}

func promMetricFeederContainersImageCurrentGaugeFunc(feedInImage, feedInContainerPrefix string) float64 {
	n := float64(0)

	// set up docker client
	getDockerClientMu.RLock()
	dockerCtx, cli, err := GetDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	// prepare filter to find feed-in containers
	filters := filters.NewArgs()
	filters.Add("name", fmt.Sprintf("%s*", feedInContainerPrefix))

	// find containers
	containers, err := cli.ContainerList(*dockerCtx, types.ContainerListOptions{Filters: filters})
	if err != nil {
		panic(err)
	}

	// for each container...
	for _, container := range containers {

		// check containers are running latest feed-in image
		if container.Image == feedInImage {
			n++
		}

	}
	return n
}

func promMetricFeederContainersImageNotCurrentGaugeFunc(feedInImage, feedInContainerPrefix string) float64 {
	n := float64(0)

	// set up docker client
	getDockerClientMu.RLock()
	dockerCtx, cli, err := GetDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	// prepare filter to find feed-in containers
	filters := filters.NewArgs()
	filters.Add("name", fmt.Sprintf("%s*", feedInContainerPrefix))

	// find containers
	containers, err := cli.ContainerList(*dockerCtx, types.ContainerListOptions{Filters: filters})
	if err != nil {
		panic(err)
	}

	// for each container...
	for _, container := range containers {

		// check containers are running latest feed-in image
		if container.Image != feedInImage {
			n++
		}

	}
	return n
}

func registerPromMetrics(feedInImage, feedInContainerPrefix string) {

	promMetricFeederContainersImageCurrent = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: stats.PromNamespace,
		Subsystem: stats.PromSubsystem,
		Name:      "feedercontainers_image_current",
		Help:      "The number of feed-in-* containers running on this host that are using the latest feed-in image.",
	},
		func() float64 {
			return promMetricFeederContainersImageCurrentGaugeFunc(feedInImage, feedInContainerPrefix)
		})

	promMetricFeederContainersImageNotCurrent = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: stats.PromNamespace,
		Subsystem: stats.PromSubsystem,
		Name:      "feedercontainers_image_not_current",
		Help:      "The number of feed-in-* containers running on this host that are using an out of date feed-in image and require upgrading.",
	},
		func() float64 {
			return promMetricFeederContainersImageNotCurrentGaugeFunc(feedInImage, feedInContainerPrefix)
		})
}

type ContainerManager struct {
	FeedInImageName                    string // Name of docker image for feed-in containers.
	FeedInImageBuildContext            string
	FeedInImageBuildContextDockerfile  string
	FeedInContainerPrefix              string         // Feed-in containers will be prefixed with this. Recommend "feed-in-".
	FeedInContainerNetwork             string         // Name of docker network to attach feed-in containers to.
	SignalSkipContainerRecreationDelay syscall.Signal // Signal that will skip container recreation delay.
	PWIngestSink                       string         // URL to pass to the --sink flag of pw-ingest in feed-in container.
	Logger                             zerolog.Logger // Logging context to use

	// facility to stop goroutines
	stop   bool           // set to false (using below mutex for safety) to stop goroutines
	stopMu sync.RWMutex   // mutex for above bool
	stopC  chan bool      // channel to stop checkFeederContainers
	wg     sync.WaitGroup // waitgroup for sync
}

func (conf *ContainerManager) Init() {
	// start goroutines associated with container manager

	log.Info().Msg("starting feed-in container manager")

	conf.stopC = make(chan bool) // prep channel to stop checkFeederContainers

	// TODO: check feed-in image exists
	// TODO: check feed-in network exists

	signalSkipContainerRecreationDelay = conf.SignalSkipContainerRecreationDelay
	feedInImageName = conf.FeedInImageName
	feedInImageBuildContext = conf.FeedInImageBuildContext
	feedInImageBuildContextDockerfile = conf.FeedInImageBuildContextDockerfile
	feedInContainerPrefix = conf.FeedInContainerPrefix

	// nats
	if nats_io.IsConnected() {
		err := nats_io.Sub(natsSubjFeedInImageRebuild, RebuildFeedInImageHandler)
		if err != nil {
			log.Fatal().Str("subj", natsSubjFeedInImageRebuild).Err(err).Msg("subscribe failed")
		}
		err = nats_io.Sub(natsSubjFeederKick, KickFeederHandler)
		if err != nil {
			log.Fatal().Str("subj", natsSubjFeederKick).Err(err).Msg("subscribe failed")
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

		// run forever
		for {
			_ = startFeederContainers(startFeederContainersConf)
			// no sleep here as this goroutune needs to be relaunched after each container start

			// if requested to stop, then don't loop any more
			conf.stopMu.RLock()
			if conf.stop {
				conf.stopMu.RUnlock()
				log.Debug().Msg("stopped startFeederContainers")
				conf.wg.Done()
				return
			} else {
				conf.stopMu.RUnlock()
			}
		}
	}()

	// start goroutine to check feed-in containers
	conf.wg.Add(1)
	go func() {

		// prep config
		checkFeederContainersConf := checkFeederContainersConfig{
			feedInImageName:          conf.FeedInImageName,
			feedInContainerPrefix:    conf.FeedInContainerPrefix,
			checkFeederContainerSigs: chanSkipDelay,
			logger:                   conf.Logger,
			stop:                     conf.stopC,
		}

		// run forever
		for {
			_ = checkFeederContainers(checkFeederContainersConf)
			// no sleep here as this goroutune needs to be relaunched after each container kill

			// if requested to stop, then don't loop any more
			conf.stopMu.RLock()
			if conf.stop {
				conf.stopMu.RUnlock()
				log.Debug().Msg("stopped checkFeederContainers")
				conf.wg.Done()
				return
			} else {
				conf.stopMu.RUnlock()
			}
		}
	}()
	containerManagerInitialised = true
}

func (conf *ContainerManager) Close() {
	// stop goroutines associated with container manager

	conf.stopMu.Lock()
	conf.stop = true
	conf.stopMu.Unlock()
	conf.stopC <- true
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

	log.Info().Msg("stopped feed-in container manager")
}

// struct for requests to the startFeederContainers goroutine start a container
type FeedInContainer struct {
	Lat, Lon   float64   // position of feeder
	Label      string    // feeder label
	ApiKey     uuid.UUID // feeder ATC api key
	FeederCode string    // unique feeder code
	Addr       net.IP    // client IP address
}

func (feedInContainer *FeedInContainer) Start() (containerID string, err error) {
	// start a feed-in container, return the container ID

	// ensure container manager has been initialised
	if !containerManagerInitialised {
		err := errors.New("container manager has not been initialised")
		return containerID, err
	}

	// request start of the feed-in container with submission timeout
	select {
	case containersToStartRequests <- *feedInContainer:
	case <-time.After(5 * time.Second):
		err := errors.New("5s timeout waiting to submit container start request")
		return containerID, err
	}

	// wait for request to be actioned
	var startedContainer startContainerResponse
	select {
	case startedContainer = <-containersToStartResponses:
	case <-time.After(30 * time.Second):
		err := errors.New("30s timeout waiting for container start request to be fulfilled")
		return containerID, err
	}

	// check for start errors
	if startedContainer.Err != nil {
		return containerID, startedContainer.Err
	}

	// wait for container start if needed
	if startedContainer.ContainerStartDelay {
		log.Debug().Msg("waiting for container to start")
		time.Sleep(5 * time.Second)
	}

	containerID = startedContainer.ContainerID

	return containerID, nil
}

type checkFeederContainersConfig struct {
	feedInImageName          string         // Name of docker image for feed-in containers.
	feedInContainerPrefix    string         // Feed-in containers will be prefixed with this. Recommend "feed-in-".
	checkFeederContainerSigs chan os.Signal // Channel to receive signals. Received signal will skip sleeps and cause containers to be checked/recreated immediately.
	logger                   zerolog.Logger // Logging context
	stop                     chan bool
}

func checkFeederContainers(conf checkFeederContainersConfig) error {
	// Checks feed-in containers are running the latest image. If they aren't remove them.
	// They will be recreated using the latest image when the client reconnects.

	// TODO: One instance of this goroutine per region/mux would be good.

	var (
		containerRemoved bool          // was a container removed this run
		sleepTime        time.Duration // how long to sleep for between runs
	)

	log := conf.logger.With().
		Strs("func", []string{"containers.go", "checkFeederContainers"}).
		Logger()

	// cycles through feed-in containers and recreates if needed

	// set up docker client
	log.Trace().Msg("set up docker client")
	getDockerClientMu.RLock()
	dockerCtx, cli, err := GetDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		log.Err(err).Msg("error creating docker client")
		return err
	}
	defer cli.Close()

	// prepare filters to find feed-in containers
	log.Trace().Msg("prepare filter to find feed-in containers")
	filterFeedIn := filters.NewArgs()
	filterFeedIn.Add("name", fmt.Sprintf("%s*", conf.feedInContainerPrefix))

	// find containers
	log.Trace().Msg("find containers")
	containers, err := cli.ContainerList(*dockerCtx, types.ContainerListOptions{Filters: filterFeedIn})
	if err != nil {
		log.Err(err).Msg("error finding containers")
		return err
	}

	// for each container...
ContainerLoop:
	for _, container := range containers {

		log.Trace().
			Str("container_id", container.ID).
			Str("container_image", container.Image).
			Str("container_name", container.Names[0]).
			Str("feedInImageName", conf.feedInImageName).
			Msg("checking container")

		// check containers are running latest feed-in image
		if container.Image != conf.feedInImageName {

			// update log context with container name
			log := log.With().Str("container", container.Names[0][1:]).Logger()

			// If a container is found running an out-of-date image, then remove it.
			// It should be recreated automatically when the client reconnects
			log.Info().Msg("out of date feed-in container being killed for recreation")
			err := cli.ContainerRemove(*dockerCtx, container.ID, types.ContainerRemoveOptions{Force: true})
			if err != nil {
				log.Err(err).Msg("error killing out of date feed-in container")
			} else {

				// If container was removed successfully, then break out of this loop
				containerRemoved = true
				break ContainerLoop
			}
		}
	}

	// determine how long to sleep
	if containerRemoved {
		// if a container has been removed, only wait 30 seconds
		sleepTime = 30
	} else {
		// if no containers have been removed, wait 5 minutes before checking again
		sleepTime = 300
	}

	// sleep unless/until siguser1 is caught
	select {
	case s := <-conf.checkFeederContainerSigs:
		log.Info().Str("signal", s.String()).Msg("caught signal, proceeding immediately")
		break
	case <-time.After(sleepTime * time.Second):
		log.Trace().Msg("finished sleeping")
	case <-conf.stop:
		log.Trace().Msg("received from stop chan")
	}

	return nil
}

func getFeedInContainerName(apiKey uuid.UUID) string {
	// return feed in image container name for given api key
	return fmt.Sprintf("%s%s", feedInContainerPrefix, apiKey.String())
}

type startFeederContainersConfig struct {
	feedInImageName            string                      // Name of docker image for feed-in containers.
	feedInContainerPrefix      string                      // Feed-in containers will be prefixed with this. Recommend "feed-in-".
	feedInContainerNetwork     string                      // Name of docker network to attach feed-in containers to.
	pwIngestSink               string                      // URL to pass to the --sink flag of pw-ingest in feed-in container.
	containersToStartRequests  chan FeedInContainer        // Channel to receive container start requests from.
	containersToStartResponses chan startContainerResponse // Channel to send container start responses to.
	logger                     zerolog.Logger              // Logging context
}

func startFeederContainers(conf startFeederContainersConfig) error {
	// reads startContainerRequests from channel containersToStartRequests and starts container
	// responds via channel containersToStartResponses

	// update log context with function name
	log := conf.logger.With().
		Strs("func", []string{"containers.go", "startFeederContainers"}).
		Logger()

	// log.Trace().Msg("started")

	// set up docker client
	log.Trace().Msg("set up docker client")
	getDockerClientMu.RLock()
	dockerCtx, cli, err := GetDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		log.Err(err).Msg("error creating docker client")
		return err
	}
	defer cli.Close()

	// read from channel (this blocks until a request comes in)
	var containerToStart FeedInContainer
	select {
	case containerToStart = <-conf.containersToStartRequests:
		log.Trace().Msg("received from containersToStartRequests")
	case <-time.After(time.Second * 5):
		log.Trace().Msg("timeout receiving from containersToStartRequests")
		return nil
	}

	// prep response object
	response := startContainerResponse{}

	// prepare logger
	log = log.With().
		Str("label", containerToStart.Label).
		Str("uuid", containerToStart.ApiKey.String()).
		Str("src", containerToStart.Addr.String()).
		Str("code", containerToStart.FeederCode).
		Logger()

	// determine if container is already running

	response.ContainerName = getFeedInContainerName(containerToStart.ApiKey)

	// prepare filter to find feed-in container
	filterFeedIn := filters.NewArgs()
	filterFeedIn.Add("name", response.ContainerName)
	filterFeedIn.Add("status", "running")

	// find container
	containers, err := cli.ContainerList(*dockerCtx, types.ContainerListOptions{Filters: filterFeedIn})
	if err != nil {
		log.Err(err).Msg("error finding feed-in container")
	}

	// check if container found
	if len(containers) > 0 {
		// if container found
		log.Debug().
			Str("state", containers[0].State).
			Str("status", containers[0].Status).
			Msg("feed-in container exists")

		// no need to check if started as container created with AutoRemove set to true

	} else {
		// if container is not running, create it

		// tell calling function that it should wait for services to start before proxying connections to the container
		response.ContainerStartDelay = true

		// prepare environment variables for container
		envVars := [...]string{
			fmt.Sprintf("FEEDER_LAT=%f", containerToStart.Lat),
			fmt.Sprintf("FEEDER_LON=%f", containerToStart.Lon),
			fmt.Sprintf("FEEDER_UUID=%s", containerToStart.ApiKey.String()),
			fmt.Sprintf("FEEDER_TAG=%s", containerToStart.FeederCode),
			"PW_INGEST_PUBLISH=location-updates",
			"PW_INGEST_INPUT_MODE=listen",
			"PW_INGEST_INPUT_PROTO=beast",
			"PW_INGEST_INPUT_ADDR=0.0.0.0",
			"PW_INGEST_INPUT_PORT=12345",
			fmt.Sprintf("PW_INGEST_SINK=%s", conf.pwIngestSink),
		}

		// prepare labels for container
		containerLabels := make(map[string]string)
		containerLabels["plane.watch.label"] = containerToStart.Label
		containerLabels["plane.watch.feedercode"] = containerToStart.FeederCode
		containerLabels["plane.watch.lat"] = fmt.Sprintf("%f", containerToStart.Lat)
		containerLabels["plane.watch.lon"] = fmt.Sprintf("%f", containerToStart.Lon)
		containerLabels["plane.watch.uuid"] = containerToStart.ApiKey.String()

		// prepare container config
		containerConfig := container.Config{
			Image:  conf.feedInImageName,
			Env:    envVars[:],
			Labels: containerLabels,
		}

		// prepare tmpfs config
		tmpFSConfig := make(map[string]string)
		tmpFSConfig["/run"] = "exec,size=64M"
		tmpFSConfig["/var/log"] = ""

		// prepare container host config
		containerHostConfig := container.HostConfig{
			AutoRemove: true,
			Tmpfs:      tmpFSConfig,
		}

		// prepare container network config
		endpointsConfig := make(map[string]*network.EndpointSettings)
		endpointsConfig[conf.feedInContainerNetwork] = &network.EndpointSettings{}
		networkingConfig := network.NetworkingConfig{
			EndpointsConfig: endpointsConfig,
		}

		// create feed-in container
		resp, err := cli.ContainerCreate(*dockerCtx, &containerConfig, &containerHostConfig, &networkingConfig, nil, response.ContainerName)
		if err != nil {
			log.Err(err).Msg("could not create feed-in container")
			response.Err = err
		} else {
			log.Debug().Str("container_id", resp.ID).Msg("created feed-in container")
		}

		response.ContainerID = resp.ID

		// start container
		if err := cli.ContainerStart(*dockerCtx, resp.ID, types.ContainerStartOptions{}); err != nil {
			log.Err(err).Msg("could not start feed-in container")
			response.Err = err
		} else {
			log.Debug().Str("container_id", resp.ID).Msg("started feed-in container")
		}

	}

	// send response
	conf.containersToStartResponses <- response

	// log.Trace().Msg("finished")
	return nil
}

func KickFeederHandler(msg *nats.Msg) {

	log := log.With().
		Str("subj", natsSubjFeederKick).
		Logger()

	inst, err := nats_io.GetInstance()
	if err != nil {
		log.Err(err).Msg("could not get nats instance")
		return
	}

	log = log.With().Str("instance", inst).Logger()

	apiKey, err := uuid.ParseBytes(msg.Data)
	if err != nil {
		log.Err(err).Msg("could not parse api key from message body")
		return
	}

	log = log.With().Str("apikey", apiKey.String()).Logger()

	err = KickFeeder(apiKey)
	if err != nil {
		log.Err(err).Msg("could not kick feeder")
		return
	}

	// reply
	log.Debug().Msg("acking message")
	err = msg.Ack()
	if err != nil {
		log.Err(err).Msg("could not acknowledge message")
		return
	}
}

func KickFeeder(apiKey uuid.UUID) error {
	// kills the feeder container used by feeder with apiKey

	// get docker client
	getDockerClientMu.RLock()
	ctx, cli, err := GetDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		return err
	}

	// get container name
	containerName := getFeedInContainerName(apiKey)

	// log context
	log := log.With().Str("container", containerName).Logger()

	// prep filters
	filters := filters.NewArgs()
	filters.Add("label", fmt.Sprintf("%s=%s", "plane.watch.uuid", apiKey.String()))
	filters.Add("name", containerName)

	// find container
	containers, err := cli.ContainerList(*ctx, types.ContainerListOptions{
		All:     true,
		Filters: filters,
	})

	// ensure exactly one container found
	if len(containers) <= 0 {
		log.Debug().Msg("container not found")
		return nil
	} else if len(containers) > 1 {
		err := errors.New("multiple containers found")
		log.Err(err).Msg("container not found")
		return err
	}

	// kill container
	log.Info().Msg("killing feed-in container")
	err = cli.ContainerRemove(*ctx, containers[0].ID, types.ContainerRemoveOptions{
		Force: true,
	})
	if err != nil {
		log.Err(err).Msg("could not remove container")
		return err
	}

	return nil
}
