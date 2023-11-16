package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/docker/docker/client"
	"github.com/docker/docker/testutil/daemon"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const testDaemonDockerSocket = "/run/containerd/containerd.sock"

var testDaemon *daemon.Daemon

func TestPrepTestEnvironment(t *testing.T) {

	// start test docker daemon
	testDaemon = daemon.New(
		t,
		daemon.WithContainerdSocket(testDaemonDockerSocket),
	)
	testDaemon.Start(t)

	// prep testing client
	getDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		log.Debug().Msg("using test docker client")
		cctx := context.Background()
		cli = testDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
		return &cctx, cli, nil
	}

	// clean up
	defer func() {
		testDaemon.Stop(t)
		testDaemon.Cleanup(t)
	}()

	testChan := make(chan os.Signal)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	err := checkFeederContainers("foo", testChan)
	fmt.Println(err)

}
