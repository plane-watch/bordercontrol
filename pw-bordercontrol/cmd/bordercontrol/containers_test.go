package main

import (
	"context"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/docker/docker/testutil/daemon"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

const (
	TestDaemonDockerSocket = "/run/containerd/containerd.sock"

	TestFeedInImageName = "ubuntu"

	// mock feeder details
	TestFeederAPIKey    = "6261B9C8-25C1-4B67-A5A2-51FC688E8A25"
	TestFeederLabel     = "Test Feeder 123"
	TestFeederLatitude  = 123.456789
	TestFeederLongitude = 98.765432
	TestFeederMux       = "test-mux"
)

var (
	TestDaemon *daemon.Daemon
)

func TestPrepTestEnvironment(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// start test docker daemon
	TestDaemon = daemon.New(
		t,
		daemon.WithContainerdSocket(TestDaemonDockerSocket),
	)
	TestDaemon.Start(t)

	// prep testing client
	getDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		log.Debug().Msg("using test docker client")
		cctx := context.Background()
		cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
		return &cctx, cli, nil
	}

	// clean up
	defer func() {
		TestDaemon.Stop(t)
		TestDaemon.Cleanup(t)
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
	containersToStartRequests := make(chan startContainerRequest)
	defer close(containersToStartRequests)

	// prepare channel for container start responses
	containersToStartResponses := make(chan startContainerResponse)
	defer close(containersToStartResponses)

	// start process to test
	go startFeederContainers(TestFeedInImageName, "", containersToStartRequests, containersToStartResponses)

	// start container
	containersToStartRequests <- startContainerRequest{
		clientDetails: &feederClient{
			clientApiKey: uuid.MustParse(TestFeederAPIKey),
			refLat:       TestFeederLatitude,
			refLon:       TestFeederLongitude,
			mux:          TestFeederMux,
			label:        TestFeederMux,
		},
		srcIP: net.IPv4(127, 0, 0, 1),
	}

	// wait for container to start
	startedContainer := <-containersToStartResponses

	// ensure container started without error
	assert.NoError(t, startedContainer.err)

	// err := checkFeederContainers("foo", testChan)
	// fmt.Println(err)

}
