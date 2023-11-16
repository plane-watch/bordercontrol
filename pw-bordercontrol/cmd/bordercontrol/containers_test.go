package main

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/docker/docker/testutil/daemon"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const testDaemonDockerSocket = "/run/containerd/containerd.sock"

var (
	testDaemon *daemon.Daemon

	testFeedInImageName = "ubuntu"
)

func TestPrepTestEnvironment(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

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

	// continually send sighup1 to prevent checkFeederContainers from sleeping
	testChan := make(chan os.Signal)
	go func() {
		for {
			testChan <- syscall.SIGUSR1
			time.Sleep(time.Second)
		}
	}()

	// prepare channel for container start requests
	containersToStart := make(chan startContainerRequest)
	defer close(containersToStart)

	// go startFeederContainers()

	err := checkFeederContainers("foo", testChan)
	fmt.Println(err)

}
