package stunnel

import (
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

var (
	TestSNI = uuid.New()
)

func TestStunnel(t *testing.T) {

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
			tmpCertFileName := fmt.Sprintf("pw-bordercontrol-testing-%s-certfile", t.Name())
			t.Cleanup(func() {
				os.Remove(tmpCertFileName)
			})

			tmpCertFile, err := os.CreateTemp("", tmpCertFileName)
			assert.NoError(t, err)
			t.Cleanup(func() {
				tmpCertFile.Close()
			})

			tmpKeyFileName := fmt.Sprintf("pw-bordercontrol-testing-%s-keyfile", t.Name())
			t.Cleanup(func() {
				os.Remove(tmpKeyFileName)
			})

			tmpKeyFile, err := os.CreateTemp("", tmpKeyFileName)
			assert.NoError(t, err)
			t.Cleanup(func() {
				tmpKeyFile.Close()
			})

			err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
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
