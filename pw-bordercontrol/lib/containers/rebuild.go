package containers

import (
	"bufio"
	"encoding/json"
	"errors"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/pkg/archive"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

// struct to hold error from docker image build process for JSON unmarshalling
type ErrorLine struct {
	Error       string      `json:"error"`
	ErrorDetail ErrorDetail `json:"errorDetail"`
}

// struct to hold error from docker image build process for JSON unmarshalling
type ErrorDetail struct {
	Message string `json:"message"`
}

// RebuildFeedInImageHandler handles the NATS request/reply for RebuildFeedInImage
func RebuildFeedInImageHandler(msg *nats.Msg) {

	// update log context
	log := log.With().
		Str("subj", natsSubjFeedInImageRebuild).
		Str("context", feedInImageBuildContext).
		Str("dockerfile", feedInImageBuildContextDockerfile).
		Str("image", feedInImageName).
		Logger()

	// get nats instance of this bordercontrol
	forUs, inst, err := natsThisInstance(string(msg.Data))
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
	err = natsRespondMsg(msg, reply)
	if err != nil {
		log.Err(err).Str("subj", natsSubjFeedInImageRebuild).Msg("could not respond")
	}
}

// RebuildFeedInImage will trigger a rebuild of the feed-in image
func RebuildFeedInImage(imageName, buildContext, dockerfile string) (lastLine string, err error) {

	// ensure container manager has been initialised
	if !isInitialised() {
		return lastLine, ErrNotInitialised
	}

	log.Debug().Msg("starting rebuild feed-in image")

	// get docker client
	getDockerClientMu.RLock()
	cli, err := getDockerClient()
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
	res, err := cli.ImageBuild(ctx, tar, opts)
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
