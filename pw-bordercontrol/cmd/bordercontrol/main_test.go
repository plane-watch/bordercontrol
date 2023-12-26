package main

import (
	"crypto/sha256"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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
	createSignalChannels()

	// send SIGHUP
	err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
	assert.NoError(t, err)

	// check SIGHUP was received
	select {
	case <-time.After(time.Second * 5):
		assert.Fail(t, "timeout reading chanSIGHUP")
	case s := <-chanSIGHUP:
		assert.Equal(t, syscall.SIGHUP, s)
	}

	// send SIGUSR1
	err = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
	assert.NoError(t, err)

	// check SIGUSR1 was received
	select {
	case <-time.After(time.Second * 5):
		assert.Fail(t, "timeout reading chanSIGUSR1")
	case s := <-chanSIGUSR1:
		assert.Equal(t, syscall.SIGUSR1, s)
	}
}
