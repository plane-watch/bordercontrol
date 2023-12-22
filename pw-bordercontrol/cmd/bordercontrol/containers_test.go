package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/testutil/daemon"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

const (
	TestDaemonDockerSocket = "/run/containerd/containerd.sock"

	TestFeedInImageName   = "wardsco/sleep:latest"
	TestfeedInImagePrefix = "test-feed-in-"

	// mock feeder details
	TestFeederAPIKey    = "6261B9C8-25C1-4B67-A5A2-51FC688E8A25" // not a real feeder api key, generated with uuidgen
	TestFeederLabel     = "Test Feeder 123"
	TestFeederLatitude  = 123.456789
	TestFeederLongitude = 98.765432
	TestFeederMux       = "test-mux"
	TestFeederCode      = "ABCD-1234"

	TestPWIngestSink = "nats://pw-ingest-sink:12345"
)

func TestContainersWithKill(t *testing.T) {

	var (
		ContainerNetworkOK bool
	)

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// starting test docker daemon
	t.Log("starting test docker daemon")
	TestDaemon := daemon.New(
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

	// get docker client
	t.Log("get docker client to inspect container")
	ctx, cli, err := getDockerClient()
	assert.NoError(t, err)

	// ensure test image is downloaded
	t.Log("pull test image")
	imageirc, err := cli.ImagePull(*ctx, TestFeedInImageName, types.ImagePullOptions{})
	assert.NoError(t, err)
	defer imageirc.Close()

	t.Log("load test image")
	_, err = cli.ImageLoad(*ctx, imageirc, false)
	assert.NoError(t, err)

	// // ensure test network is created
	// t.Log("ensure test network is created")
	// feedInContainerNetwork = "test-feed-in-net"
	// _, err = cli.NetworkCreate(*ctx, feedInContainerNetwork, types.NetworkCreate{
	// 	Driver: "bridge",
	// })
	// assert.NoError(t, err)
	feedInContainerNetwork = "bridge"

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

	// prepare stop channel for startFeederContainers
	stopChan := make(chan bool)

	// start process to test
	t.Log("starting startFeederContainers")
	go startFeederContainers(TestFeedInImageName, TestfeedInImagePrefix, TestPWIngestSink, containersToStartRequests, containersToStartResponses, stopChan)

	// start container
	t.Log("requesting container start")
	containersToStartRequests <- startContainerRequest{
		clientDetails: &feederClient{
			clientApiKey: uuid.MustParse(TestFeederAPIKey),
			refLat:       TestFeederLatitude,
			refLon:       TestFeederLongitude,
			mux:          TestFeederMux,
			label:        TestFeederLabel,
			feederCode:   TestFeederCode,
		},
		srcIP: net.IPv4(127, 0, 0, 1),
	}

	// wait for container to start
	t.Log("waiting for container start")
	startedContainer := <-containersToStartResponses

	// ensure container started without error
	t.Log("ensure container started without error")
	assert.NoError(t, startedContainer.err)

	// inspect container
	t.Log("inspecting container")
	ct, err := cli.ContainerInspect(*ctx, startedContainer.containerID)

	// check environment variables
	t.Log("checking container environment variables")
	envVars := make(map[string]string)
	for _, e := range ct.Config.Env {
		envVars[strings.Split(e, "=")[0]] = strings.Split(e, "=")[1]
	}

	assert.Equal(t, fmt.Sprintf("%f", TestFeederLatitude), envVars["FEEDER_LAT"])
	assert.Equal(t, fmt.Sprintf("%f", TestFeederLongitude), envVars["FEEDER_LON"])
	assert.Equal(t, fmt.Sprintf("%s", TestFeederAPIKey), envVars["FEEDER_UUID"])
	assert.Equal(t, TestFeederCode, envVars["FEEDER_TAG"])
	assert.Equal(t, TestPWIngestSink, envVars["PW_INGEST_SINK"])
	assert.Equal(t, "location-updates", envVars["PW_INGEST_PUBLISH"])
	assert.Equal(t, "listen", envVars["PW_INGEST_INPUT_MODE"])
	assert.Equal(t, "beast", envVars["PW_INGEST_INPUT_PROTO"])
	assert.Equal(t, "0.0.0.0", envVars["PW_INGEST_INPUT_ADDR"])
	assert.Equal(t, "12345", envVars["PW_INGEST_INPUT_PORT"])

	// check container autoremove set to true
	t.Log("check container autoremove set to true")
	assert.True(t, ct.HostConfig.AutoRemove)

	// check container network connection
	t.Log("check container network connection")
	for network, _ := range ct.NetworkSettings.Networks {
		if network == feedInContainerNetwork {
			ContainerNetworkOK = true
		}
	}
	assert.Len(t, ct.NetworkSettings.Networks, 1)
	assert.True(t, ContainerNetworkOK)

	// start statsManager testing server
	statsManagerMu.RLock()
	if statsManagerAddr == "" {
		statsManagerMu.RUnlock()
		// get address for testing
		nl, err := nettest.NewLocalListener("tcp4")
		assert.NoError(t, err)
		nl.Close()
		go statsManager(nl.Addr().String())

		// wait for server to come up
		time.Sleep(1 * time.Second)
	} else {
		statsManagerMu.RUnlock()
	}

	// check prom metrics
	t.Log("check prom container metrics with current image")
	feedInContainerPrefix = TestfeedInImagePrefix
	feedInImage = TestFeedInImageName
	statsManagerMu.RLock()
	promMetrics := getMetricsFromTestServer(t, fmt.Sprintf("http://%s/metrics", statsManagerAddr))
	statsManagerMu.RUnlock()
	expectedMetrics := []string{
		`pw_bordercontrol_feedercontainers_image_current 1`,
		`pw_bordercontrol_feedercontainers_image_not_current 0`,
	}
	checkPromMetricsExist(t, promMetrics, expectedMetrics)

	//
	// check prom metrics
	t.Log("check prom container metrics with not current image")
	feedInContainerPrefix = TestfeedInImagePrefix
	feedInImage = "foo"
	statsManagerMu.RLock()
	promMetrics = getMetricsFromTestServer(t, fmt.Sprintf("http://%s/metrics", statsManagerAddr))
	statsManagerMu.RUnlock()
	expectedMetrics = []string{
		`pw_bordercontrol_feedercontainers_image_current 0`,
		`pw_bordercontrol_feedercontainers_image_not_current 1`,
	}
	checkPromMetricsExist(t, promMetrics, expectedMetrics)

	// test checkFeederContainers
	// by passing "foo" as the feedInImageName, it should kill the previously created container
	t.Log("running checkFeederContainers")
	err = checkFeederContainers("foo", TestfeedInImagePrefix, testChan)
	assert.NoError(t, err)

	// wait for container to be removed
	t.Log("wait for container to be removed")
	time.Sleep(time.Second * 15)

	// ensure container has been killed
	t.Log("ensure container has been killed")
	_, err = cli.ContainerInspect(*ctx, startedContainer.containerID)
	assert.Error(t, err)

	// clean up
	t.Log("cleaning up")
	stopChan <- true
	TestDaemon.Stop(t)
	TestDaemon.Cleanup(t)
}

func TestContainersWithoutKill(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// starting test docker daemon
	t.Log("starting test docker daemon")
	TestDaemon := daemon.New(
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

	// get docker client
	t.Log("get docker client to inspect container")
	ctx, cli, err := getDockerClient()
	assert.NoError(t, err)

	// ensure test image is downloaded
	t.Log("pull test image")
	imageirc, err := cli.ImagePull(*ctx, TestFeedInImageName, types.ImagePullOptions{})
	assert.NoError(t, err)
	defer imageirc.Close()

	t.Log("load test image")
	_, err = cli.ImageLoad(*ctx, imageirc, false)
	assert.NoError(t, err)

	// // ensure test network is created
	// t.Log("ensure test network is created")
	// feedInContainerNetwork = "test-feed-in-net"
	// _, err = cli.NetworkCreate(*ctx, feedInContainerNetwork, types.NetworkCreate{
	// 	Driver: "null",
	// })
	// assert.NoError(t, err)
	feedInContainerNetwork = "bridge"

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

	stopChan := make(chan bool)
	defer close(stopChan)

	// start process to test
	t.Log("starting startFeederContainers")
	go startFeederContainers(TestFeedInImageName, TestfeedInImagePrefix, TestPWIngestSink, containersToStartRequests, containersToStartResponses, stopChan)

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

	// test checkFeederContainers
	t.Log("running checkFeederContainers")
	err = checkFeederContainers(TestFeedInImageName, TestfeedInImagePrefix, testChan)
	assert.NoError(t, err)

	// ensure container has been killed
	t.Log("ensure container has not been killed")
	_, err = cli.ContainerInspect(*ctx, startedContainer.containerID)
	assert.NoError(t, err)

	// clean up
	t.Log("cleaning up")
	stopChan <- true
	TestDaemon.Stop(t)
	TestDaemon.Cleanup(t)

}
