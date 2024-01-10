package containers

import (
	"fmt"
	"net"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// struct for requests to the startFeederContainers goroutine start a container
type FeedInContainer struct {
	Lat, Lon   float64   // position of feeder
	Label      string    // feeder label
	ApiKey     uuid.UUID // feeder ATC api key
	FeederCode string    // unique feeder code
	Addr       net.IP    // client IP address
}

// struct for responses from the startFeederContainers goroutine start a container
type startContainerResponse struct {
	Err                 error  // holds error from starting container
	ContainerStartDelay bool   // do we need to wait for container services to start? (pointer to allow calling function to read data)
	ContainerName       string // feed-in container name
	ContainerID         string // feed-in container ID
}

func (feedInContainer *FeedInContainer) Start() (containerID string, err error) {
	// start a feed-in container, return the container ID

	// ensure container manager has been initialised
	if !isInitialised() {
		return containerID, ErrNotInitialised
	}

	// request start of the feed-in container with submission timeout
	select {
	case <-ctx.Done():
		return containerID, ErrContextCancelled
	case containersToStartRequests <- *feedInContainer:
	case <-time.After(5 * time.Second):
		return containerID, ErrTimeoutContainerStartReq
	}

	// wait for request to be actioned
	var startedContainer startContainerResponse
	select {
	case <-ctx.Done():
		return containerID, ErrContextCancelled
	case startedContainer = <-containersToStartResponses:
	case <-time.After(30 * time.Second):
		return containerID, ErrTimeoutContainerStartResp
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

type startFeederContainersConfig struct {
	feedInImageName            string                      // Name of docker image for feed-in containers.
	feedInContainerPrefix      string                      // Feed-in containers will be prefixed with this. Recommend "feed-in-".
	feedInContainerNetwork     string                      // Name of docker network to attach feed-in containers to.
	pwIngestSink               string                      // URL to pass to the --sink flag of pw-ingest in feed-in container.
	containersToStartRequests  chan FeedInContainer        // Channel to receive container start requests from.
	containersToStartResponses chan startContainerResponse // Channel to send container start responses to.
	logger                     zerolog.Logger              // Logging context
}

func startFeederContainers(conf startFeederContainersConfig, containerToStart FeedInContainer) (startContainerResponse, error) {
	// reads startContainerRequests from channel containersToStartRequests and starts container
	// responds via channel containersToStartResponses

	// update log context with function name
	log := conf.logger.With().
		Strs("func", []string{"containers.go", "startFeederContainers"}).
		Logger()

	// set up docker client
	getDockerClientMu.RLock()
	cli, err := getDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		log.Err(err).Msg("error creating docker client")
		return startContainerResponse{}, err
	}
	defer cli.Close()

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
	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{Filters: filterFeedIn})
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
		resp, err := cli.ContainerCreate(ctx, &containerConfig, &containerHostConfig, &networkingConfig, nil, response.ContainerName)
		if err != nil {
			log.Err(err).Msg("could not create feed-in container")
			return startContainerResponse{}, err
		} else {
			log.Debug().Str("container_id", resp.ID).Msg("created feed-in container")
		}

		response.ContainerID = resp.ID

		// start container
		if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
			log.Err(err).Msg("could not start feed-in container")
			return startContainerResponse{}, err
		} else {
			log.Debug().Str("container_id", resp.ID).Msg("started feed-in container")
		}

	}

	return response, err
}
