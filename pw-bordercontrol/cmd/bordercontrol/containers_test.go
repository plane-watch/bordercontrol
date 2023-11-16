package main

import (
	"context"
	"fmt"
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

	TestPWIngestSink = "nats://pw-ingest-sink:12345"
)

var (
	TestDaemon *daemon.Daemon
)

func TestContainers(t *testing.T) {

	var (
		ContainerEnvVarFeederLatOK                bool
		ContainerEnvVarFeederLonOK                bool
		ContainerEnvVarFeederUUIDOK               bool
		ContainerEnvVarFeederReadsbNetConnectorOK bool
		ContainerEnvVarFeederPWIngestSinkOK       bool

		ContainerNetworkOK bool
	)

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// starting test docker daemon
	t.Log("starting test docker daemon")
	TestDaemon = daemon.New(
		t,
		daemon.WithContainerdSocket(TestDaemonDockerSocket),
	)
	TestDaemon.Start(t)

	// prep testing client
	t.Log("prep testing client")
	getDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		log.Debug().Msg("using test docker client")
		cctx := context.Background()
		cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
		return &cctx, cli, nil
	}

	// clean up
	defer func() {
		t.Log("cleaning up")
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
	t.Log("starting startFeederContainers")
	go startFeederContainers(TestFeedInImageName, TestPWIngestSink, containersToStartRequests, containersToStartResponses)

	// start container
	t.Log("requesting container start")
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
	t.Log("waiting for container start")
	startedContainer := <-containersToStartResponses

	// ensure container started without error
	t.Log("ensure container started without error")
	assert.NoError(t, startedContainer.err)

	// get docker client to inspect container
	t.Log("get docker client to inspect container")
	ctx, cli, err := getDockerClient()
	assert.NoError(t, err)

	// inspect container
	t.Log("inspecting container")
	ct, err := cli.ContainerInspect(*ctx, startedContainer.containerID)

	// check environment variables
	t.Log("checking container environment variables")
	for _, e := range ct.Config.Env {
		switch e {
		case fmt.Sprintf("FEEDER_LAT=%f", TestFeederLatitude):
			ContainerEnvVarFeederLatOK = true
		case fmt.Sprintf("FEEDER_LON=%f", TestFeederLongitude):
			ContainerEnvVarFeederLonOK = true
		case fmt.Sprintf("FEEDER_UUID=%s", TestFeederAPIKey):
			ContainerEnvVarFeederUUIDOK = true
		case fmt.Sprintf("READSB_NET_CONNECTOR=%s,12345,beast_out", TestFeederMux):
			ContainerEnvVarFeederReadsbNetConnectorOK = true
		case fmt.Sprintf("PW_INGEST_SINK=%s", TestPWIngestSink):
			ContainerEnvVarFeederPWIngestSinkOK = true
		}
	}
	assert.True(t, ContainerEnvVarFeederLatOK)
	assert.True(t, ContainerEnvVarFeederLonOK)
	assert.True(t, ContainerEnvVarFeederUUIDOK)
	assert.True(t, ContainerEnvVarFeederReadsbNetConnectorOK)
	assert.True(t, ContainerEnvVarFeederPWIngestSinkOK)

	// check container autoremove set to true
	t.Log("check container autoremove set to true")
	assert.True(t, ct.HostConfig.AutoRemove)

	// check container network connection
	for network, _ := range ct.NetworkSettings.Networks {
		if network == feedInContainerNetwork {
			ContainerNetworkOK = true
		}
	}
	assert.Len(t, ct.NetworkSettings.Networks, 1)
	assert.True(t, ContainerNetworkOK)

	// err := checkFeederContainers("foo", testChan)
	// fmt.Println(err)

}
