package nats_io

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

func RunTestServer() (*natsserver.Server, error) {

	// get host & port for testing
	tmpListener, err := nettest.NewLocalListener("tcp4")
	if err != nil {
		return &natsserver.Server{}, err
	}
	natsHost := strings.Split(tmpListener.Addr().String(), ":")[0]
	natsPort, err := strconv.Atoi(strings.Split(tmpListener.Addr().String(), ":")[1])
	if err != nil {
		return &natsserver.Server{}, err
	}
	tmpListener.Close()

	// create nats server
	server, err := natsserver.NewServer(&natsserver.Options{
		ServerName: "bordercontrol_test_server",
		Host:       natsHost,
		Port:       natsPort,
	})
	if err != nil {
		return &natsserver.Server{}, err
	}

	// start nats server
	server.Start()
	if !server.ReadyForConnections(time.Second * 5) {
		panic("NATS server didn't start")
	}
	return server, nil
}

func TestNats(t *testing.T) {

	// test isInitialised
	t.Run("isInitialised", func(t *testing.T) {
		require.False(t, initialised)
	})

	// start nats server
	ns, err := RunTestServer()
	if err != nil {
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		ns.Shutdown()
	})

}
