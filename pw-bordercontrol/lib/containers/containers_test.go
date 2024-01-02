package containers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
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

	TestFeedInImageNameFirst   = "wardsco/sleep:latest"
	TestFeedInImageNameSecond  = "itisfoundation/sleeper:latest"
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
	TestPWIngestSink    = "nats://pw-ingest-sink:12345"
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
	tmpDir, err := os.MkdirTemp("", "pw-bordercontrol-go-test-*") // get temp path for test docker daemon
	assert.NoError(t, err)
	TestDaemon, err := daemon.NewDaemon( // create test docker daemon
		tmpDir,
		daemon.WithContainerdSocket(TestDaemonDockerSocket),
	)
	assert.NoError(t, err)
	TestDaemon.Start(t) // start test docker daemon
	t.Cleanup(func() {  // defer cleanup of test docker daemon
		TestDaemon.Cleanup(t)
		TestDaemon.Stop(t)
		TestDaemon.Kill()
		os.RemoveAll(tmpDir)
	})

	// prep broken docker client
	t.Log("prep broken testing docker client")
	GetDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		cctx := context.Background()
		cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
		return &cctx, cli, errors.New("error injected for testing")
	}

	// test checkFeederContainers with broken docker client
	t.Run("test checkFeederContainers with broken docker client", func(t *testing.T) {
		checkFeederContainersConf := checkFeederContainersConfig{}
		err := checkFeederContainers(checkFeederContainersConf)
		assert.Error(t, err)
		assert.Equal(t, "error injected for testing", err.Error())
	})

	// test startFeederContainers with broken docker client
	t.Run("test startFeederContainers with broken docker client", func(t *testing.T) {
		startFeederContainersConf := startFeederContainersConfig{}
		err := startFeederContainers(startFeederContainersConf)
		assert.Error(t, err)
		assert.Equal(t, "error injected for testing", err.Error())
	})

	// prep invalid testing docker client
	t.Log("prep invalid testing docker client")
	GetDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		cctx := context.Background()
		cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
		return &cctx, cli, nil
	}
	TestDaemon.Stop(t) // make client invalid

	// test checkFeederContainers with invalid client
	t.Run("test checkFeederContainers with invalid docker client", func(t *testing.T) {
		checkFeederContainersConf := checkFeederContainersConfig{}
		err := checkFeederContainers(checkFeederContainersConf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Cannot connect to the Docker daemon at")
	})

	// test startFeederContainers with invalid docker client
	t.Run("test startFeederContainers with invalid docker client", func(t *testing.T) {

		// prep channels
		containersToStartRequests = make(chan FeedInContainer)
		containersToStartResponses = make(chan startContainerResponse)

		// prep config for startFeederContainers
		startFeederContainersConf := startFeederContainersConfig{
			containersToStartRequests:  containersToStartRequests,
			containersToStartResponses: containersToStartResponses,
			logger:                     log.Logger,
		}

		// prep waitgroup to wait for goroutine
		wg := sync.WaitGroup{}

		// start startFeederContainers in background
		wg.Add(1)
		go func(t *testing.T) {
			err := startFeederContainers(startFeederContainersConf)
			assert.NoError(t, err)
			wg.Done()
		}(t)

		// send request to start container
		fic := FeedInContainer{
			Lat:        TestFeederLatitude,
			Lon:        TestFeederLongitude,
			Label:      TestFeederLabel,
			ApiKey:     TestFeederAPIKey,
			FeederCode: TestFeederCode,
			Addr:       TestFeederAddr,
		}
		select {
		case containersToStartRequests <- fic:
			t.Log("sent to containersToStartRequests")
		case <-time.After(time.Second * 31):
			assert.Fail(t, "timeout sending to chan containersToStartRequests")
		}

		// receive response for start container
		select {
		case r := <-containersToStartResponses:
			assert.Error(t, r.Err)
			assert.Contains(t, r.Err.Error(), "Cannot connect to the Docker daemon at")
		case <-time.After(time.Second * 31):
			assert.Fail(t, "timeout receiving from chan containersToStartResponses")
		}

		// wait for goroutine to finish
		wg.Wait()
	})

	// prep test env docker client
	t.Log("prep working testing client")
	TestDaemon.Start(t)
	GetDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		cctx := context.Background()
		cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
		return &cctx, cli, nil
	}

	// get docker client
	t.Log("get docker client to inspect container")
	ctx, cli, err := GetDockerClient()
	assert.NoError(t, err)

	// pull test image
	t.Logf("pull test image: %s", TestFeedInImageNameFirst)
	imageircFirst, err := cli.ImagePull(*ctx, TestFeedInImageNameFirst, types.ImagePullOptions{})
	assert.NoError(t, err)
	t.Cleanup(func() { imageircFirst.Close() })

	// pull test image
	t.Logf("pull test image: %s", TestFeedInImageNameSecond)
	imageircSecond, err := cli.ImagePull(*ctx, TestFeedInImageNameSecond, types.ImagePullOptions{})
	assert.NoError(t, err)
	t.Cleanup(func() { imageircSecond.Close() })

	// load test image
	t.Logf("load test image: %s", TestFeedInImageNameFirst)
	_, err = cli.ImageLoad(*ctx, imageircFirst, false)
	assert.NoError(t, err)

	// load test image
	t.Logf("load test image: %s", TestFeedInImageNameSecond)
	_, err = cli.ImageLoad(*ctx, imageircSecond, false)
	assert.NoError(t, err)

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

	// start feed-in container - will fail, submit timeout
	t.Run("start feed-in container submit timeout", func(t *testing.T) {
		// prep test env
		containerManagerInitialised = true
		containersToStartRequests = make(chan FeedInContainer)
		containersToStartResponses = make(chan startContainerResponse)

		fic := FeedInContainer{
			Lat:        TestFeederLatitude,
			Lon:        TestFeederLongitude,
			Label:      TestFeederLabel,
			ApiKey:     TestFeederAPIKey,
			FeederCode: TestFeederCode,
			Addr:       TestFeederAddr,
		}

		t.Log("waiting for timeout (~5 secs)...")
		_, err = fic.Start()
		assert.Error(t, err)
		assert.Equal(t, "5s timeout waiting to submit container start request", err.Error())
		containerManagerInitialised = false
	})

	// start feed-in container - will fail, start timeout
	t.Run("start feed-in container start timeout", func(t *testing.T) {

		// prep test env
		containerManagerInitialised = true
		containersToStartRequests = make(chan FeedInContainer)
		containersToStartResponses = make(chan startContainerResponse)

		fic := FeedInContainer{
			Lat:        TestFeederLatitude,
			Lon:        TestFeederLongitude,
			Label:      TestFeederLabel,
			ApiKey:     TestFeederAPIKey,
			FeederCode: TestFeederCode,
			Addr:       TestFeederAddr,
		}

		wg := sync.WaitGroup{}

		wg.Add(1)
		go func(t *testing.T) {
			t.Log("waiting for timeout (~30 secs)...")
			_, err = fic.Start()
			assert.Error(t, err)
			assert.Equal(t, "30s timeout waiting for container start request to be fulfilled", err.Error())
			wg.Done()
		}(t)

		select {
		case <-containersToStartRequests:
			t.Log("received from containersToStartRequests")
		case <-time.After(time.Second * 6):
			assert.Fail(t, "timeout receiving from containersToStartRequests")
		}

		wg.Wait()

		containerManagerInitialised = false
	})

	// start feed-in container - will fail, error starting
	t.Run("start feed-in container err starting", func(t *testing.T) {
		// prep test env
		containerManagerInitialised = true
		containersToStartRequests = make(chan FeedInContainer)
		containersToStartResponses = make(chan startContainerResponse)

		fic := FeedInContainer{
			Lat:        TestFeederLatitude,
			Lon:        TestFeederLongitude,
			Label:      TestFeederLabel,
			ApiKey:     TestFeederAPIKey,
			FeederCode: TestFeederCode,
			Addr:       TestFeederAddr,
		}

		wg := sync.WaitGroup{}

		wg.Add(1)
		go func(t *testing.T) {
			_, err = fic.Start()
			assert.Error(t, err)
			assert.Equal(t, "error injected for testing", err.Error())
			wg.Done()
		}(t)

		select {
		case <-containersToStartRequests:
			t.Log("received from containersToStartRequests")
		case <-time.After(time.Second * 6):
			assert.Fail(t, "timeout receiving from containersToStartRequests")
		}

		select {
		case containersToStartResponses <- startContainerResponse{Err: errors.New("error injected for testing")}:
			t.Log("received from containersToStartResponses")
		case <-time.After(time.Second * 31):
			assert.Fail(t, "timeout receiving from containersToStartResponses")
		}

		wg.Wait()

		containerManagerInitialised = false
	})

	// init container manager
	cm := ContainerManager{
		FeedInImageName:                    TestFeedInImageNameFirst,
		FeedInContainerPrefix:              TestFeedInContainerPrefix,
		FeedInContainerNetwork:             TestFeedInContainerNetwork,
		SignalSkipContainerRecreationDelay: syscall.SIGUSR1,
		PWIngestSink:                       TestPWIngestSink,
		Logger:                             log.Logger,
	}
	t.Run("running ContainerManager.Init()", func(t *testing.T) {
		cm.Init()
	})

	var cid string

	// start feed-in container
	t.Run("start feed-in container working", func(t *testing.T) {
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

	// start feed-in container
	t.Run("start feed-in container already started", func(t *testing.T) {
		t.Log("requesting container start")
		fic := FeedInContainer{
			Lat:        TestFeederLatitude,
			Lon:        TestFeederLongitude,
			Label:      TestFeederLabel,
			ApiKey:     TestFeederAPIKey,
			FeederCode: TestFeederCode,
			Addr:       TestFeederAddr,
		}
		_, err = fic.Start()
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

	t.Run("check prom metrics gauge funcs", func(t *testing.T) {
		assert.Equal(t, float64(1), promMetricFeederContainersImageCurrentGaugeFunc(TestFeedInImageNameFirst, TestFeedInContainerPrefix))
		assert.Equal(t, float64(0), promMetricFeederContainersImageNotCurrentGaugeFunc(TestFeedInImageNameFirst, TestFeedInContainerPrefix))
	})

	t.Run("running ContainerManager.Close()", func(t *testing.T) {
		cm.Close()
	})

	// init container manager
	cm = ContainerManager{
		FeedInImageName:                    TestFeedInImageNameSecond,
		FeedInContainerPrefix:              TestFeedInContainerPrefix,
		FeedInContainerNetwork:             TestFeedInContainerNetwork,
		SignalSkipContainerRecreationDelay: syscall.SIGUSR1,
		PWIngestSink:                       TestPWIngestSink,
		Logger:                             log.Logger,
	}
	t.Run("running ContainerManager.Init() with new feed-in image", func(t *testing.T) {
		cm.Init()
	})
	t.Cleanup(func() { cm.Close() })

	t.Run("check prom metrics gauge funcs", func(t *testing.T) {
		assert.Equal(t, float64(0), promMetricFeederContainersImageCurrentGaugeFunc(TestFeedInImageNameSecond, TestFeedInContainerPrefix))
		assert.Equal(t, float64(1), promMetricFeederContainersImageNotCurrentGaugeFunc(TestFeedInImageNameSecond, TestFeedInContainerPrefix))
	})

	// send SIGUSR1 to prevent checkFeederContainers from sleeping
	t.Log("send SIGUSR1 to prevent checkFeederContainers from sleeping")
	chanSkipDelay <- syscall.SIGUSR1

	// ensure out-of-date container has been removed
	time.Sleep(time.Second * 3) // wait for container to be removed by checkFeederContainers
	t.Run("ensure out-of-date container has been removed", func(t *testing.T) {
		cl, err := cli.ContainerList(*ctx, types.ContainerListOptions{})
		assert.NoError(t, err, "expected no error from docker")
		for _, c := range cl {
			if c.ID == cid {
				assert.Fail(t, "expected feed-in container to be killed")
			}
		}
	})
}
