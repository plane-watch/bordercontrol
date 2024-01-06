package nats_io

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
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
		return &natsserver.Server{}, errors.New("NATS server didn't start")
	}
	return server, nil
}

func TestNats(t *testing.T) {

	t.Run("test functions before initialise", func(t *testing.T) {

		t.Run("isInitialised", func(t *testing.T) {
			require.False(t, initialised)
		})

		t.Run("GetInstance", func(t *testing.T) {
			_, err := GetInstance()
			require.Error(t, err)
			require.Equal(t, ErrNatsNotInitialised.Error(), err.Error())
		})

		t.Run("ThisInstance", func(t *testing.T) {
			_, _, err := ThisInstance("*")
			require.Error(t, err)
			require.Equal(t, ErrNatsNotInitialised.Error(), err.Error())
		})

		t.Run("IsConnected", func(t *testing.T) {
			require.False(t, IsConnected())
		})

		t.Run("Sub", func(t *testing.T) {
			err := Sub("test", func(msg *nats.Msg) {})
			require.Error(t, err)
			require.Equal(t, ErrNatsNotInitialised.Error(), err.Error())
		})

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
