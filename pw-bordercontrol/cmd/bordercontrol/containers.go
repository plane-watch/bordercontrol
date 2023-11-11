package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/google/uuid"

	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
)

// struct for requesting that the startFeederContainers goroutine start a container
type startContainerRequest struct {
	uuid   uuid.UUID       // feeder uuid
	refLat float64         // feeder lat
	refLon float64         // feeder lon
	mux    string          // the multiplexer to upstream the data to
	label  string          // the label of the feeder
	srcIP  net.IP          // client IP address
	wg     *sync.WaitGroup // waitgroup for when container has started
}

func checkFeederContainers(ctx *cli.Context, checkFeederContainerSigs chan os.Signal) error {
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
	// log.Info().Msg("set up docker client")
	dockerCtx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Err(err).Msg("error creating docker client")
		return err
	}
	defer cli.Close()

	// prepare filters to find feed-in containers
	// log.Info().Msg("prepare filter to find feed-in containers")
	filterFeedIn := filters.NewArgs()
	filterFeedIn.Add("name", "feed-in-*")

	// find containers
	// log.Info().Msg("find containers")
	containers, err := cli.ContainerList(dockerCtx, types.ContainerListOptions{Filters: filterFeedIn})
	if err != nil {
		log.Err(err).Msg("error finding containers")
	}

	// for each container...
ContainerLoop:
	for _, container := range containers {

		// log.Info().Str("container", container.Names[0][1:]).Msg("checking container is running latest feed-in image")

		// check containers are running latest feed-in image
		if container.Image != ctx.String("feedinimage") {

			log := log.With().Str("container", container.Names[0][1:]).Logger()

			// If a container is found running an out-of-date image, then remove it.
			// It should be recreated automatically when the client reconnects
			log.Info().Msg("out of date container being killed for recreation")
			err := cli.ContainerRemove(dockerCtx, container.ID, types.ContainerRemoveOptions{Force: true})
			if err != nil {
				log.Err(err).Msg("error killing out of date container")
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
	case s := <-checkFeederContainerSigs:
		log.Info().Str("signal", s.String()).Msg("caught signal, proceeding immediately")
		break
	case <-time.After(sleepTime * time.Second):
	}

	return nil
}

func startFeederContainers(ctx *cli.Context, containersToStart chan startContainerRequest) {
	// reads startContainerRequests from channel containersToStart and starts container

	log := log.With().
		Strs("func", []string{"containers.go", "startFeederContainers"}).
		Logger()

	log.Info().Msg("started")

	// set up docker client
	dockerCtx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Panic().AnErr("err", err).Msg("error creating docker client")
	}
	defer cli.Close()

	for {

		foundContainer := false

		// read from channel (this blocks until a request comes in)
		containerToStart := <-containersToStart

		// prepare logger
		log := log.With().
			Float64("lat", containerToStart.refLat).
			Float64("lon", containerToStart.refLon).
			Str("mux", containerToStart.mux).
			Str("label", containerToStart.label).
			Str("uuid", containerToStart.uuid.String()).
			IPAddr("src", containerToStart.srcIP).
			Logger()

		// determine if container is already running

		// prepare filter to find feed-in container
		filterFeedIn := filters.NewArgs()
		filterFeedIn.Add("name", fmt.Sprintf("feed-in-%s", containerToStart.uuid.String()))

		// find container
		containers, err := cli.ContainerList(dockerCtx, types.ContainerListOptions{Filters: filterFeedIn})
		if err != nil {
			log.Err(err).Msg("error finding feed-in container")
		}
		if len(containers) > 0 {
			foundContainer = true
			log.Info().Msg("feed-in container exists")
		} else {
			foundContainer = false
		}

		// 	containers, err := cli.ContainerList(dockerCtx, types.ContainerListOptions{})
		// 	if err != nil {
		// 		log.Err(err).Msg("error listing docker containers")
		// 		break
		// 	}
		// 	foundContainer := false
		// 	feederContainerName := fmt.Sprintf("feed-in-%s", containerToStart.uuid.String())
		// Outerloop:
		// 	for _, container := range containers {
		// 		for _, cn := range container.Names {
		// 			if cn == fmt.Sprintf("/%s", feederContainerName) {
		// 				foundContainer = true
		// 				break Outerloop
		// 			}
		// 		}
		// 	}

		if !foundContainer {

			// if container is not running, create it

			// prepare environment variables for container
			envVars := [...]string{
				fmt.Sprintf("FEEDER_LAT=%f", containerToStart.refLat),
				fmt.Sprintf("FEEDER_LON=%f", containerToStart.refLon),
				fmt.Sprintf("FEEDER_UUID=%s", containerToStart.uuid),
				"READSB_STATS_EVERY=300",
				"READSB_NET_ENABLE=true",
				"READSB_NET_BEAST_INPUT_PORT=12345",
				"READSB_NET_BEAST_OUTPUT_PORT=30005",
				"READSB_NET_ONLY=true",
				fmt.Sprintf("READSB_NET_CONNECTOR=%s,12345,beast_out", containerToStart.mux),
				"PW_INGEST_PUBLISH=location-updates",
				fmt.Sprintf("PW_INGEST_SINK=%s", ctx.String("pwingestpublish")),
			}

			// prepare labels
			containerLabels := make(map[string]string)
			containerLabels["plane.watch.label"] = containerToStart.label
			containerLabels["plane.watch.mux"] = containerToStart.mux
			containerLabels["plane.watch.lat"] = fmt.Sprintf("%f", containerToStart.refLat)
			containerLabels["plane.watch.lon"] = fmt.Sprintf("%f", containerToStart.refLon)
			containerLabels["plane.watch.uuid"] = containerToStart.uuid.String()

			// prepare container config
			containerConfig := container.Config{
				Image:  ctx.String("feedinimage"),
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
			endpointsConfig["bordercontrol_feeder"] = &network.EndpointSettings{}
			networkingConfig := network.NetworkingConfig{
				EndpointsConfig: endpointsConfig,
			}

			// create feed-in container
			resp, err := cli.ContainerCreate(dockerCtx, &containerConfig, &containerHostConfig, &networkingConfig, nil, feederContainerName)
			if err != nil {
				log.Err(err).Msg("could not create feed-in container")
			} else {
				log.Info().Str("container_id", resp.ID).Msg("created feed-in container")
			}

			// start container
			if err := cli.ContainerStart(dockerCtx, resp.ID, types.ContainerStartOptions{}); err != nil {
				log.Err(err).Msg("could not start feed-in container")
			} else {
				log.Info().Str("container_id", resp.ID).Msg("started feed-in container")
			}
		}

		containerToStart.wg.Done()
	}
	log.Info().Msg("finished")
}
