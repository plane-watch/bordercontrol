package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

const MaxUint = ^uint(0)
const MinUint = 0
const MaxInt = int(MaxUint >> 1)
const MinInt = -MaxInt - 1

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
}

func generateTLSCertAndKey(keyFile, certFile *os.File) error {

	// Thanks to: https://go.dev/src/crypto/tls/generate_cert.go

	// prep certificate info
	hosts := []string{"localhost"}
	ipAddrs := []net.IP{net.IPv4(127, 0, 0, 1)}
	notBefore := time.Now()
	notAfter := time.Now().Add(time.Minute * 15)
	isCA := true
	rsaBits := 2048

	// generate private key
	priv, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return err
	}

	keyUsage := x509.KeyUsageDigitalSignature
	keyUsage |= x509.KeyUsageKeyEncipherment

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
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, priv.PublicKey, priv)
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
	kpr, err := NewKeypairReloader(certFile.Name(), keyFile.Name())
	assert.NoError(t, err, "could not load TLS cert/key for test")
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()

	// get testing host/port
	n, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err, "could not generate new local listener")
	tlsListenAddr := n.Addr().String()
	err = n.Close()
	assert.NoError(t, err, "could not close temp local listener")

	// configure temp listener
	tlsListener, err := tls.Listen("tcp", tlsListenAddr, &tlsConfig)
	defer tlsListener.Close()

}
