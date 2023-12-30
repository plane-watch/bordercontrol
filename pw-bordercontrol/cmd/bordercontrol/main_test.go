package main

import (
	"crypto/sha256"
	"net"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

func getListenableAddress(t *testing.T) (tempTcpAddr string) {
	// get testing host/port
	n, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err, "could not generate new local listener for test")
	tempTcpAddr = n.Addr().String()
	err = n.Close()
	assert.NoError(t, err, "could not close temp local listener for test")
	return tempTcpAddr
}

func TestDFWTB(t *testing.T) {
	// don't mess with the banner!
	bannerCheckSum := sha256.Sum256([]byte(banner))
	expectedCheckSum := [32]uint8{28, 39, 253, 10, 28, 108, 170, 133, 71, 150, 147, 107, 235, 39, 187, 141, 112, 229, 54, 58, 2, 39, 205, 10, 136, 172, 42, 112, 13, 56, 182, 97}
	assert.Equal(t, expectedCheckSum, bannerCheckSum, "don't mess with the banner! :-)")
}

func TestGetRepoInfo(t *testing.T) {
	ch, ct := getRepoInfo()

	// return unknown during testing
	assert.Equal(t, "unknown", ch)
	assert.Equal(t, "unknown", ct)
}

func TestCreateSignalChannels(t *testing.T) {

	// create signal channels
	t.Log("create signal channels")
	createSignalChannels()

	// send SIGHUP
	t.Log("send SIGHUP")
	err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
	assert.NoError(t, err)

	// check SIGHUP was received
	t.Log("check SIGHUP was received")
	select {
	case <-time.After(time.Second * 5):
		assert.Fail(t, "timeout reading chanSIGHUP")
	case s := <-chanSIGHUP:
		assert.Equal(t, syscall.SIGHUP, s)
		t.Log("it was")
	}

	t.Log("test complete")
}

func TestListener(t *testing.T) {

	prepTestEnvironmentTLS(t)

	tempAddr := getListenableAddress(t)
	ip := strings.Split(tempAddr, ":")[0]
	port, err := strconv.Atoi(strings.Split(tempAddr, ":")[1])
	assert.NoError(t, err, "could not split address string")

	// prep listener config
	conf := listenConfig{
		listenProto: protoBEAST,
		listenAddr: net.TCPAddr{
			IP:   net.ParseIP(ip),
			Port: port,
		},
		mgmt: &goRoutineManager{},
	}

	// stop listener without accepting connection
	conf.mgmt.Stop()

	// start listener
	err = listener(conf)

	// ensure no errors
	assert.NoError(t, err)
}
