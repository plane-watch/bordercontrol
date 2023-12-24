package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"pw_bordercontrol/lib/atc"
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
	"golang.org/x/net/nettest"
)

const (
	TestDaemonDockerSocket = "/run/containerd/containerd.sock"

	TestFeedInImageName       = "wardsco/sleep:latest"
	TestfeedInContainerPrefix = "test-feed-in-"

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

	// start process to test
	t.Log("starting startFeederContainers")
	confA := startFeederContainersConfig{
		feedInImageName:            TestFeedInImageName,
		feedInContainerPrefix:      TestfeedInContainerPrefix,
		pwIngestPublish:            TestPWIngestSink,
		containersToStartRequests:  containersToStartRequests,
		containersToStartResponses: containersToStartResponses,
	}
	go startFeederContainers(confA)

	// start container
	t.Log("requesting container start")
	containersToStartRequests <- startContainerRequest{
		clientDetails: feederClient{
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
	assert.Equal(t, strings.ToLower(fmt.Sprintf("%s", TestFeederAPIKey)), envVars["FEEDER_UUID"])
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
	feedInContainerPrefix = TestfeedInContainerPrefix
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
	feedInContainerPrefix = TestfeedInContainerPrefix
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
	conf := &checkFeederContainersConfig{
		feedInImageName:          "foo",
		feedInContainerPrefix:    TestfeedInContainerPrefix,
		checkFeederContainerSigs: testChan,
	}
	err = checkFeederContainers(*conf)
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

	// start process to test
	t.Log("starting startFeederContainers")
	confA := &startFeederContainersConfig{
		feedInImageName:            TestFeedInImageName,
		feedInContainerPrefix:      TestfeedInContainerPrefix,
		pwIngestPublish:            TestPWIngestSink,
		containersToStartRequests:  containersToStartRequests,
		containersToStartResponses: containersToStartResponses,
	}
	go startFeederContainers(*confA)

	// start container
	t.Log("requesting container start")
	containersToStartRequests <- startContainerRequest{
		clientDetails: feederClient{
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
	confB := &checkFeederContainersConfig{
		feedInImageName:          TestFeedInImageName,
		feedInContainerPrefix:    TestfeedInContainerPrefix,
		checkFeederContainerSigs: testChan,
	}
	err = checkFeederContainers(*confB)
	assert.NoError(t, err)

	// ensure container has been killed
	t.Log("ensure container has not been killed")
	_, err = cli.ContainerInspect(*ctx, startedContainer.containerID)
	assert.NoError(t, err)

	// clean up
	t.Log("cleaning up")
	TestDaemon.Stop(t)
	TestDaemon.Cleanup(t)

}

func TestProxyClientConnection_MLAT(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// init stats
	t.Log("init stats")
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// start server listener
	serverQuit := make(chan bool)
	go func(t *testing.T) {

		buf := make([]byte, 1000)

		listenAddr := net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 12346,
		}
		listener, err := net.ListenTCP("tcp", &listenAddr)
		assert.NoError(t, err, "could not start test MLAT server")
		defer listener.Close()

		serverConn, err := listener.Accept()
		assert.NoError(t, err, "could not accept MLAT server connection")
		defer serverConn.Close()

		n, err := serverConn.Read(buf)
		assert.NoError(t, err, "could not read from client connection")

		t.Logf("test MLAT server received: '%s'", string(buf[:n]))

		n, err = serverConn.Write(buf[:n])
		t.Logf("test MLAT server sent: '%s'", string(buf[:n]))

		_ = <-serverQuit

	}(t)

	t.Log("preparing test environment TLS cert/key")

	// prepare test data
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		Altitude:   1,
		ApiKey:     testSNI,
		FeederCode: "ABCD-1234",
		Label:      "test_feeder",
		Latitude:   123.45678,
		Longitude:  98.76543,
		Mux:        "127.0.0.1", // connect to the tcp echo server
	})

	// prep signal channels
	prepSignalChannels()

	// prep cert file
	certFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_cert.pem")
	assert.NoError(t, err, "could not create temporary certificate file for test")

	// prep key file
	keyFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_key.pem")
	assert.NoError(t, err, "could not create temporary private key file for test")

	// generate cert/key for testing
	err = generateTLSCertAndKey(keyFile, certFile)
	assert.NoError(t, err, "could not generate cert/key for test")

	// prep tls config for mocked server
	kpr, err := NewKeypairReloader(certFile.Name(), keyFile.Name(), chanSIGHUP)
	assert.NoError(t, err, "could not load TLS cert/key for test")
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()

	// clean up after testing
	certFile.Close()
	os.Remove(certFile.Name())
	keyFile.Close()
	os.Remove(keyFile.Name())

	// get testing host/port
	n, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err, "could not generate new local listener for test")
	tlsListenAddr := n.Addr().String()
	err = n.Close()
	assert.NoError(t, err, "could not close temp local listener for test")

	// configure temp listener
	tlsListener, err := tls.Listen("tcp", tlsListenAddr, &tlsConfig)
	assert.NoError(t, err)
	defer tlsListener.Close()
	t.Log(fmt.Sprintf("Listening on: %s", tlsListenAddr))

	// load root CAs
	scp, err := x509.SystemCertPool()
	assert.NoError(t, err, "could not use system cert pool for test")

	// set up tls config
	tlsClientConfig := tls.Config{
		RootCAs:            scp,
		ServerName:         testSNI.String(),
		InsecureSkipVerify: true,
	}

	d := net.Dialer{
		Timeout: 10 * time.Second,
	}

	// define waitgroup
	wg := sync.WaitGroup{}

	// define channels for test flow
	closeConn := make(chan bool)

	// define test data
	bytesToSend := []byte("test data from client to server")

	t.Log("starting test environment TLS server")
	var clientConn *tls.Conn
	go func(t *testing.T) {
		wg.Add(1)

		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListenAddr, &tlsClientConfig)
		assert.NoError(t, e, "could not dial test server")
		defer clientConn.Close()

		// send data #1
		nW, e := clientConn.Write(bytesToSend)
		assert.NoError(t, e, "could not send test data from client to server")
		assert.Equal(t, len(bytesToSend), nW)

		bytesReceived := make([]byte, len(bytesToSend))

		// receive data
		nR, e := clientConn.Read(bytesReceived)
		assert.NoError(t, e, "could not receive test data from server to client")
		assert.Equal(t, len(bytesToSend), nR)
		assert.Equal(t, bytesToSend, bytesReceived)

		// wait to close the connection
		_ = <-closeConn

		wg.Done()

	}(t)

	// accept the connection from the above goroutine
	connIn, err := tlsListener.Accept()
	assert.NoError(t, err)

	// prepare proxy config
	pc := proxyConfig{
		connIn:                     connIn,
		connProto:                  protoMLAT, // must be MLAT for two way communications
		connNum:                    1,
		containersToStartRequests:  make(chan startContainerRequest),
		containersToStartResponses: make(chan startContainerResponse),
	}

	go func(t *testing.T) {
		wg.Add(1)
		var err error
		err = proxyClientConnection(pc)
		assert.Error(t, err)
		if err != nil {
			assert.Equal(t, "EOF", err.Error())
		}
		wg.Done()
	}(t)

	t.Log("closing the connection from client side")
	closeConn <- true

	wg.Wait()

}
