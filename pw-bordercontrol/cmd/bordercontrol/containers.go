package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/rs/zerolog/log"
)

// struct for requesting that the startFeederContainers goroutine start a container
type startContainerRequest struct {
	clientDetails feederClient // lat, long, mux, label, api key
	srcIP         net.IP       // client IP address

}

type startContainerResponse struct {
	err                 error  // holds error from starting container
	containerStartDelay bool   // do we need to wait for container services to start? (pointer to allow calling function to read data)
	containerName       string // feed-in container name
	containerID         string // feed-in container ID
}

var (
	feedInContainerNetwork = "bordercontrol_feeder" // name of docker network to attach feed-in containers to
)

// the following function is variable-ized so it can be overridden for unit testing
var getDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
	// set up docker client
	cctx := context.Background()
	cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	return &cctx, cli, err
}

type checkFeederContainersConfig struct {
	feedInImageName          string         // Name of docker image for feed-in containers.
	feedInContainerPrefix    string         // Feed-in containers will be prefixed with this. Recommend "feed-in-".
	checkFeederContainerSigs chan os.Signal // Channel to receive signals. Received signal will skip sleeps and cause containers to be checked/recreated immediately.
}

func checkFeederContainers(conf checkFeederContainersConfig) error {
	// Checks feed-in containers are running the latest image. If they aren't remove them.
	// They will be recreated using the latest image when the client reconnects.

	// TODO: One instance of this goroutine per region/mux would be good.

	var (
		containerRemoved bool          // was a container removed this run
		sleepTime        time.Duration // how long to sleep for between runs
	)

	log := log.With().
		Strs("func", []string{"containers.go", "checkFeederContainers"}).
		Logger()

	// cycles through feed-in containers and recreates if needed

	// set up docker client
	log.Trace().Msg("set up docker client")
	dockerCtx, cli, err := getDockerClient()
	if err != nil {
		log.Err(err).Msg("error creating docker client")
		return err
	}
	defer cli.Close()

	// prepare filters to find feed-in containers
	log.Trace().Msg("prepare filter to find feed-in containers")
	filterFeedIn := filters.NewArgs()
	filterFeedIn.Add("name", fmt.Sprintf("%s*", feedInContainerPrefix))

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
	pwIngestPublish            string                      // URL to pass to the --sink flag of pw-ingest in feed-in container.
	containersToStartRequests  chan startContainerRequest  // Channel to receive container start requests from.
	containersToStartResponses chan startContainerResponse // Channel to send container start responses to.
}

func startFeederContainers(conf startFeederContainersConfig) error {
	// reads startContainerRequests from channel containersToStart and starts container

	log := log.With().
		Strs("func", []string{"containers.go", "startFeederContainers"}).
		Logger()

	// log.Trace().Msg("started")

	// set up docker client
	log.Trace().Msg("set up docker client")
	dockerCtx, cli, err := getDockerClient()
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
		// Float64("lat", containerToStart.clientDetails.refLat).
		// Float64("lon", containerToStart.clientDetails.refLon).
		Str("mux", containerToStart.clientDetails.mux).
		Str("label", containerToStart.clientDetails.label).
		Str("uuid", containerToStart.clientDetails.clientApiKey.String()).
		Str("src", containerToStart.srcIP.String()).
		Str("code", containerToStart.clientDetails.feederCode).
		Logger()

	// determine if container is already running

	response.containerName = fmt.Sprintf("%s%s", conf.feedInContainerPrefix, containerToStart.clientDetails.clientApiKey.String())

	// prepare filter to find feed-in container
	filterFeedIn := filters.NewArgs()
	filterFeedIn.Add("name", response.containerName)
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
		response.containerStartDelay = true

		// prepare environment variables for container
		envVars := [...]string{
			fmt.Sprintf("FEEDER_LAT=%f", containerToStart.clientDetails.refLat),
			fmt.Sprintf("FEEDER_LON=%f", containerToStart.clientDetails.refLon),
			fmt.Sprintf("FEEDER_UUID=%s", containerToStart.clientDetails.clientApiKey.String()),
			fmt.Sprintf("FEEDER_TAG=%s", containerToStart.clientDetails.feederCode),
			"PW_INGEST_PUBLISH=location-updates",
			"PW_INGEST_INPUT_MODE=listen",
			"PW_INGEST_INPUT_PROTO=beast",
			"PW_INGEST_INPUT_ADDR=0.0.0.0",
			"PW_INGEST_INPUT_PORT=12345",
			fmt.Sprintf("PW_INGEST_SINK=%s", conf.pwIngestPublish),
		}

		// prepare labels
		containerLabels := make(map[string]string)
		containerLabels["plane.watch.label"] = containerToStart.clientDetails.label
		containerLabels["plane.watch.mux"] = containerToStart.clientDetails.mux
		containerLabels["plane.watch.lat"] = fmt.Sprintf("%f", containerToStart.clientDetails.refLat)
		containerLabels["plane.watch.lon"] = fmt.Sprintf("%f", containerToStart.clientDetails.refLon)
		containerLabels["plane.watch.uuid"] = containerToStart.clientDetails.clientApiKey.String()

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
		endpointsConfig[feedInContainerNetwork] = &network.EndpointSettings{}
		networkingConfig := network.NetworkingConfig{
			EndpointsConfig: endpointsConfig,
		}

		// create feed-in container
		resp, err := cli.ContainerCreate(*dockerCtx, &containerConfig, &containerHostConfig, &networkingConfig, nil, response.containerName)
		if err != nil {
			log.Err(err).Msg("could not create feed-in container")
			response.err = err
		} else {
			log.Debug().Str("container_id", resp.ID).Msg("created feed-in container")
		}

		response.containerID = resp.ID

		// start container
		if err := cli.ContainerStart(*dockerCtx, resp.ID, types.ContainerStartOptions{}); err != nil {
			log.Err(err).Msg("could not start feed-in container")
			response.err = err
		} else {
			log.Debug().Str("container_id", resp.ID).Msg("started feed-in container")
		}

		// send response
		conf.containersToStartResponses <- response

	}
	// log.Trace().Msg("finished")
	return nil
}
