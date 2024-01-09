package main

import (
	"crypto/sha256"
	"sync"
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
