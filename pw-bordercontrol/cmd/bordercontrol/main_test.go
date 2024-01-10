package main

import (
	"context"
	"crypto/sha256"
	"pw_bordercontrol/lib/feedprotocol"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
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

func TestLogNumGoroutines(t *testing.T) {
	sc := make(chan bool)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		logNumGoroutines(time.Second, sc)
		wg.Done()
	}()
	time.Sleep(time.Second * 2)
	sc <- true
	wg.Wait()
}

func TestListenWithContext(t *testing.T) {

	tmpListener, err := nettest.NewLocalListener("tcp4")
	require.NoError(t, err)

	err = tmpListener.Close()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		listenWithContext(
			ctx,
			tmpListener.Addr().String(),
			feedprotocol.BEAST,
			"test-feed-in-",
			12345,
		)
	}()

	time.Sleep(time.Second * 5)

	cancel()

	time.Sleep(time.Second * 5)

}
