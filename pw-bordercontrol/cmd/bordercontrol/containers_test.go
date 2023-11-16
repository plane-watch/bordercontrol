package main

import (
	"testing"

	"github.com/docker/docker/testutil/daemon"
)

func TestPrepTestEnvironment(t *testing.T) {

	d := daemon.New(
		t,
		daemon.WithContainerdSocket("/run/containerd/containerd.sock"),
	)
	d.StartWithBusybox(t)

	d.Stop(t)
	d.Cleanup(t)

}
