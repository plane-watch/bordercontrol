package containers

import (
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

// KickFeederHandler handles the NATS request/reply for KickFeeder
func KickFeederHandler(msg *nats.Msg) {

	log := log.With().
		Str("subj", natsSubjFeederKick).
		Logger()

	apiKey, err := uuid.ParseBytes(msg.Data)
	if err != nil {
		log.Err(err).Msg("could not parse api key from message body")
		natsTerm(msg)
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
	err = natsAck(msg)
	if err != nil {
		log.Err(err).Msg("could not acknowledge message")
		return
	}
}

// KickFeeder removes the feeder container used by feeder with apiKey
func KickFeeder(apiKey uuid.UUID) error {

	// get docker client
	getDockerClientMu.RLock()
	cli, err := getDockerClient()
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
	containers, err := dockerContainerList(ctx, cli, types.ContainerListOptions{
		All:     true,
		Filters: filters,
	})

	// ensure exactly one container found
	if len(containers) <= 0 {
		log.Err(ErrContainerNotFound).Msg(ErrContainerNotFound.Error())
		return ErrContainerNotFound
	} else if len(containers) > 1 {
		log.Err(ErrMultipleContainersFound).Msg(ErrMultipleContainersFound.Error())
		return ErrMultipleContainersFound
	}

	// update log context
	log = log.
		With().
		Str("container", containers[0].Names[0]).
		Logger()

	// kill container
	log.Info().Msg("requested to kill feed-in container via NATS request")
	err = dockerContainerRemove(ctx, cli, containers[0].ID, types.ContainerRemoveOptions{
		Force: true,
	})
	if err != nil {
		log.Err(err).Msg("could not remove container")
		return err
	}

	return nil
}
