package containers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/google/uuid"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	chanSkipDelay               chan os.Signal              // channel for received signals to skip container recreation delay
	containersToStartRequests   chan FeedInContainer        // channel for container start requests
	containersToStartResponses  chan startContainerResponse // channel for container start responses
	containerManagerInitialised bool                        // has ContainerManager.Init() been run?
)

// struct for responses from the startFeederContainers goroutine start a container
type startContainerResponse struct {
	Err                 error  // holds error from starting container
	ContainerStartDelay bool   // do we need to wait for container services to start? (pointer to allow calling function to read data)
	ContainerName       string // feed-in container name
	ContainerID         string // feed-in container ID
}

// the following function is variable-ized so it can be overridden for unit testing
var GetDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
	// set up docker client
	cctx := context.Background()
	cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	return &cctx, cli, err
}

type ContainerManager struct {
	FeedInImageName                    string         // Name of docker image for feed-in containers.
	FeedInContainerPrefix              string         // Feed-in containers will be prefixed with this. Recommend "feed-in-".
	FeedInContainerNetwork             string         // Name of docker network to attach feed-in containers to.
	SignalSkipContainerRecreationDelay syscall.Signal // Signal that will skip container recreation delay.
	PWIngestSink                       string         // URL to pass to the --sink flag of pw-ingest in feed-in container.
	Logger                             zerolog.Logger // Logging context to use
}

func (conf *ContainerManager) Init() {

	log.Info().Msg("starting feed-in container manager")

	// TODO: check feed-in image exists
	// TODO: check feed-in network exists

	// prep channel for signal to skip delay for out-of-date feed-in container recreation
	chanSkipDelay = make(chan os.Signal, 1)
	signal.Notify(chanSkipDelay, conf.SignalSkipContainerRecreationDelay)

	// prepare channel for container start requests
	containersToStartRequests = make(chan FeedInContainer)

	// prepare channel for container start responses
	containersToStartResponses = make(chan startContainerResponse)

	// start goroutine to create feed-in containers
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
		}
	}()

	// start goroutine to check feed-in containers
	go func() {

		// prep config
		checkFeederContainersConf := checkFeederContainersConfig{
			feedInImageName:          conf.FeedInImageName,
			feedInContainerPrefix:    conf.FeedInContainerPrefix,
			checkFeederContainerSigs: chanSkipDelay,
			logger:                   conf.Logger,
		}

		// run forever
		for {
			_ = checkFeederContainers(checkFeederContainersConf)
			// no sleep here as this goroutune needs to be relaunched after each container kill
		}
	}()

	containerManagerInitialised = true
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
	dockerCtx, cli, err := GetDockerClient()
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
	}

	return nil
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
	dockerCtx, cli, err := GetDockerClient()
	if err != nil {
		log.Err(err).Msg("error creating docker client")
		return err
	}
	defer cli.Close()

	// read from channel (this blocks until a request comes in)
	containerToStart := <-conf.containersToStartRequests

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

	response.ContainerName = fmt.Sprintf("%s%s", conf.feedInContainerPrefix, containerToStart.ApiKey.String())

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
