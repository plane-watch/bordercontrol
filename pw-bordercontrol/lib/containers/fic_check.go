package containers

import (
	"fmt"
	"os"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/rs/zerolog"
)

type checkFeederContainersConfig struct {
	feedInImageName          string         // Name of docker image for feed-in containers.
	feedInContainerPrefix    string         // Feed-in containers will be prefixed with this. Recommend "feed-in-".
	checkFeederContainerSigs chan os.Signal // Channel to receive signals. Received signal will skip sleeps and cause containers to be checked/recreated immediately.
	logger                   zerolog.Logger // Logging context
	stop                     *chan bool
}

func checkFeederContainers(conf checkFeederContainersConfig) (sleepTime time.Duration, err error) {
	// Checks feed-in containers are running the latest image. If they aren't remove them.
	// They will be recreated using the latest image when the client reconnects.

	// TODO: One instance of this goroutine per region/mux would be good.

	var (
		containerRemoved bool // was a container removed this run
	)

	log := conf.logger.With().
		Strs("func", []string{"containers.go", "checkFeederContainers"}).
		Logger()

	// cycles through feed-in containers and recreates if needed

	// set up docker client
	log.Trace().Msg("set up docker client")
	getDockerClientMu.RLock()
	cli, err := getDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		log.Err(err).Msg("error creating docker client")
		return time.Second, err
	}
	defer cli.Close()

	// prepare filters to find feed-in containers
	log.Trace().Msg("prepare filter to find feed-in containers")
	filterFeedIn := filters.NewArgs()
	filterFeedIn.Add("name", fmt.Sprintf("%s*", conf.feedInContainerPrefix))

	// find containers
	log.Trace().Msg("find containers")
	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{Filters: filterFeedIn})
	if err != nil {
		log.Err(err).Msg("error finding containers")
		return time.Second, err
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
			err := cli.ContainerRemove(ctx, container.ID, types.ContainerRemoveOptions{Force: true})
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
		sleepTime = 30 * time.Second
	} else {
		// if no containers have been removed, wait 5 minutes before checking again
		sleepTime = 300 * time.Second
	}

	return sleepTime, err
}
