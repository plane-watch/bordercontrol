package main

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

func prepTestEnvironmentTLS(t *testing.T) {
	t.Run("preparing test environment TLS certificate and private key", func(t *testing.T) {

		// prep signal channels
		prepSignalChannels()

		// prep cert file
		certFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_cert.pem")
		assert.NoError(t, err, "could not create temporary certificate file for test")
		defer func(t *testing.T) {
			t.Log("closing certFile")
			certFile.Close()
			t.Log("deleting certFile")
			os.Remove(certFile.Name())
		}(t)

		// prep key file
		keyFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_key.pem")
		assert.NoError(t, err, "could not create temporary private key file for test")
		defer func(t *testing.T) {
			t.Log("closing keyFile")
			keyFile.Close()
			t.Log("deleting certFile")
			os.Remove(keyFile.Name())
		}(t)

		// generate cert/key for testing
		t.Log("generating TLS cert & key")
		err = generateTLSCertAndKey(keyFile, certFile)
		assert.NoError(t, err, "could not generate cert/key for test")

		// prep tls config for mocked server
		kpr, err := NewKeypairReloader(certFile.Name(), keyFile.Name(), chanSIGHUP)
		assert.NoError(t, err, "could not load TLS cert/key for test")
		tlsConfig.GetCertificate = kpr.GetCertificateFunc()
	})
}

func prepTestEnvironmentTLSListener(t *testing.T) net.Listener {

	var tlsListener net.Listener

	t.Run("preparing test environment TLS Listener", func(t *testing.T) {

		// get testing host/port
		n, err := nettest.NewLocalListener("tcp")
		assert.NoError(t, err, "could not generate new local listener for test")
		tlsListenAddr := n.Addr().String()
		err = n.Close()
		assert.NoError(t, err, "could not close temp local listener for test")

		// configure temp listener
		tlsListener, err = tls.Listen("tcp", tlsListenAddr, &tlsConfig)
		assert.NoError(t, err)
		t.Logf("Listening on: %s", tlsListenAddr)
	})

	return tlsListener
}

func prepTestEnvironmentTLSClientConfig(t *testing.T) *tls.Config {

	var tlsClientConfig tls.Config

	t.Run("preparing test environment TLS Client Config", func(t *testing.T) {

		// load root CAs
		scp, err := x509.SystemCertPool()
		assert.NoError(t, err, "could not use system cert pool for test")

		// set up tls config
		tlsClientConfig = tls.Config{
			RootCAs:            scp,
			ServerName:         testSNI.String(),
			InsecureSkipVerify: true,
		}
	})

	return &tlsClientConfig
}

func TestTLS_CertReload(t *testing.T) {

	// init stats
	t.Log("init stats")
	stats.mu.Lock()
	stats.Feeders = make(map[uuid.UUID]FeederStats)
	stats.mu.Unlock()

	t.Log("preparing test environment TLS cert/key")

	prepTestEnvironmentTLS(t)

	// test reload via signal
	t.Log("sending SIGHUP for cert/key reload (working)")
	chanSIGHUP <- syscall.SIGHUP

	// wait for the channel to be read
	time.Sleep(time.Second)

	// test reload via signal
	t.Log("sending SIGHUP for cert/key reload (will not work, files missing)")
	chanSIGHUP <- syscall.SIGHUP
}
