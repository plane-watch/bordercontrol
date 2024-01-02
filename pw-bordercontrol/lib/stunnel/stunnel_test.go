package stunnel

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
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
		t.Logf("created temp certificate file: %s", tmpCertFile.Name())

		// make temp file for key
		tmpKeyFileName := fmt.Sprintf("pw-bordercontrol-testing-%s-keyfile-*", tName)
		tmpKeyFile, err := os.CreateTemp("", tmpKeyFileName)
		assert.NoError(t, err)
		t.Cleanup(func() {
			tmpKeyFile.Close()
			os.Remove(tmpKeyFile.Name())
		})
		t.Logf("created temp key file: %s", tmpKeyFile.Name())

		// generate test environment TLS Cert/Key
		t.Log("generating self signed TLS cert & key")
		err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
		assert.NoError(t, err)

		// initialise keypair reloader
		t.Log("load TLS cert & key")
		kpr, err := NewKeypairReloader(tmpCertFile.Name(), tmpKeyFile.Name())
		assert.NoError(t, err)

		// get copy of original cert
		kpr.certMu.RLock()
		c1 := *kpr.cert
		kpr.certMu.RUnlock()

		// generate new test environment TLS Cert/Key
		t.Log("generating new self signed TLS cert & key")
		err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
		assert.NoError(t, err)

		// send signal
		t.Log("trigger cert/key reload")
		signalChan <- syscall.SIGHUP

		// wait for the channel to be read
		time.Sleep(time.Second)

		// get copy of new cert
		kpr.certMu.RLock()
		c2 := *kpr.cert
		kpr.certMu.RUnlock()

		// remove cert & key files
		t.Log("delete cert/key files")
		tmpCertFile.Close()
		os.Remove(tmpCertFile.Name())
		tmpKeyFile.Close()
		os.Remove(tmpKeyFile.Name())

		// send signal
		t.Log("trigger cert/key reload")
		signalChan <- syscall.SIGHUP

		// wait for the channel to be read
		time.Sleep(time.Second)

		// get copy of new new cert
		kpr.certMu.RLock()
		c3 := *kpr.cert
		kpr.certMu.RUnlock()

		t.Log("check in-memory certificates are as-expected")
		// ensure c1 & c2 different
		assert.NotEqual(t, c1, c2)
		// ensure c2 & c3 are the same (as no files to reload, they were deleted)
		assert.Equal(t, c2, c3)
	})

	t.Run("prepare for testing connections", func(t *testing.T) {
		err := PrepTestEnvironmentTLSCertAndKey()
		assert.NoError(t, err)
	})

	var listener net.Listener
	var clientTlsConfig *tls.Config

	t.Run("prepare test environment listener", func(t *testing.T) {
		var err error
		listener, err = PrepTestEnvironmentTLSListener()
		assert.NoError(t, err)
	})

	t.Run("prepare test environment client tls config", func(t *testing.T) {
		var err error
		clientTlsConfig, err = PrepTestEnvironmentTLSClientConfig(TestSNI.String())
		assert.NoError(t, err)
	})

	t.Run("test client connection", func(t *testing.T) {

		testData := "Hello World!"
		stopListener := make(chan bool, 1)
		wg := sync.WaitGroup{}

		// server side
		wg.Add(1)
		go func(t *testing.T) {

			buf := make([]byte, len(testData))

			// accept one connection
			conn, err := listener.Accept()
			assert.NoError(t, err)

			// read some data
			n, err := conn.Read(buf)
			assert.NoError(t, err)

			// check TLS handshake completed
			assert.True(t, HandshakeComplete(conn), "TLS handshake")

			// check SNI
			sni := GetSNI(conn)
			assert.Equal(t, TestSNI.String(), sni)

			// check received data
			assert.Equal(t, len(testData), n)
			assert.Equal(t, testData, string(buf))

			// write some data
			n, err = conn.Write(buf)
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)

			// wait until we're ready to stop
			_ = <-stopListener
			conn.Close()
			wg.Done()
		}(t)

		// client side
		wg.Add(1)
		go func(t *testing.T) {

			buf := make([]byte, len(testData))

			// dial server
			conn, err := tls.Dial("tcp", listener.Addr().String(), clientTlsConfig)
			assert.NoError(t, err)

			// write some data
			n, err := conn.Write([]byte(testData))
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)

			// read some data
			n, err = conn.Read(buf)
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)
			assert.Equal(t, testData, string(buf))

			err = conn.Close()
			assert.NoError(t, err)

			// stop listener
			stopListener <- true
			wg.Done()
		}(t)

		wg.Wait()
	})

}
