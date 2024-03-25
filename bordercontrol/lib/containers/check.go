package containers

import (
	"fmt"
	"os"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/rs/zerolog"
)

// checkFeederContainersConfig provides configuration for function checkFeederContainers
type checkFeederContainersConfig struct {

	// Name of docker image for feed-in containers.
	feedInImageName string

	// Feed-in containers will be prefixed with this. Recommend "feed-in-".
	feedInContainerPrefix string

	// Channel to receive signals. Received signal will skip sleeps and cause containers to be checked/recreated immediately.
	checkFeederContainerSigs chan os.Signal

	// Logging context
	logger zerolog.Logger
}

// checkFeederContainers checks feed-in containers are running the latest feed-in image.
// If they aren't remove them. They will be recreated using the latest image when the client reconnects.
// Returns a time.Duration sleepTime telling the calling function how long to sleep before calling this function again.
func checkFeederContainers(conf checkFeederContainersConfig) (sleepTime time.Duration, err error) {

	var (
		// was a container removed this run
		containerRemoved bool
	)

	// update log context
	log := conf.logger.With().
		Logger()

	// cycles through feed-in containers and recreates if needed

	// set up docker client
	getDockerClientMu.RLock()
	cli, err := getDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		log.Err(err).Msg("error creating docker client")
		return time.Second, err
	}
	defer cli.Close()

	// prepare filters to find feed-in containers
	filterFeedIn := filters.NewArgs()
	filterFeedIn.Add("name", fmt.Sprintf("%s*", conf.feedInContainerPrefix))

	// find containers
	containers, err := cli.ContainerList(ctx, container.ListOptions{Filters: filterFeedIn})
	if err != nil {
		log.Err(err).Msg("error finding containers")
		return time.Second, err
	}

	// for each container...
ContainerLoop:
	for _, c := range containers {

		// check containers are running latest feed-in image
		if c.Image != conf.feedInImageName {

			// update log context with container name
			log := log.With().Str("container", c.Names[0][1:]).Logger()

			// If a container is found running an out-of-date image, then remove it.
			// It should be recreated automatically when the client reconnects
			log.Info().Msg("out of date feed-in container being killed for recreation")
			err := cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
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
