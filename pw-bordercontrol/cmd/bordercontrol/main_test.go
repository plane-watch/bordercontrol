package main

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestDFWTB(t *testing.T) {
	bannerCheckSum := sha256.Sum256([]byte(banner))
	fmt.Println(bannerCheckSum)
}
