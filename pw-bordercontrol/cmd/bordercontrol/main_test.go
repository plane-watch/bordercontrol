package main

import (
	"crypto/sha256"
	"os"
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

func TestSignalChannels(t *testing.T) {

	chanSIGHUP = make(chan os.Signal, 1)
	go func() {
		chanSIGHUP <- syscall.SIGHUP
	}()

	chanSIGUSR1 = make(chan os.Signal, 1)
	go func() {
		chanSIGUSR1 <- syscall.SIGUSR1
	}()

	// check SIGHUP
	select {
	case s := <-chanSIGHUP:
		assert.Equal(t, syscall.SIGHUP, s)
	case <-time.After(time.Second * 2):
		assert.Fail(t, "expected syscall.SIGHUP on chanSIGHUP")
	}

	// check SIGUSR1
	select {
	case s := <-chanSIGUSR1:
		assert.Equal(t, syscall.SIGUSR1, s)
	case <-time.After(time.Second * 2):
		assert.Fail(t, "expected syscall.SIGUSR1 on chanSIGUSR1")
	}

}
