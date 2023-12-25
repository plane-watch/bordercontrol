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

func TestProxyClientToServer_Working(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// prepare test data
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		Altitude:   1,
		ApiKey:     testSNI,
		FeederCode: "ABCD-1234",
		Label:      "test_feeder",
		Latitude:   123.45678,
		Longitude:  98.76543,
		Mux:        "test_mux",
	})
	validFeeders.mu.Unlock()

	wg := sync.WaitGroup{}

	// init stats
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// test connections
	clientOuter, clientInner := net.Pipe()
	serverOuter, serverInner := net.Pipe()
	defer clientOuter.Close()
	defer clientInner.Close()
	defer serverOuter.Close()
	defer serverInner.Close()

	// method to signal goroutines to exit
	pStatus := proxyStatus{
		run: true,
	}

	// test proxyClientToServer
	lastAuthCheck := time.Now()
	conf := protocolProxyConfig{
		clientConn:                  clientInner,
		serverConn:                  serverInner,
		connNum:                     uint(1),
		clientApiKey:                testSNI,
		pStatus:                     &pStatus,
		lastAuthCheck:               &lastAuthCheck,
		log:                         log.Logger,
		feederValidityCheckInterval: time.Second * 1,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		proxyClientToServer(conf)
	}()

	// send data to be proxied from client-side
	_, err := clientOuter.Write([]byte("Hello World!"))
	assert.NoError(t, err)

	// read proxied data from the server-side
	buf := make([]byte, 12)
	_, err = serverOuter.Read(buf)
	assert.NoError(t, err)

	// data should match!
	assert.Equal(t, []byte("Hello World!"), buf)

	// wait for auth check
	time.Sleep(time.Second * 2)

	// stop proxyClientToServer
	pStatus.mu.Lock()
	pStatus.run = false
	pStatus.mu.Unlock()

	wg.Wait()

}

func TestProxyServerToClient_Working(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// prepare test data
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		Altitude:   1,
		ApiKey:     testSNI,
		FeederCode: "ABCD-1234",
		Label:      "test_feeder",
		Latitude:   123.45678,
		Longitude:  98.76543,
		Mux:        "test_mux",
	})
	validFeeders.mu.Unlock()

	wg := sync.WaitGroup{}

	// init stats
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// test connections
	clientOuter, clientInner := net.Pipe()
	serverOuter, serverInner := net.Pipe()
	defer clientOuter.Close()
	defer clientInner.Close()
	defer serverOuter.Close()
	defer serverInner.Close()

	// method to signal goroutines to exit
	pStatus := proxyStatus{
		run: true,
	}

	// test proxyClientToServer
	lastAuthCheck := time.Now()
	conf := protocolProxyConfig{
		clientConn:                  clientInner,
		serverConn:                  serverInner,
		connNum:                     uint(1),
		clientApiKey:                testSNI,
		pStatus:                     &pStatus,
		lastAuthCheck:               &lastAuthCheck,
		log:                         log.Logger,
		feederValidityCheckInterval: time.Second * 60,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		proxyServerToClient(conf)
	}()

	// send data to be proxied from client-side
	_, err := serverOuter.Write([]byte("Hello World!"))
	assert.NoError(t, err)

	// read proxied data from the server-side
	buf := make([]byte, 12)
	_, err = clientOuter.Read(buf)
	assert.NoError(t, err)

	// data should match!
	assert.Equal(t, []byte("Hello World!"), buf)

	// wait for auth check
	time.Sleep(time.Second * 2)

	// stop proxyClientToServer
	pStatus.mu.Lock()
	pStatus.run = false
	pStatus.mu.Unlock()

	wg.Wait()

}

func TestProxyClientToServer_FeederBanned(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// prepare test data
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		Altitude:   1,
		ApiKey:     testSNI,
		FeederCode: "ABCD-1234",
		Label:      "test_feeder",
		Latitude:   123.45678,
		Longitude:  98.76543,
		Mux:        "test_mux",
	})
	validFeeders.mu.Unlock()

	wg := sync.WaitGroup{}

	// init stats
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// test connections
	clientOuter, clientInner := net.Pipe()
	serverOuter, serverInner := net.Pipe()
	defer clientOuter.Close()
	defer clientInner.Close()
	defer serverOuter.Close()
	defer serverInner.Close()

	// method to signal goroutines to exit
	pStatus := proxyStatus{
		run: true,
	}

	// test proxyClientToServer
	lastAuthCheck := time.Now()
	conf := protocolProxyConfig{
		clientConn:                  clientInner,
		serverConn:                  serverInner,
		connNum:                     uint(1),
		clientApiKey:                testSNI,
		pStatus:                     &pStatus,
		lastAuthCheck:               &lastAuthCheck,
		log:                         log.Logger,
		feederValidityCheckInterval: time.Second * 1,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		proxyClientToServer(conf)
	}()

	// make feeder invalid
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.mu.Unlock()

	// wait for feeder to expire
	time.Sleep(time.Second * 10)

	// send data to be proxied from client-side
	err := clientOuter.SetDeadline(time.Now().Add(time.Second * 2))
	assert.NoError(t, err)
	_, err = clientOuter.Write([]byte("Hello World!"))
	assert.Error(t, err)
	assert.Equal(t, "write pipe: i/o timeout", err.Error())

	// read proxied data from the server-side
	buf := make([]byte, 12)
	err = serverOuter.SetDeadline(time.Now().Add(time.Second * 2))
	assert.NoError(t, err)
	_, err = serverOuter.Read(buf)
	assert.Error(t, err)
	assert.Equal(t, "read pipe: i/o timeout", err.Error())

	pStatus.mu.Lock()
	pStatus.run = false
	pStatus.mu.Unlock()

	wg.Wait()

}

func TestProxyServerToClient_FeederBanned(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// prepare test data
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		Altitude:   1,
		ApiKey:     testSNI,
		FeederCode: "ABCD-1234",
		Label:      "test_feeder",
		Latitude:   123.45678,
		Longitude:  98.76543,
		Mux:        "test_mux",
	})
	validFeeders.mu.Unlock()

	wg := sync.WaitGroup{}

	// init stats
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// test connections
	clientOuter, clientInner := net.Pipe()
	serverOuter, serverInner := net.Pipe()
	defer clientOuter.Close()
	defer clientInner.Close()
	defer serverOuter.Close()
	defer serverInner.Close()

	// method to signal goroutines to exit
	pStatus := proxyStatus{
		run: true,
	}

	// test proxyClientToServer
	lastAuthCheck := time.Now()
	conf := protocolProxyConfig{
		clientConn:                  clientInner,
		serverConn:                  serverInner,
		connNum:                     uint(1),
		clientApiKey:                testSNI,
		pStatus:                     &pStatus,
		lastAuthCheck:               &lastAuthCheck,
		log:                         log.Logger,
		feederValidityCheckInterval: time.Second * 1,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		proxyServerToClient(conf)
	}()

	// make feeder invalid
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.mu.Unlock()

	// wait for feeder to expire
	time.Sleep(time.Second * 10)

	// send data to be proxied from client-side
	err := serverOuter.SetDeadline(time.Now().Add(time.Second * 2))
	assert.NoError(t, err)
	_, err = serverOuter.Write([]byte("Hello World!"))
	assert.Error(t, err)
	assert.Equal(t, "write pipe: i/o timeout", err.Error())

	// read proxied data from the server-side
	buf := make([]byte, 12)
	err = clientOuter.SetDeadline(time.Now().Add(time.Second * 2))
	assert.NoError(t, err)
	_, err = clientOuter.Read(buf)
	assert.Error(t, err)
	assert.Equal(t, "read pipe: i/o timeout", err.Error())

	pStatus.mu.Lock()
	pStatus.run = false
	pStatus.mu.Unlock()

	wg.Wait()

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

func TestAuthenticateFeeder_Working(t *testing.T) {

	// prep waitgoup
	wg := sync.WaitGroup{}

	// init stats
	t.Log("init stats")
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// set up TLS environment, listener & client config
	prepTestEnvironmentTLS(t)
	tlsListener := prepTestEnvironmentTLSListener(t)
	defer tlsListener.Close()
	tlsClientConfig := prepTestEnvironmentTLSClientConfig(t)

	// prep channel for controlling flow of goroutines
	sendData := make(chan bool)

	// start test client
	t.Log("starting test environment TLS server")
	var clientConn *tls.Conn
	wg.Add(1)
	go func(t *testing.T) {
		defer wg.Done()

		// prep dialler
		d := net.Dialer{
			Timeout: 10 * time.Second,
		}

		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListener.Addr().String(), tlsClientConfig)
		assert.NoError(t, e, "could not dial test server")
		defer clientConn.Close()

		// send some initial test data to allow handshake to take place
		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

		// wait to send more data until instructed
		_ = <-sendData

		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

	}(t)

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
		validFeeders.mu.Lock()
		validFeeders.Feeders = []atc.Feeder{}
		validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
			Altitude:   1,
			ApiKey:     testSNI,
			FeederCode: "ABCD-1234",
			Label:      "test_feeder",
			Latitude:   123.45678,
			Longitude:  98.76543,
			Mux:        "test_mux",
		})
		validFeeders.mu.Unlock()

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

	wg.Wait()
}

func TestAuthenticateFeeder_NonTLSClient(t *testing.T) {

	// set up TLS environment, listener & client config
	prepTestEnvironmentTLS(t)
	tlsListener := prepTestEnvironmentTLSListener(t)
	defer tlsListener.Close()

	// channels to control test flow
	closeSvrConn := make(chan bool)
	readFromSvrConn := make(chan bool)

	t.Log("starting test environment TLS server")
	var wg sync.WaitGroup
	var svrConn net.Conn
	wg.Add(1)
	go func(t *testing.T) {
		defer wg.Done()
		var e error
		svrConn, e = tlsListener.Accept()
		assert.NoError(t, e, "could not accept test connection")
		defer svrConn.Close()
		readFromSvrConn <- true
		_ = <-closeSvrConn
	}(t)

	c, err := net.Dial("tcp", tlsListener.Addr().String())
	assert.NoError(t, err)
	defer c.Close()

	wg.Add(1)
	go func(t *testing.T) {
		defer wg.Done()
		_, err := c.Write([]byte("Hello World!"))
		assert.NoError(t, err)
		readFromSvrConn <- true
	}(t)

	t.Run("test readFromClient non TLS client", func(t *testing.T) {
		buf := make([]byte, 12)
		_ = <-readFromSvrConn
		_ = <-readFromSvrConn
		_, err = readFromClient(svrConn, buf)
		assert.Error(t, err)
		assert.Equal(t, "tls: first record does not look like a TLS handshake", err.Error())
	})

	closeSvrConn <- true
	wg.Wait()

}

func TestAuthenticateFeeder_WrongAPIKey(t *testing.T) {
	// Test where client sends a correctly-formatted UUID,
	// but that UUID is not in the database as an allowed feeder.

	// init stats
	t.Log("init stats")
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// set up TLS environment, listener & client config
	prepTestEnvironmentTLS(t)
	tlsListener := prepTestEnvironmentTLSListener(t)
	defer tlsListener.Close()
	tlsClientConfig := prepTestEnvironmentTLSClientConfig(t)

	// set SNI to a UUID not in the database
	tlsClientConfig.ServerName = uuid.NewString()

	sendData := make(chan bool)

	t.Log("starting test environment TLS server")
	var clientConn *tls.Conn
	go func(t *testing.T) {

		// prep dialler
		d := net.Dialer{
			Timeout: 10 * time.Second,
		}

		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListener.Addr().String(), tlsClientConfig)
		assert.NoError(t, e, "could not dial test server")
		defer clientConn.Close()

		// send some initial test data to allow handshake to take place
		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

		// wait to send more data until instructed
		_ = <-sendData

	}(t)

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
	assert.Error(t, err)
	assert.Equal(t, "client sent invalid api key", err.Error())

	// now send some data
	sendData <- true

	c.Close()

}

func TestAuthenticateFeeder_HandshakeIncomplete(t *testing.T) {

	// init stats
	t.Log("init stats")
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// set up TLS environment, listener & client config
	prepTestEnvironmentTLS(t)
	tlsListener := prepTestEnvironmentTLSListener(t)
	defer tlsListener.Close()
	tlsClientConfig := prepTestEnvironmentTLSClientConfig(t)

	sendData := make(chan bool)

	t.Log("starting test environment TLS server")
	go func(t *testing.T) {

		// prep dialler
		d := net.Dialer{
			Timeout: 10 * time.Second,
		}

		// dial remote
		clientConn, e := tls.DialWithDialer(&d, "tcp", tlsListener.Addr().String(), tlsClientConfig)
		assert.Error(t, e, "could not dial test server")
		if e == nil {
			defer clientConn.Close()
		}

		// wait to send more data until instructed
		_ = <-sendData

	}(t)

	t.Log("starting test environment TLS client")
	c, err := tlsListener.Accept()
	assert.NoError(t, err, "could not accept test connection")
	defer c.Close()

	// test authenticateFeeder
	_, err = authenticateFeeder(c)
	assert.Error(t, err)
	assert.Equal(t, "tls handshake incomplete", err.Error())

	// now send some data
	sendData <- true

}

func TestAuthenticateFeeder_InvalidApiKey(t *testing.T) {
	// Test where client sends a correctly-formatted UUID,
	// but that UUID is not in the database as an allowed feeder.

	// init stats
	t.Log("init stats")
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// set up TLS environment, listener & client config
	prepTestEnvironmentTLS(t)
	tlsListener := prepTestEnvironmentTLSListener(t)
	defer tlsListener.Close()
	tlsClientConfig := prepTestEnvironmentTLSClientConfig(t)

	// set SNI to a UUID not in the database
	tlsClientConfig.ServerName = "l33t h4x0r"

	sendData := make(chan bool)

	t.Log("starting test environment TLS server")
	var clientConn *tls.Conn
	go func(t *testing.T) {

		// prep dialler
		d := net.Dialer{
			Timeout: 10 * time.Second,
		}

		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListener.Addr().String(), tlsClientConfig)
		assert.NoError(t, e, "could not dial test server")
		defer clientConn.Close()

		// send some initial test data to allow handshake to take place
		_, e = clientConn.Write([]byte("Hello World!"))
		assert.NoError(t, e, "could not send test data")

		// wait to send more data until instructed
		_ = <-sendData

	}(t)

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
	assert.Error(t, err)
	assert.Equal(t, "invalid UUID length: 10", err.Error())

	// now send some data
	sendData <- true

	c.Close()

}

func TestProxyClientConnection_MLAT(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// define waitgroup
	wg := sync.WaitGroup{}

	// init stats
	t.Log("init stats")
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// define channels for controlling test goroutines
	closeConn := make(chan bool)
	serverQuit := make(chan bool)
	testsComplete := make(chan bool)

	// start test MLAT server - simple TCP echo server)
	t.Log("starting test MLAT server (TCP echo server) on 127.0.0.1:12346")
	wg.Add(1)
	go func(t *testing.T) {
		defer wg.Done()

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
		assert.NoError(t, err, "could not write to client connection")
		t.Logf("test MLAT server sent: '%s'", string(buf[:n]))

		// signify tests are now complete
		testsComplete <- true

		// keep server running until requested to quit
		_ = <-serverQuit

	}(t)

	// prepare test data
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		Altitude:   1,
		ApiKey:     testSNI,
		FeederCode: "ABCD-1234",
		Label:      "test_feeder",
		Latitude:   123.45678,
		Longitude:  98.76543,
		Mux:        "127.0.0.1", // connect to the tcp echo server
	})
	validFeeders.mu.Unlock()

	// set up TLS environment, listener & client config
	prepTestEnvironmentTLS(t)
	tlsListener := prepTestEnvironmentTLSListener(t)
	defer tlsListener.Close()
	tlsClientConfig := prepTestEnvironmentTLSClientConfig(t)

	// start test environment TLS client
	t.Log("starting test environment TLS client")

	var clientConn *tls.Conn

	wg.Add(1)
	go func(t *testing.T) {
		defer wg.Done()

		// prep dialler
		d := net.Dialer{
			Timeout: 10 * time.Second,
		}

		// define test data
		bytesToSend := []byte("test data from client to server")

		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListener.Addr().String(), tlsClientConfig)
		assert.NoError(t, e, "could not dial test server")
		defer clientConn.Close()

		// send data
		nW, e := clientConn.Write(bytesToSend)
		assert.NoError(t, e, "could not send test data from client to server")
		assert.Equal(t, len(bytesToSend), nW)

		// receive data
		bytesReceived := make([]byte, len(bytesToSend))
		nR, e := clientConn.Read(bytesReceived)
		assert.NoError(t, e, "could not receive test data from server to client")
		assert.Equal(t, len(bytesToSend), nR)
		assert.Equal(t, bytesToSend, bytesReceived)

		// wait to close the connection
		_ = <-closeConn

	}(t)

	// accept the TLS connection from the above goroutine
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

	// hand off the incoming test connection to the proxy

	wg.Add(1)
	go func(t *testing.T) {
		defer wg.Done()
		var err error
		err = proxyClientConnection(pc)
		assert.NoError(t, err)
	}(t)

	// wait for tests to complete
	_ = <-testsComplete

	t.Log("closing client connection")
	closeConn <- true

	t.Log("terminating test MLAT server")
	serverQuit <- true

	// wait for goroutines to finish
	t.Log("waiting for goroutines to finish")
	wg.Wait()

	t.Log("test complete")
}

func TestProxyClientConnection_MLAT_TooManyConns(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// define waitgroup
	wg := sync.WaitGroup{}

	// init stats
	t.Log("init stats")
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	// define channels for controlling test goroutines
	serverQuit := make(chan bool)

	// start test MLAT server - simple TCP echo server)
	t.Log("starting test MLAT server (TCP echo server) on 127.0.0.1:12346")
	wg.Add(1)
	go func(t *testing.T) {
		defer wg.Done()
		listenAddr := net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 12346,
		}
		listener, err := net.ListenTCP("tcp", &listenAddr)
		assert.NoError(t, err, "could not start test MLAT server")
		defer listener.Close()

		for {

			var serverConn net.Conn

			listener.SetDeadline(time.Now().Add(time.Second))

			select {
			case _ = <-serverQuit:
				return
			default:
				serverConn, err = listener.Accept()
				if err == nil {
					time.Sleep(time.Second)
					serverConn.Close()
				}
			}
		}
	}(t)

	// prepare test data
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		Altitude:   1,
		ApiKey:     testSNI,
		FeederCode: "ABCD-1234",
		Label:      "test_feeder",
		Latitude:   123.45678,
		Longitude:  98.76543,
		Mux:        "127.0.0.1", // connect to the tcp echo server
	})
	validFeeders.mu.Unlock()

	// set up TLS environment, listener & client config
	prepTestEnvironmentTLS(t)
	tlsListener := prepTestEnvironmentTLSListener(t)
	defer tlsListener.Close()
	tlsClientConfig := prepTestEnvironmentTLSClientConfig(t)

	// start test environment TLS client
	t.Log("starting test environment TLS client")

	var clientConn *tls.Conn

	go func() error {
		defer wg.Done()

		// prep dialler
		d := net.Dialer{
			Timeout: 10 * time.Second,
		}

		// dial remote
		var e error
		clientConn, e = tls.DialWithDialer(&d, "tcp", tlsListener.Addr().String(), tlsClientConfig)
		if e != nil {
			return e
		}
		defer clientConn.Close()

		// sleep for a bit
		time.Sleep(time.Second * 2)

		return nil
	}()

	// accept the TLS connection from the above goroutine
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

	// hand off the incoming test connection to the proxy

	// start max num of allowed conns
	for i := 0; i <= 2; i++ {
		wg.Add(1)
		go func(t *testing.T) {
			defer wg.Done()
			_ = proxyClientConnection(pc)
		}(t)
	}

	// start one more conn - should be disallowed
	wg.Add(1)
	go func(t *testing.T) {
		defer wg.Done()
		var err error
		err = proxyClientConnection(pc)
		assert.Error(t, err)
		assert.Equal(t, "more than 3 connections from src within a 10 second period", err.Error())
	}(t)

	t.Log("terminating test MLAT server")
	serverQuit <- true

	// wait for goroutines to finish
	t.Log("waiting for goroutines to finish")
	wg.Wait()

	t.Log("test complete")
}
