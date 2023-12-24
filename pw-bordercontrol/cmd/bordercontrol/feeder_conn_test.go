package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"pw_bordercontrol/lib/atc"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

const MaxUint = ^uint(0)
const MinUint = 0
const MaxInt = int(MaxUint >> 1)
const MinInt = -MaxInt - 1

var testSNI = uuid.New()

func TestGetNum(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	iCT := incomingConnectionTracker{}

	// test basic functionality
	t.Run("test basic functionality", func(t *testing.T) {
		assert.Equal(t, uint(1), iCT.getNum())
		assert.Equal(t, uint(2), iCT.getNum())
	})

	// test bounds wrap
	t.Run("test bounds wrap", func(t *testing.T) {
		iCT.connectionNumber = MaxUint
		assert.Equal(t, uint(1), iCT.getNum())
		assert.Equal(t, uint(2), iCT.getNum())
	})

	// test duplicate avoidance
	t.Run("test duplicate avoidance", func(t *testing.T) {
		iCT = incomingConnectionTracker{}
		iCT.connections = append(iCT.connections, incomingConnection{
			connNum: 1,
		})
		iCT.connections = append(iCT.connections, incomingConnection{
			connNum: 3,
		})
		assert.Equal(t, uint(2), iCT.getNum())
		assert.Equal(t, uint(4), iCT.getNum())
	})
}

func TestEvict(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	iCT := incomingConnectionTracker{}

	// add a connection with connection time older than 10 seconds
	iCT.connections = append(iCT.connections, incomingConnection{
		connTime: time.Now().Add(-(time.Second * 11)),
		connNum:  1,
	})

	// add a connection with a connection time newer than 10 seconds
	iCT.connections = append(iCT.connections, incomingConnection{
		connTime: time.Now().Add(-(time.Second * 2)),
		connNum:  2,
	})

	// should be 2 connections in the list prior to running evict
	assert.Equal(t, 2, len(iCT.connections))

	// run evict
	iCT.evict()

	// should be 1 connection in the list after running evict
	assert.Equal(t, 1, len(iCT.connections))

	// connection 2 should be the only connection in the list
	assert.Equal(t, uint(2), iCT.connections[0].connNum)
}

func TestCheck(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// prepare test data
	srcIP := net.IPv4(172, 0, 0, 1)
	iCT := incomingConnectionTracker{}

	// add max number of connections
	t.Run("add max number of connections", func(t *testing.T) {
		for i := uint(0); i < maxIncomingConnectionRequestsPerSrcIP; i++ {
			err := iCT.check(srcIP, iCT.getNum())
			assert.NoError(t, err, "should pass")
		}
	})

	// add next connection (should fail)
	t.Run("add connection exceeding max", func(t *testing.T) {
		err := iCT.check(srcIP, iCT.getNum())
		assert.Error(t, err)
	})
}

func TestLookupContainerTCP(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// test lookup of loopback
	t.Run("test lookup of loopback", func(t *testing.T) {

		// prepare test data
		port := 12345
		n, err := lookupContainerTCP("localhost", port)

		// expect IPv4 or IPv6 loopback
		expected := []string{
			fmt.Sprintf("127.0.0.1:%d", port),
			fmt.Sprintf("[%s]:%d", net.IPv6loopback.String(), port),
		}

		assert.NoError(t, err)
		assert.Contains(t, expected, n.String())
	})

	// test lookup failure
	t.Run("test lookup failure", func(t *testing.T) {
		_, err := lookupContainerTCP("something.invalid", 12345)
		assert.Error(t, err)
	})

	// test ipv6 (currently unsupported)
	t.Run("test unsupported IPv6", func(t *testing.T) {
		_, err := lookupContainerTCP("::1", 12345)
		assert.Error(t, err)
	})
}

func TestDialContainerTCP(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// test working server
	t.Run("test working server", func(t *testing.T) {
		// prepare mocked server
		srv, err := nettest.NewLocalListener("tcp4")
		assert.NoError(t, err)
		t.Log("listening on:", srv.Addr())
		go func() {
			for {
				_, err := srv.Accept()
				if err != nil {
					assert.NoError(t, err)
				}
			}
		}()

		// get port
		port, err := strconv.Atoi(strings.Split(srv.Addr().String(), ":")[1])
		assert.NoError(t, err)

		// test connection
		t.Log("testing dialContainerTCP")
		_, err = dialContainerTCP("localhost", port)
		assert.NoError(t, err)
	})

	// test invalid FQDN
	t.Run("test invalid FQDN", func(t *testing.T) {
		_, err := dialContainerTCP("something.invalid", 12345)
		assert.Error(t, err)
	})

	// test ipv6 (currently unsupported)
	t.Run("test unsupported IPv6", func(t *testing.T) {
		_, err := dialContainerTCP("::1", 12345)
		assert.Error(t, err)
	})

	// test broken server
	t.Run("test broken server", func(t *testing.T) {
		// prepare mocked server
		srv, err := nettest.NewLocalListener("tcp4")
		assert.NoError(t, err)

		// get port
		port, err := strconv.Atoi(strings.Split(srv.Addr().String(), ":")[1])
		assert.NoError(t, err)

		// close mocked server
		srv.Close()

		// test connection
		t.Log("testing dialContainerTCP to broken server")
		_, err = dialContainerTCP("localhost", port)
		assert.Error(t, err)
	})
}

func generateTLSCertAndKey(keyFile, certFile *os.File) error {

	// Thanks to: https://go.dev/src/crypto/tls/generate_cert.go

	// prep certificate info
	hosts := []string{"localhost"}
	ipAddrs := []net.IP{net.IPv4(127, 0, 0, 1)}
	notBefore := time.Now()
	notAfter := time.Now().Add(time.Minute * 15)
	isCA := true

	// generate private key
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	keyUsage := x509.KeyUsageDigitalSignature

	// generate serial number
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return err
	}

	// prep cert template
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"plane.watch unit testing org"},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              keyUsage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// add hostname(s)
	for _, host := range hosts {
		template.DNSNames = append(template.DNSNames, host)
	}

	// add ip(s)
	for _, ip := range ipAddrs {
		template.IPAddresses = append(template.IPAddresses, ip)
	}

	// if self-signed, include CA
	if isCA {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign
	}

	// create certificate
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, priv.Public().(ed25519.PublicKey), priv)
	if err != nil {
		return err
	}

	// encode certificate
	err = pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err != nil {
		return err
	}

	// marhsal private key
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}

	// write private key
	err = pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	if err != nil {
		return err
	}

	return nil
}

func TestTLS(t *testing.T) {

	// init stats
	t.Log("init stats")
	stats.Feeders = make(map[uuid.UUID]FeederStats)

	t.Log("preparing test environment TLS cert/key")

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

	// // test reload via signal
	// t.Log("sending SIGHUP for cert/key reload (working)")
	// chanSIGHUP <- syscall.SIGHUP

	// // wait for the channel to be read
	// time.Sleep(time.Second)

	// clean up after testing
	certFile.Close()
	os.Remove(certFile.Name())
	keyFile.Close()
	os.Remove(keyFile.Name())

	// // test reload via signal
	// t.Log("sending SIGHUP for cert/key reload")
	// chanSIGHUP <- syscall.SIGHUP

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
	tlsConfig := tls.Config{
		RootCAs:            scp,
		ServerName:         testSNI.String(),
		InsecureSkipVerify: true,
	}

	d := net.Dialer{
		Timeout: 10 * time.Second,
	}

	sendData := make(chan bool)

	t.Log("starting test environment TLS server")
	var clientConn *tls.Conn
	go func() {
		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListenAddr, &tlsConfig)
		assert.NoError(t, e, "could not dial test server")
		defer clientConn.Close()

		// send some initial test data to allow handshake to take place
		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

		// wait to send more data until instructed
		_ = <-sendData

		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

	}()

	t.Log("starting test environment TLS client")
	c, err := tlsListener.Accept()
	assert.NoError(t, err, "could not accept test connection")

	// make buffer to hold data read from client
	buf := make([]byte, sendRecvBufferSize)

	// give the unauthenticated client 10 seconds to perform TLS handshake
	c.SetDeadline(time.Now().Add(time.Second * 10))

	// read data from client
	_, err = readFromClient(c, buf)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			assert.NoError(t, err)
		}
	}

	t.Run("test authenticateFeeder working", func(t *testing.T) {

		// prepare test data
		validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
			Altitude:   1,
			ApiKey:     testSNI,
			FeederCode: "ABCD-1234",
			Label:      "test_feeder",
			Latitude:   123.45678,
			Longitude:  98.76543,
			Mux:        "test_mux",
		})

		// test authenticateFeeder
		clientDetails, err := authenticateFeeder(c)
		assert.NoError(t, err)
		assert.Equal(t, testSNI, clientDetails.clientApiKey)
		assert.Equal(t, 123.45678, clientDetails.refLat)
		assert.Equal(t, 98.76543, clientDetails.refLon)
		assert.Equal(t, "test_mux", clientDetails.mux)
		assert.Equal(t, "test_feeder", clientDetails.label)
		assert.Equal(t, "ABCD-1234", clientDetails.feederCode)
	})

	// now send some data
	sendData <- true

	t.Run("test readFromClient working", func(t *testing.T) {
		buf := make([]byte, 12)
		_, err = readFromClient(c, buf)
		assert.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), buf)
	})

	t.Run("test checkConnTLSHandshakeComplete", func(t *testing.T) {
		assert.True(t, checkConnTLSHandshakeComplete(c))
	})

	t.Run("test getUUIDfromSNI", func(t *testing.T) {
		u, err := getUUIDfromSNI(c)
		assert.NoError(t, err)
		assert.Equal(t, testSNI, u)
	})

	c.Close()
	t.Run("test readFromClient closed", func(t *testing.T) {
		buf := make([]byte, 12)
		_, err = readFromClient(clientConn, buf)
		assert.Error(t, err)
	})
}

func TestTLS_NonTLSClient(t *testing.T) {

	t.Log("preparing test environment TLS cert/key")

	// prep cert file
	certFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_cert.pem")
	assert.NoError(t, err, "could not create temporary certificate file for test")
	defer func() {
		// clean up after testing
		certFile.Close()
		os.Remove(certFile.Name())
	}()

	// prep key file
	keyFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_key.pem")
	assert.NoError(t, err, "could not create temporary private key file for test")
	defer func() {
		// clean up after testing
		keyFile.Close()
		os.Remove(keyFile.Name())
	}()

	// generate cert/key for testing
	err = generateTLSCertAndKey(keyFile, certFile)
	assert.NoError(t, err, "could not generate cert/key for test")

	// prep tls config for mocked server
	kpr, err := NewKeypairReloader(certFile.Name(), keyFile.Name(), chanSIGHUP)
	assert.NoError(t, err, "could not load TLS cert/key for test")
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()

	// get testing host/port
	n, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err, "could not generate new local listener for test")
	tlsListenAddr := n.Addr().String()
	err = n.Close()
	assert.NoError(t, err, "could not close temp local listener for test")

	// configure temp listener
	tlsListener, err := tls.Listen("tcp4", tlsListenAddr, &tlsConfig)
	assert.NoError(t, err)
	defer tlsListener.Close()
	t.Log(fmt.Sprintf("Listening on: %s", tlsListenAddr))

	t.Log("starting test environment TLS server")
	var wg sync.WaitGroup
	var svrConn net.Conn
	wg.Add(1)
	go func() {
		var e error
		svrConn, e = tlsListener.Accept()
		assert.NoError(t, e, "could not accept test connection")
		wg.Done()
	}()

	c, err := net.Dial("tcp4", tlsListener.Addr().String())
	assert.NoError(t, err)

	wg.Wait()

	go func() {
		_, err := c.Write([]byte("Hello World!"))
		assert.NoError(t, err)
	}()

	t.Run("test readFromClient non TLS client", func(t *testing.T) {
		buf := make([]byte, 12)
		_, err = readFromClient(svrConn, buf)
		assert.Error(t, err)
		assert.Equal(t, "tls: first record does not look like a TLS handshake", err.Error())
	})

	svrConn.Close()
	c.Close()
}

func TestProxyClientToServer(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	var (
		ssServerConn      net.Conn
		ssClientConn      *net.TCPConn
		serverListener    net.Listener
		err               error
		wgServerSideConns sync.WaitGroup
		wgServerListener  sync.WaitGroup
		wgServerConn      sync.WaitGroup
	)

	t.Log("preparing test server-side connections")

	// spin up server-side server that will accept one connection
	wgServerListener.Add(1)
	wgServerConn.Add(1)
	wgServerSideConns.Add(1)
	go func() {
		var e error
		serverListener, e = nettest.NewLocalListener("tcp4")
		assert.NoError(t, e)
		wgServerListener.Done()
		ssServerConn, e = serverListener.Accept()
		assert.NoError(t, e)
		wgServerConn.Done()
		wgServerSideConns.Done()
	}()
	wgServerListener.Wait()

	// spin up server-side client connection
	wgServerSideConns.Add(1)
	go func() {
		serverPort, err := strconv.Atoi(strings.Split(serverListener.Addr().String(), ":")[1])
		assert.NoError(t, err)

		connectTo := net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: serverPort,
			Zone: "",
		}
		ssClientConn, err = net.DialTCP("tcp4", nil, &connectTo)
		assert.NoError(t, err)
		wgServerSideConns.Done()
	}()
	wgServerConn.Wait()
	wgServerSideConns.Wait()

	// spin up client-side server & client connections
	t.Log("preparing test client-side connections")
	csClientConn, csServerConn := net.Pipe()

	// method to signal goroutines to exit
	pStatus := proxyStatus{
		run: true,
	}

	// test proxyClientToServer
	lastAuthCheck := time.Now()
	conf := protocolProxyConfig{
		clientConn:    csClientConn,
		serverConn:    ssClientConn,
		connNum:       uint(1),
		clientApiKey:  testSNI,
		pStatus:       &pStatus,
		lastAuthCheck: &lastAuthCheck,
		log:           log.Logger,
	}
	go proxyClientToServer(conf)

	// send data to be proxied from client-side
	_, err = csServerConn.Write([]byte("Hello World!"))
	assert.NoError(t, err)

	// read proxied data from the server-side
	buf := make([]byte, 12)
	_, err = ssServerConn.Read(buf)
	assert.NoError(t, err)

	// data should match!
	assert.Equal(t, []byte("Hello World!"), buf)

	pStatus.mu.Lock()
	pStatus.run = false
	pStatus.mu.Unlock()

}

func TestProxyServerToClient(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	var (
		ssServerConn      net.Conn
		ssClientConn      *net.TCPConn
		serverListener    net.Listener
		err               error
		wgServerSideConns sync.WaitGroup
		wgServerListener  sync.WaitGroup
		wgServerConn      sync.WaitGroup
	)

	t.Log("preparing test server-side connections")

	// spin up server-side server that will accept one connection
	wgServerListener.Add(1)
	wgServerConn.Add(1)
	wgServerSideConns.Add(1)
	go func() {
		var e error
		serverListener, e = nettest.NewLocalListener("tcp4")
		assert.NoError(t, e)
		wgServerListener.Done()
		ssServerConn, e = serverListener.Accept()
		assert.NoError(t, e)
		wgServerConn.Done()
		wgServerSideConns.Done()
	}()
	wgServerListener.Wait()

	// spin up server-side client connection
	wgServerSideConns.Add(1)
	go func() {
		serverPort, err := strconv.Atoi(strings.Split(serverListener.Addr().String(), ":")[1])
		assert.NoError(t, err)

		connectTo := net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: serverPort,
			Zone: "",
		}
		ssClientConn, err = net.DialTCP("tcp4", nil, &connectTo)
		assert.NoError(t, err)
		wgServerSideConns.Done()
	}()
	wgServerConn.Wait()
	wgServerSideConns.Wait()

	// spin up client-side server & client connections
	t.Log("preparing test client-side connections")
	csClientConn, csServerConn := net.Pipe()

	// method to signal goroutines to exit
	pStatus := proxyStatus{
		run: true,
	}

	// test proxyServerToClient
	lastAuthCheck := time.Now()

	conf := protocolProxyConfig{
		clientConn:    csClientConn,
		serverConn:    ssClientConn,
		connNum:       uint(1),
		clientApiKey:  testSNI,
		pStatus:       &pStatus,
		lastAuthCheck: &lastAuthCheck,
		log:           log.Logger,
	}

	go proxyServerToClient(conf)

	// send data to be proxied from client-side
	_, err = ssServerConn.Write([]byte("Hello World!"))
	assert.NoError(t, err)

	// read proxied data from the server-side
	buf := make([]byte, 12)
	_, err = csServerConn.Read(buf)
	assert.NoError(t, err)

	// data should match!
	assert.Equal(t, []byte("Hello World!"), buf)

	pStatus.mu.Lock()
	pStatus.run = false
	pStatus.mu.Unlock()

}

func troubleshootRunNetstat(t *testing.T) {
	// troubleshoot
	cmd := exec.Command("netstat", "-nat")
	out, err := cmd.Output()
	assert.NoError(t, err)
	fmt.Println(string(out))
}

func TestAuthenticateFeeder_InvalidAPIKey(t *testing.T) {

	// init stats
	t.Log("init stats")
	stats.Feeders = make(map[uuid.UUID]FeederStats)

	t.Log("preparing test environment TLS cert/key")

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
	tlsConfig := tls.Config{
		RootCAs:            scp,
		ServerName:         testSNI.String(),
		InsecureSkipVerify: true,
	}

	d := net.Dialer{
		Timeout: 10 * time.Second,
	}

	sendData := make(chan bool)

	t.Log("starting test environment TLS server")
	var clientConn *tls.Conn
	go func() {
		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListenAddr, &tlsConfig)
		assert.NoError(t, e, "could not dial test server")
		defer clientConn.Close()

		// send some initial test data to allow handshake to take place
		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

		// wait to send more data until instructed
		_ = <-sendData

		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

	}()

	t.Log("starting test environment TLS client")
	c, err := tlsListener.Accept()
	assert.NoError(t, err, "could not accept test connection")

	// make buffer to hold data read from client
	buf := make([]byte, sendRecvBufferSize)

	// give the unauthenticated client 10 seconds to perform TLS handshake
	c.SetDeadline(time.Now().Add(time.Second * 10))

	// read data from client
	_, err = readFromClient(c, buf)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			assert.NoError(t, err)
		}
	}

	// test authenticateFeeder
	_, err = authenticateFeeder(c)
	assert.NoError(t, err)

	// now send some data
	sendData <- true

	c.Close()

}

func TestAuthenticateFeeder_HandshakeIncomplete(t *testing.T) {

	// init stats
	t.Log("init stats")
	stats.Feeders = make(map[uuid.UUID]FeederStats)

	t.Log("preparing test environment TLS cert/key")

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
	tlsConfig := tls.Config{
		RootCAs:            scp,
		ServerName:         testSNI.String(),
		InsecureSkipVerify: true,
	}

	d := net.Dialer{
		Timeout: 10 * time.Second,
	}

	sendData := make(chan bool)

	t.Log("starting test environment TLS server")
	var clientConn *tls.Conn
	go func() {
		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListenAddr, &tlsConfig)
		assert.NoError(t, e, "could not dial test server")
		defer clientConn.Close()

		// send some initial test data to allow handshake to take place
		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

	}()

	t.Log("starting test environment TLS client")
	c, err := tlsListener.Accept()
	assert.NoError(t, err, "could not accept test connection")

	// test authenticateFeeder
	_, err = authenticateFeeder(c)
	assert.NoError(t, err)

	// now send some data
	sendData <- true

	c.Close()

}
