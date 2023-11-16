package main

import (
	"testing"

	"github.com/docker/docker/testutil/daemon"
)

func TestPrepTestEnvironment(t *testing.T) {

	d := daemon.New(t)
	d.StartWithBusybox(t)

}
