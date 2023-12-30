package containers

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
)

var (
	TestDaemonDockerSocket = "/run/containerd/containerd.sock"

	TestFeedInImageName        = "wardsco/sleep:latest"
	TestFeedInContainerPrefix  = "test-feed-in-"
	TestFeedInContainerNetwork = "bridge"

	// mock feeder details
	TestFeederAPIKey    = uuid.MustParse("6261B9C8-25C1-4B67-A5A2-51FC688E8A25") // not a real feeder api key, generated with uuidgen
	TestFeederLabel     = "Test Feeder 123"
	TestFeederLatitude  = 123.456789
	TestFeederLongitude = 98.765432
	TestFeederMux       = "test-mux"
	TestFeederCode      = "ABCD-1234"
	TestFeederAddr      = net.IPv4(127, 0, 0, 1)

	TestPWIngestSink = "nats://pw-ingest-sink:12345"
)

func TestGetDockerClient(t *testing.T) {
	_, cli, err := GetDockerClient()
	assert.NoError(t, err)
	err = cli.Close()
	assert.NoError(t, err)
}

func TestContainers(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// starting test docker daemon
	t.Log("starting test docker daemon")
	TestDaemon := daemon.New(
		t,
		daemon.WithContainerdSocket(TestDaemonDockerSocket),
	)
	TestDaemon.Start(t)
	defer func(t *testing.T) {
		TestDaemon.Stop(t)
		TestDaemon.Cleanup(t)
	}(t)

	// prep testing client
	t.Log("prep testing client")
	GetDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		log.Debug().Msg("using test docker client")
		cctx := context.Background()
		cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
		return &cctx, cli, nil
	}

	// get docker client
	t.Log("get docker client to inspect container")
	ctx, cli, err := GetDockerClient()
	assert.NoError(t, err)

	// pull test image
	t.Log("pull test image")
	imageirc, err := cli.ImagePull(*ctx, TestFeedInImageName, types.ImagePullOptions{})
	assert.NoError(t, err)
	defer imageirc.Close()

	// load test image
	t.Log("load test image")
	_, err = cli.ImageLoad(*ctx, imageirc, false)
	assert.NoError(t, err)

	// continually send SIGUSR1 to prevent checkFeederContainers from sleeping
	t.Log("continually send SIGUSR1 to prevent checkFeederContainers from sleeping")
	testChan := make(chan os.Signal)
	go func() {
		for {
			testChan <- syscall.SIGUSR1
			time.Sleep(time.Second)
		}
	}()

	// start feed-in container - will fail, no init
	t.Run("start feed-in container no init", func(t *testing.T) {
		fic := FeedInContainer{
			Lat:        TestFeederLatitude,
			Lon:        TestFeederLongitude,
			Label:      TestFeederLabel,
			ApiKey:     TestFeederAPIKey,
			FeederCode: TestFeederCode,
			Addr:       TestFeederAddr,
		}
		_, err = fic.Start()
		assert.Error(t, err)
		assert.Equal(t, "container manager has not been initialised", err.Error())
	})

	// start feed-in container - will fail, start timeout
	t.Run("start feed-in container start timeout", func(t *testing.T) {
		containerManagerInitialised = true
		fic := FeedInContainer{
			Lat:        TestFeederLatitude,
			Lon:        TestFeederLongitude,
			Label:      TestFeederLabel,
			ApiKey:     TestFeederAPIKey,
			FeederCode: TestFeederCode,
			Addr:       TestFeederAddr,
		}
		_, err = fic.Start()
		assert.Error(t, err)
		assert.Equal(t, "container manager has not been initialised", err.Error())
		containerManagerInitialised = false
	})

	// init container manager
	cm := ContainerManager{
		FeedInImageName:                    TestFeedInImageName,
		FeedInContainerPrefix:              TestFeedInContainerPrefix,
		FeedInContainerNetwork:             TestFeedInContainerNetwork,
		SignalSkipContainerRecreationDelay: syscall.SIGUSR1,
		PWIngestSink:                       TestPWIngestSink,
	}
	t.Run("Running ContainerManager.Init()", func(t *testing.T) {
		cm.Init()
	})

	var cid string

	// start feed-in container
	t.Run("start feed-in container", func(t *testing.T) {
		t.Log("requesting container start")
		fic := FeedInContainer{
			Lat:        TestFeederLatitude,
			Lon:        TestFeederLongitude,
			Label:      TestFeederLabel,
			ApiKey:     TestFeederAPIKey,
			FeederCode: TestFeederCode,
			Addr:       TestFeederAddr,
		}
		cid, err = fic.Start()
		assert.NoError(t, err)
	})

	var ct types.ContainerJSON

	// inspect container
	t.Run("inspect container", func(t *testing.T) {
		ct, err = cli.ContainerInspect(*ctx, cid)
		assert.NoError(t, err)
	})

	// check environment variables
	t.Run("check container environment variables", func(t *testing.T) {
		envVars := make(map[string]string)
		for _, e := range ct.Config.Env {
			envVars[strings.Split(e, "=")[0]] = strings.Split(e, "=")[1]
		}

		assert.Equal(t, fmt.Sprintf("%f", TestFeederLatitude), envVars["FEEDER_LAT"])
		assert.Equal(t, fmt.Sprintf("%f", TestFeederLongitude), envVars["FEEDER_LON"])
		assert.Equal(t, strings.ToLower(fmt.Sprintf("%s", TestFeederAPIKey)), envVars["FEEDER_UUID"])
		assert.Equal(t, TestFeederCode, envVars["FEEDER_TAG"])
		assert.Equal(t, TestPWIngestSink, envVars["PW_INGEST_SINK"])
		assert.Equal(t, "location-updates", envVars["PW_INGEST_PUBLISH"])
		assert.Equal(t, "listen", envVars["PW_INGEST_INPUT_MODE"])
		assert.Equal(t, "beast", envVars["PW_INGEST_INPUT_PROTO"])
		assert.Equal(t, "0.0.0.0", envVars["PW_INGEST_INPUT_ADDR"])
		assert.Equal(t, "12345", envVars["PW_INGEST_INPUT_PORT"])
	})

	// check container autoremove set to true
	t.Run("check container autoremove", func(t *testing.T) {
		assert.True(t, ct.HostConfig.AutoRemove)
	})

	// check container network connection
	t.Run("check container network", func(t *testing.T) {
		var ContainerNetworkOK bool
		for network, _ := range ct.NetworkSettings.Networks {
			if network == TestFeedInContainerNetwork {
				ContainerNetworkOK = true
			}
		}
		assert.Len(t, ct.NetworkSettings.Networks, 1)
		assert.True(t, ContainerNetworkOK)
	})

}

// func TestContainersWithKill(t *testing.T) {

// 	// start statsManager testing server
// 	statsManagerMu.RLock()
// 	if statsManagerAddr == "" {
// 		statsManagerMu.RUnlock()
// 		// get address for testing
// 		nl, err := nettest.NewLocalListener("tcp4")
// 		assert.NoError(t, err)
// 		nl.Close()
// 		go statsManager(nl.Addr().String())

// 		// wait for server to come up
// 		time.Sleep(1 * time.Second)
// 	} else {
// 		statsManagerMu.RUnlock()
// 	}

// 	// check prom metrics
// 	t.Log("check prom container metrics with current image")
// 	feedInContainerPrefix = TestfeedInContainerPrefix
// 	feedInImage = TestFeedInImageName
// 	statsManagerMu.RLock()
// 	promMetrics := getMetricsFromTestServer(t, fmt.Sprintf("http://%s/metrics", statsManagerAddr))
// 	statsManagerMu.RUnlock()
// 	expectedMetrics := []string{
// 		`pw_bordercontrol_feedercontainers_image_current 1`,
// 		`pw_bordercontrol_feedercontainers_image_not_current 0`,
// 	}
// 	checkPromMetricsExist(t, promMetrics, expectedMetrics)

// 	//
// 	// check prom metrics
// 	t.Log("check prom container metrics with not current image")
// 	feedInContainerPrefix = TestfeedInContainerPrefix
// 	feedInImage = "foo"
// 	statsManagerMu.RLock()
// 	promMetrics = getMetricsFromTestServer(t, fmt.Sprintf("http://%s/metrics", statsManagerAddr))
// 	statsManagerMu.RUnlock()
// 	expectedMetrics = []string{
// 		`pw_bordercontrol_feedercontainers_image_current 0`,
// 		`pw_bordercontrol_feedercontainers_image_not_current 1`,
// 	}
// 	checkPromMetricsExist(t, promMetrics, expectedMetrics)

// 	// test checkFeederContainers
// 	// by passing "foo" as the feedInImageName, it should kill the previously created container
// 	t.Log("running checkFeederContainers")
// 	conf := &checkFeederContainersConfig{
// 		feedInImageName:          "foo",
// 		feedInContainerPrefix:    TestfeedInContainerPrefix,
// 		checkFeederContainerSigs: testChan,
// 	}
// 	err = checkFeederContainers(*conf)
// 	assert.NoError(t, err)

// 	// wait for container to be removed
// 	t.Log("wait for container to be removed")
// 	time.Sleep(time.Second * 15)

// 	// ensure container has been killed
// 	t.Log("ensure container has been killed")
// 	_, err = cli.ContainerInspect(*ctx, startedContainer.containerID)
// 	assert.Error(t, err)

// 	// clean up
// 	t.Log("cleaning up")
// 	TestDaemon.Stop(t)
// 	TestDaemon.Cleanup(t)
// }

// func TestContainersWithoutKill(t *testing.T) {

// 	// set logging to trace level
// 	zerolog.SetGlobalLevel(zerolog.TraceLevel)

// 	// starting test docker daemon
// 	t.Log("starting test docker daemon")
// 	TestDaemon := daemon.New(
// 		t,
// 		daemon.WithContainerdSocket(TestDaemonDockerSocket),
// 	)
// 	TestDaemon.Start(t)

// 	// prep testing client
// 	t.Log("prep testing client")
// 	getDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
// 		log.Debug().Msg("using test docker client")
// 		cctx := context.Background()
// 		cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
// 		return &cctx, cli, nil
// 	}

// 	// get docker client
// 	t.Log("get docker client to inspect container")
// 	ctx, cli, err := getDockerClient()
// 	assert.NoError(t, err)

// 	// ensure test image is downloaded
// 	t.Log("pull test image")
// 	imageirc, err := cli.ImagePull(*ctx, TestFeedInImageName, types.ImagePullOptions{})
// 	assert.NoError(t, err)
// 	defer imageirc.Close()

// 	t.Log("load test image")
// 	_, err = cli.ImageLoad(*ctx, imageirc, false)
// 	assert.NoError(t, err)

// 	// // ensure test network is created
// 	// t.Log("ensure test network is created")
// 	// feedInContainerNetwork = "test-feed-in-net"
// 	// _, err = cli.NetworkCreate(*ctx, feedInContainerNetwork, types.NetworkCreate{
// 	// 	Driver: "null",
// 	// })
// 	// assert.NoError(t, err)
// 	feedInContainerNetwork = "bridge"

// 	// continually send sighup1 to prevent checkFeederContainers from sleeping
// 	testChan := make(chan os.Signal)
// 	go func() {
// 		for {
// 			testChan <- syscall.SIGUSR1
// 			time.Sleep(time.Second)
// 		}
// 	}()

// 	// prepare channel for container start requests
// 	containersToStartRequests := make(chan startContainerRequest)
// 	defer close(containersToStartRequests)

// 	// prepare channel for container start responses
// 	containersToStartResponses := make(chan startContainerResponse)
// 	defer close(containersToStartResponses)

// 	// start process to test
// 	t.Log("starting startFeederContainers")
// 	confA := &startFeederContainersConfig{
// 		feedInImageName:            TestFeedInImageName,
// 		feedInContainerPrefix:      TestfeedInContainerPrefix,
// 		pwIngestPublish:            TestPWIngestSink,
// 		containersToStartRequests:  containersToStartRequests,
// 		containersToStartResponses: containersToStartResponses,
// 	}
// 	go startFeederContainers(*confA)

// 	// start container
// 	t.Log("requesting container start")
// 	containersToStartRequests <- startContainerRequest{
// 		clientDetails: feederClient{
// 			clientApiKey: uuid.MustParse(TestFeederAPIKey),
// 			refLat:       TestFeederLatitude,
// 			refLon:       TestFeederLongitude,
// 			mux:          TestFeederMux,
// 			label:        TestFeederMux,
// 		},
// 		srcIP: net.IPv4(127, 0, 0, 1),
// 	}

// 	// wait for container to start
// 	t.Log("waiting for container start")
// 	startedContainer := <-containersToStartResponses

// 	// ensure container started without error
// 	t.Log("ensure container started without error")
// 	assert.NoError(t, startedContainer.err)

// 	// test checkFeederContainers
// 	t.Log("running checkFeederContainers")
// 	confB := &checkFeederContainersConfig{
// 		feedInImageName:          TestFeedInImageName,
// 		feedInContainerPrefix:    TestfeedInContainerPrefix,
// 		checkFeederContainerSigs: testChan,
// 	}
// 	err = checkFeederContainers(*confB)
// 	assert.NoError(t, err)

// 	// ensure container has been killed
// 	t.Log("ensure container has not been killed")
// 	_, err = cli.ContainerInspect(*ctx, startedContainer.containerID)
// 	assert.NoError(t, err)

// 	// clean up
// 	t.Log("cleaning up")
// 	TestDaemon.Stop(t)
// 	TestDaemon.Cleanup(t)

// }
