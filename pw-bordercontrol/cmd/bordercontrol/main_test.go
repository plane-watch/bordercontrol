package main

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDFWTB(t *testing.T) {
	// poor attempt at humour
	bannerCheckSum := sha256.Sum256([]byte(banner))
	expectedCheckSum := []byte{28, 39, 253, 10, 28, 108, 170, 133, 71, 150, 147, 107, 235, 39, 187, 141, 112, 229, 54, 58, 2, 39, 205, 10, 136, 172, 42, 112, 13, 56, 182, 97}
	assert.Equal(t, expectedCheckSum, bannerCheckSum)
}
