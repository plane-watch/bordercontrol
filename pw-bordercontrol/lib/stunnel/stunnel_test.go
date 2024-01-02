package stunnel

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

var (
	TestSNI = uuid.New()
)

func TestStunnel(t *testing.T) {

	// ensure functions that need initialisation throw an error if not initialised
	t.Run("test not initialised", func(t *testing.T) {
		t.Run("LoadCertAndKeyFromFile", func(t *testing.T) {
			err := LoadCertAndKeyFromFile("", "")
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("NewListener", func(t *testing.T) {
			_, err := NewListener("", "")
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("NewKeypairReloader", func(t *testing.T) {
			_, err := NewKeypairReloader("", "")
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("GenerateSelfSignedTLSCertAndKey", func(t *testing.T) {

			// get test name & remove path separator chars
			tName := strings.ReplaceAll(t.Name(), "/", "_")

			// make temp file for cert
			tmpCertFileName := fmt.Sprintf("pw-bordercontrol-testing-%s-certfile-*", tName)
			tmpCertFile, err := os.CreateTemp("", tmpCertFileName)
			assert.NoError(t, err)
			t.Cleanup(func() {
				tmpCertFile.Close()
				os.Remove(tmpCertFile.Name())
			})

			// make temp file for key
			tmpKeyFileName := fmt.Sprintf("pw-bordercontrol-testing-%s-keyfile-*", tName)
			tmpKeyFile, err := os.CreateTemp("", tmpKeyFileName)
			assert.NoError(t, err)
			t.Cleanup(func() {
				tmpKeyFile.Close()
				os.Remove(tmpKeyFile.Name())
			})

			// finally, test
			err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("PrepTestEnvironmentTLSCertAndKey", func(t *testing.T) {
			err := PrepTestEnvironmentTLSCertAndKey()
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("PrepTestEnvironmentTLSListener", func(t *testing.T) {
			_, err := PrepTestEnvironmentTLSListener()
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("PrepTestEnvironmentTLSClientConfig", func(t *testing.T) {
			_, err := PrepTestEnvironmentTLSClientConfig("")
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
	})

	// initialise stunnel subsystem, with SIGHUP as signal to reload
	t.Run("initialise stunnel subsystem", func(t *testing.T) {
		ptf := assert.PanicTestFunc(func() { Init(syscall.SIGHUP) })
		assert.NotPanics(t, ptf)
	})

	// ensure now initialised
	t.Run("isInitialised", func(t *testing.T) {
		initialisedMu.RLock()
		t.Cleanup(func() { initialisedMu.RUnlock() })
		assert.True(t, initialised)
	})

	// test cert reload
	t.Run("test reload via signal", func(t *testing.T) {

		// get test name & remove path separator chars
		tName := strings.ReplaceAll(t.Name(), "/", "_")

		// make temp file for cert
		tmpCertFileName := fmt.Sprintf("pw-bordercontrol-testing-%s-certfile-*", tName)
		tmpCertFile, err := os.CreateTemp("", tmpCertFileName)
		assert.NoError(t, err)
		t.Cleanup(func() {
			tmpCertFile.Close()
			os.Remove(tmpCertFile.Name())
		})

		// make temp file for key
		tmpKeyFileName := fmt.Sprintf("pw-bordercontrol-testing-%s-keyfile-*", tName)
		tmpKeyFile, err := os.CreateTemp("", tmpKeyFileName)
		assert.NoError(t, err)
		t.Cleanup(func() {
			tmpKeyFile.Close()
			os.Remove(tmpKeyFile.Name())
		})

		// generate test environment TLS Cert/Key
		err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
		assert.NoError(t, err)

		// initialise keypair reloader
		kpr, err := NewKeypairReloader(tmpCertFile.Name(), tmpKeyFile.Name())
		assert.NoError(t, err)

		// get copy of original cert
		kpr.certMu.RLock()
		c1 := *kpr.cert
		kpr.certMu.RUnlock()

		// generate new test environment TLS Cert/Key
		err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
		assert.NoError(t, err)

		// send signal
		signalChan <- syscall.SIGHUP

		// wait for the channel to be read
		time.Sleep(time.Second)

		// get copy of original cert
		kpr.certMu.RLock()
		c2 := *kpr.cert
		kpr.certMu.RUnlock()

		// ensure certs different
		assert.NotEqual(t, c1, c2)
	})

}

// func TestTLS_CertReload(t *testing.T) {

// 	Init(syscall.SIGHUP)

// 	t.Log("preparing test environment TLS cert/key")

// 	err := PrepTestEnvironmentTLSCertAndKey()
// 	assert.NoError(t, err)

// 	// test reload via signal
// 	t.Log("sending SIGHUP for cert/key reload (working)")
// 	signalChan <- syscall.SIGHUP

// 	// wait for the channel to be read
// 	time.Sleep(time.Second)

// 	// test reload via signal
// 	t.Log("sending SIGHUP for cert/key reload (will not work, files missing)")
// 	signalChan <- syscall.SIGHUP
// }
