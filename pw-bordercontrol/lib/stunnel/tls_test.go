package stunnel

import (
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

var (
	TestSNI = uuid.New()
)

func TestTLS_CertReload(t *testing.T) {

	Init(syscall.SIGHUP)

	t.Log("preparing test environment TLS cert/key")

	err := PrepTestEnvironmentTLSCertAndKey()
	assert.NoError(t, err)

	// test reload via signal
	t.Log("sending SIGHUP for cert/key reload (working)")
	signalChan <- syscall.SIGHUP

	// wait for the channel to be read
	time.Sleep(time.Second)

	// test reload via signal
	t.Log("sending SIGHUP for cert/key reload (will not work, files missing)")
	signalChan <- syscall.SIGHUP
}
