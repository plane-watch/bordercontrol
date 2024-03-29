package containers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"pw_bordercontrol/lib/nats_io"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/testutil/daemon"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/nats-io/nats.go"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

var (
	TestDaemonDockerSocket = "/run/containerd/containerd.sock"

	TestFeedInImageNameFirst   = "wardsco/sleep:latest"
	TestFeedInImageNameSecond  = "test-feed-in"
	TestFeedInContainerPrefix  = "test-feed-in-"
	TestFeedInContainerNetwork = "bridge"

	// mock feeder details
	TestFeederAPIKey    = uuid.New()
	TestFeederLabel     = "Test Feeder 123"
	TestFeederLatitude  = 123.456789
	TestFeederLongitude = 98.765432
	TestFeederMux       = "test-mux"
	TestFeederCode      = "ABCD-1234"
	TestFeederAddr      = net.IPv4(127, 0, 0, 1)
	TestPWIngestSink    = "nats://ingest-sink:12345"

	ErrTesting = errors.New("error injected for testing")
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})
}

func RunTestNatsServer() (*natsserver.Server, error) {

	// get host & port for testing
	tmpListener, err := nettest.NewLocalListener("tcp4")
	if err != nil {
		return &natsserver.Server{}, err
	}
	natsHost := strings.Split(tmpListener.Addr().String(), ":")[0]
	natsPort, err := strconv.Atoi(strings.Split(tmpListener.Addr().String(), ":")[1])
	if err != nil {
		return &natsserver.Server{}, err
	}
	tmpListener.Close()

	// create nats server
	server, err := natsserver.NewServer(&natsserver.Options{
		ServerName: "bordercontrol_test_server",
		Host:       natsHost,
		Port:       natsPort,
	})
	if err != nil {
		return &natsserver.Server{}, err
	}

	// start nats server
	server.Start()
	if !server.ReadyForConnections(time.Second * 5) {
		return &natsserver.Server{}, errors.New("NATS server didn't start")
	}
	return server, nil
}

func TestGetDockerClient(t *testing.T) {
	getDockerClientMu.RLock()
	defer getDockerClientMu.RUnlock()
	cli, err := getDockerClient()
	require.NoError(t, err)
	err = cli.Close()
	require.NoError(t, err)
}

func TestContainers(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// starting test docker daemon
	t.Log("starting test docker daemon")
	tmpDir, err := os.MkdirTemp("", "bordercontrol-go-test-*") // get temp path for test docker daemon
	require.NoError(t, err)
	TestDaemon, err := daemon.NewDaemon( // create test docker daemon
		tmpDir,
		daemon.WithContainerdSocket(TestDaemonDockerSocket),
	)
	require.NoError(t, err)
	TestDaemon.Start(t) // start test docker daemon
	t.Cleanup(func() {  // defer cleanup of test docker daemon
		TestDaemon.Stop(t)
		TestDaemon.Kill()
		TestDaemon.Cleanup(t)
		os.RemoveAll(tmpDir)
	})

	// prep broken docker client
	t.Run("broken docker client", func(t *testing.T) {

		getDockerClientMu.Lock()
		getDockerClient = func() (cli *client.Client, err error) {
			cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
			return cli, ErrTesting
		}
		getDockerClientMu.Unlock()

		// test checkFeederContainers with broken docker client
		t.Run("checkFeederContainers", func(t *testing.T) {
			checkFeederContainersConf := checkFeederContainersConfig{}
			_, err := checkFeederContainers(checkFeederContainersConf)
			require.Error(t, err)
			require.Equal(t, ErrTesting.Error(), err.Error())
		})

		// test startFeederContainers with broken docker client
		t.Run("startFeederContainers", func(t *testing.T) {
			startFeederContainersConf := startFeederContainersConfig{}
			_, err := startFeederContainers(startFeederContainersConf, FeedInContainer{})
			require.Error(t, err)
			require.Equal(t, ErrTesting.Error(), err.Error())
		})

		// test KickFeeder with broken docker client
		t.Run("KickFeeder", func(t *testing.T) {
			err = KickFeeder(TestFeederAPIKey)
			require.Error(t, err)
			assert.Equal(t, ErrTesting.Error(), err.Error())
		})

	})

	t.Run("invalid docker client", func(t *testing.T) {

		// test context
		ctx = context.Background()

		// prep invalid testing docker client
		getDockerClientMu.Lock()
		getDockerClient = func() (cli *client.Client, err error) {
			cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
			return cli, nil
		}
		getDockerClientMu.Unlock()
		TestDaemon.Stop(t) // make client invalid

		// test checkFeederContainers with invalid client
		t.Run("checkFeederContainers", func(t *testing.T) {
			checkFeederContainersConf := checkFeederContainersConfig{}
			_, err := checkFeederContainers(checkFeederContainersConf)
			require.Error(t, err)
			require.Contains(t, err.Error(), "Cannot connect to the Docker daemon at")
		})

		// test startFeederContainers with invalid docker client
		t.Run("startFeederContainers", func(t *testing.T) {

			// prep channels
			containersToStartRequests = make(chan FeedInContainer)
			containersToStartResponses = make(chan startContainerResponse)

			// prep config for startFeederContainers
			startFeederContainersConf := startFeederContainersConfig{
				containersToStartRequests:  containersToStartRequests,
				containersToStartResponses: containersToStartResponses,
				logger:                     log.Logger,
			}

			// send request to start container
			fic := FeedInContainer{
				Lat:        TestFeederLatitude,
				Lon:        TestFeederLongitude,
				Label:      TestFeederLabel,
				ApiKey:     TestFeederAPIKey,
				FeederCode: TestFeederCode,
				Addr:       TestFeederAddr,
			}

			_, err := startFeederContainers(startFeederContainersConf, fic)

			require.Error(t, err)
			require.Contains(t, err.Error(), "Cannot connect to the Docker daemon at")

		})
	})

	t.Run("working docker client, no init", func(t *testing.T) {
		// prep test env docker client
		ctx = context.Background()
		TestDaemon.Start(t)
		getDockerClientMu.Lock()
		getDockerClient = func() (cli *client.Client, err error) {
			cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
			return cli, nil
		}
		getDockerClientMu.Unlock()

		t.Run("RebuildFeedInImage", func(t *testing.T) {
			_, err := RebuildFeedInImage("", "", "")
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})

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
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})

		// start feed-in container - will fail, submit timeout
		t.Run("start feed-in container submit timeout", func(t *testing.T) {
			// prep test env

			initialisedMu.Lock()
			initialised = true
			initialisedMu.Unlock()

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
			require.Error(t, err)
			require.Equal(t, ErrTimeoutContainerStartReq.Error(), err.Error())

			initialisedMu.Lock()
			initialised = false
			initialisedMu.Unlock()
		})

		// start feed-in container - will fail, start timeout
		t.Run("start feed-in container start timeout", func(t *testing.T) {

			// prep test env

			initialisedMu.Lock()
			initialised = true
			initialisedMu.Unlock()

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
				require.Error(t, err)
				require.Equal(t, ErrTimeoutContainerStartResp.Error(), err.Error())
				wg.Done()
			}(t)

			select {
			case <-containersToStartRequests:
				t.Log("received from containersToStartRequests")
			case <-time.After(time.Second * 6):
				require.Fail(t, "timeout receiving from containersToStartRequests")
			}

			wg.Wait()

			initialisedMu.Lock()
			initialised = false
			initialisedMu.Unlock()

		})

		// start feed-in container - will fail, error starting
		t.Run("start feed-in container err starting", func(t *testing.T) {
			// prep test env

			initialisedMu.Lock()
			initialised = true
			initialisedMu.Unlock()

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
				require.Error(t, err)
				require.Equal(t, ErrTesting.Error(), err.Error())
				wg.Done()
			}(t)

			select {
			case <-containersToStartRequests:
				t.Log("received from containersToStartRequests")
			case <-time.After(time.Second * 6):
				require.Fail(t, "timeout receiving from containersToStartRequests")
			}

			select {
			case containersToStartResponses <- startContainerResponse{Err: ErrTesting}:
				t.Log("received from containersToStartResponses")
			case <-time.After(time.Second * 31):
				require.Fail(t, "timeout receiving from containersToStartResponses")
			}

			wg.Wait()

			initialisedMu.Lock()
			initialised = false
			initialisedMu.Unlock()

		})

	})

	// container manager config
	cm := ContainerManager{
		FeedInImageName:                    TestFeedInImageNameFirst,
		FeedInContainerPrefix:              TestFeedInContainerPrefix,
		FeedInContainerNetwork:             TestFeedInContainerNetwork,
		SignalSkipContainerRecreationDelay: syscall.SIGUSR1,
		PWIngestSink:                       TestPWIngestSink,
		Logger:                             log.Logger,
	}

	t.Run("attempt Stop() before .Run()", func(t *testing.T) {
		err := cm.Stop()
		require.Error(t, err)
		assert.Equal(t, ErrNotInitialised.Error(), err.Error())
	})

	t.Run("ContainerManager.Run() with nats", func(t *testing.T) {
		natsServer, err := RunTestNatsServer()
		require.NoError(t, err)

		nc := nats_io.NatsConfig{
			Url:       natsServer.ClientURL(),
			Instance:  "testing",
			Version:   "testver",
			StartTime: time.Now(),
		}

		err = nc.Init()
		require.NoError(t, err)

		require.True(t, nats_io.IsConnected())

		err = cm.Run()
		require.NoError(t, err)

		err = cm.Stop()
		require.NoError(t, err)

		err = nc.Close()
		require.NoError(t, err)

		natsServer.Shutdown()
	})

	t.Run("ContainerManager.Run() no nats", func(t *testing.T) {
		err := cm.Run()
		require.NoError(t, err)
	})

	t.Run("wait 35 seconds for checkFeederContainers", func(t *testing.T) {
		time.Sleep(35 * time.Second)
	})

	t.Run("ContainerManager.Run() already running", func(t *testing.T) {
		err := cm.Run()
		require.Error(t, err)
		assert.Equal(t, ErrAlreadyInitialised.Error(), err.Error())
	})

	t.Run("working client after init", func(t *testing.T) {

		// prep test env docker client
		TestDaemon.Start(t)
		getDockerClientMu.Lock()
		getDockerClient = func() (cli *client.Client, err error) {
			cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
			return cli, nil
		}
		getDockerClientMu.Unlock()

		// get docker client
		t.Log("get docker client to inspect container")
		getDockerClientMu.RLock()
		cli, err := getDockerClient()
		getDockerClientMu.RUnlock()
		require.NoError(t, err)

		// pull test image
		t.Logf("pull test image: %s", TestFeedInImageNameFirst)
		imageircFirst, err := cli.ImagePull(ctx, TestFeedInImageNameFirst, types.ImagePullOptions{})
		require.NoError(t, err)
		t.Cleanup(func() { imageircFirst.Close() })

		// load test image
		t.Logf("load test image: %s", TestFeedInImageNameFirst)
		_, err = cli.ImageLoad(ctx, imageircFirst, false)
		require.NoError(t, err)

		t.Run("build feed-in image", func(t *testing.T) {
			pwd, err := os.Getwd()
			require.NoError(t, err)

			buildContext := filepath.Join(pwd, "../../../feed-in/")
			t.Logf("build context: %s", buildContext)

			lastLine, err := RebuildFeedInImage(TestFeedInImageNameSecond, buildContext, "Dockerfile.feeder")
			require.NoError(t, err)
			require.Contains(t, lastLine, fmt.Sprintf("Successfully tagged %s:latest", TestFeedInImageNameSecond))
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
			require.NoError(t, err)
		})

		var ct types.ContainerJSON

		// inspect container
		t.Run("inspect container", func(t *testing.T) {
			ct, err = cli.ContainerInspect(ctx, cid)
			require.NoError(t, err)
		})

		// check environment variables
		t.Run("check container environment variables", func(t *testing.T) {
			envVars := make(map[string]string)
			for _, e := range ct.Config.Env {
				envVars[strings.Split(e, "=")[0]] = strings.Split(e, "=")[1]
			}

			require.Equal(t, fmt.Sprintf("%f", TestFeederLatitude), envVars["FEEDER_LAT"])
			require.Equal(t, fmt.Sprintf("%f", TestFeederLongitude), envVars["FEEDER_LON"])
			require.Equal(t, strings.ToLower(fmt.Sprintf("%s", TestFeederAPIKey)), envVars["FEEDER_UUID"])
			require.Equal(t, TestFeederCode, envVars["FEEDER_TAG"])
			require.Equal(t, TestPWIngestSink, envVars["PW_INGEST_SINK"])
			require.Equal(t, "location-updates", envVars["PW_INGEST_PUBLISH"])
			require.Equal(t, "listen", envVars["PW_INGEST_INPUT_MODE"])
			require.Equal(t, "beast", envVars["PW_INGEST_INPUT_PROTO"])
			require.Equal(t, "0.0.0.0", envVars["PW_INGEST_INPUT_ADDR"])
			require.Equal(t, "12345", envVars["PW_INGEST_INPUT_PORT"])
		})

		// check container autoremove set to true
		t.Run("check container autoremove", func(t *testing.T) {
			require.True(t, ct.HostConfig.AutoRemove)
		})

		// check container network connection
		t.Run("check container network", func(t *testing.T) {
			var ContainerNetworkOK bool
			for network, _ := range ct.NetworkSettings.Networks {
				if network == TestFeedInContainerNetwork {
					ContainerNetworkOK = true
				}
			}
			require.Len(t, ct.NetworkSettings.Networks, 1)
			require.True(t, ContainerNetworkOK)
		})

		t.Run("check prom metrics gauge funcs", func(t *testing.T) {
			require.Equal(t, float64(1), promMetricFeederContainersImageCurrentGaugeFunc(TestFeedInImageNameFirst, TestFeedInContainerPrefix))
			require.Equal(t, float64(0), promMetricFeederContainersImageNotCurrentGaugeFunc(TestFeedInImageNameFirst, TestFeedInContainerPrefix))
		})

		t.Run("RebuildFeedInImageHandler", func(t *testing.T) {

			wg := sync.WaitGroup{}

			feedInImageName = TestFeedInImageNameSecond

			pwd, err := os.Getwd()
			require.NoError(t, err)

			feedInImageBuildContext = filepath.Join(pwd, "../../../feed-in/")
			feedInImageBuildContextDockerfile = "Dockerfile.feeder"

			// override functions for testing
			natsThisInstance = func(sentToInstance string) (meantForThisInstance bool, thisInstanceName string, err error) {
				return true, sentToInstance, nil
			}
			wg.Add(1)
			natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
				require.Equal(t, string(original.Data), reply.Header.Get("instance"))
				require.Contains(t, string(reply.Data), fmt.Sprintf("Successfully tagged %s:latest", feedInImageName))
				t.Log(string(reply.Data))
				wg.Done()
				return nil
			}

			//
			msg := nats.NewMsg("pw_bordercontrol.testing.RebuildFeedInImageHandler")
			msg.Data = []byte("testinstance")
			RebuildFeedInImageHandler(msg)

			wg.Wait()

		})

		t.Run("running ContainerManager.Close()", func(t *testing.T) {
			err := cm.Stop()
			require.NoError(t, err)
		})

		// init container manager with new feed in image
		cm = ContainerManager{
			FeedInImageName:                    TestFeedInImageNameSecond,
			FeedInContainerPrefix:              TestFeedInContainerPrefix,
			FeedInContainerNetwork:             TestFeedInContainerNetwork,
			SignalSkipContainerRecreationDelay: syscall.SIGUSR1,
			PWIngestSink:                       TestPWIngestSink,
			Logger:                             log.Logger,
		}
		t.Run("running ContainerManager.Init() with new feed-in image", func(t *testing.T) {
			err := cm.Run()
			require.NoError(t, err)
		})
		t.Cleanup(func() {
			cm.Stop()
		})

		t.Run("check prom metrics gauge funcs", func(t *testing.T) {
			require.Equal(t, float64(0), promMetricFeederContainersImageCurrentGaugeFunc(TestFeedInImageNameSecond, TestFeedInContainerPrefix))
			require.Equal(t, float64(1), promMetricFeederContainersImageNotCurrentGaugeFunc(TestFeedInImageNameSecond, TestFeedInContainerPrefix))
		})

		// send SIGUSR1 to prevent checkFeederContainers from sleeping
		t.Log("send SIGUSR1 to prevent checkFeederContainers from sleeping")
		chanSkipDelay <- syscall.SIGUSR1

		// ensure out-of-date container has been removed
		time.Sleep(time.Second * 3) // wait for container to be removed by checkFeederContainers
		t.Run("ensure out-of-date container has been removed", func(t *testing.T) {
			cl, err := cli.ContainerList(ctx, container.ListOptions{})
			require.NoError(t, err, "expected no error from docker")
			for _, c := range cl {
				if c.ID == cid {
					require.Fail(t, "expected feed-in container to be killed")
				}
			}
		})

		t.Run("KickFeeder zero containers", func(t *testing.T) {
			dockerContainerListOrig := dockerContainerList
			dockerContainerList = func(ctx context.Context, cli *client.Client, options container.ListOptions) ([]types.Container, error) {
				return []types.Container{}, nil
			}
			err = KickFeeder(TestFeederAPIKey)
			require.Error(t, err)
			assert.Equal(t, ErrContainerNotFound.Error(), err.Error())
			dockerContainerList = dockerContainerListOrig
		})

		t.Run("KickFeeder >1 container", func(t *testing.T) {
			dockerContainerListOrig := dockerContainerList
			dockerContainerList = func(ctx context.Context, cli *client.Client, options container.ListOptions) ([]types.Container, error) {
				return []types.Container{types.Container{}, types.Container{}}, nil
			}
			err = KickFeeder(TestFeederAPIKey)
			require.Error(t, err)
			assert.Equal(t, ErrMultipleContainersFound.Error(), err.Error())
			dockerContainerList = dockerContainerListOrig
		})

		t.Run("start container to remove", func(t *testing.T) {
			fic := FeedInContainer{
				Lat:        TestFeederLatitude,
				Lon:        TestFeederLongitude,
				Label:      TestFeederLabel,
				ApiKey:     TestFeederAPIKey,
				FeederCode: TestFeederCode,
				Addr:       TestFeederAddr,
			}
			cid, err = fic.Start()
			require.NoError(t, err)
		})

		t.Run("KickFeeder err removing", func(t *testing.T) {
			dockerContainerRemoveOrig := dockerContainerRemove
			dockerContainerRemove = func(ctx context.Context, cli *client.Client, containerID string, options container.RemoveOptions) error {
				return ErrTesting
			}
			err = KickFeeder(TestFeederAPIKey)
			require.Error(t, err)
			assert.Equal(t, ErrTesting.Error(), err.Error())
			dockerContainerRemove = dockerContainerRemoveOrig
		})

		t.Run("KickFeeder", func(t *testing.T) {
			err = KickFeeder(TestFeederAPIKey)
			require.NoError(t, err)
		})

		t.Run("KickFeederHandler", func(t *testing.T) {

			t.Run("error invalid api key", func(t *testing.T) {

				wg := sync.WaitGroup{}

				fic := FeedInContainer{
					Lat:        TestFeederLatitude,
					Lon:        TestFeederLongitude,
					Label:      TestFeederLabel,
					ApiKey:     TestFeederAPIKey,
					FeederCode: TestFeederCode,
					Addr:       TestFeederAddr,
				}
				cid, err = fic.Start()
				require.NoError(t, err)

				// override functions for testing
				natsThisInstance = func(sentToInstance string) (meantForThisInstance bool, thisInstanceName string, err error) {
					return true, sentToInstance, nil
				}
				wg.Add(1)
				natsTerm = func(msg *nats.Msg) error {
					wg.Done()
					return nil
				}

				msg := nats.NewMsg("pw_bordercontrol.testing.KickFeederHandler")
				msg.Data = []byte("invalid api key!")
				KickFeederHandler(msg)

				wg.Wait()

			})

			t.Run("error acking nats msg", func(t *testing.T) {

				natsAckOrig := natsAck
				natsAck = func(msg *nats.Msg) error {
					return ErrTesting
				}

				wg := sync.WaitGroup{}

				fic := FeedInContainer{
					Lat:        TestFeederLatitude,
					Lon:        TestFeederLongitude,
					Label:      TestFeederLabel,
					ApiKey:     TestFeederAPIKey,
					FeederCode: TestFeederCode,
					Addr:       TestFeederAddr,
				}
				cid, err = fic.Start()
				require.NoError(t, err)

				// override functions for testing
				natsThisInstance = func(sentToInstance string) (meantForThisInstance bool, thisInstanceName string, err error) {
					return true, sentToInstance, nil
				}
				wg.Add(1)
				natsAck = func(msg *nats.Msg) error {
					wg.Done()
					return nil
				}

				msg := nats.NewMsg("pw_bordercontrol.testing.KickFeederHandler")
				msg.Data = []byte(TestFeederAPIKey.String())
				KickFeederHandler(msg)

				wg.Wait()

				natsAck = natsAckOrig

			})

			t.Run("no error", func(t *testing.T) {

				wg := sync.WaitGroup{}

				fic := FeedInContainer{
					Lat:        TestFeederLatitude,
					Lon:        TestFeederLongitude,
					Label:      TestFeederLabel,
					ApiKey:     TestFeederAPIKey,
					FeederCode: TestFeederCode,
					Addr:       TestFeederAddr,
				}
				cid, err = fic.Start()
				require.NoError(t, err)

				// override functions for testing
				natsThisInstance = func(sentToInstance string) (meantForThisInstance bool, thisInstanceName string, err error) {
					return true, sentToInstance, nil
				}
				wg.Add(1)
				natsAck = func(msg *nats.Msg) error {
					wg.Done()
					return nil
				}

				msg := nats.NewMsg("pw_bordercontrol.testing.KickFeederHandler")
				msg.Data = []byte(TestFeederAPIKey.String())
				KickFeederHandler(msg)

				wg.Wait()

			})

		})

	})

}
