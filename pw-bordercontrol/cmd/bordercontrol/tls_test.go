package main

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func prepTestEnvironmentTLS(t *testing.T) {
	t.Run("preparing test environment TLS cert/key", func(t *testing.T) {

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
