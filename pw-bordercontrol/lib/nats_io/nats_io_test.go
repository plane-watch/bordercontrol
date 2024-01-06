package nats_io

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

var (
	testInstanceName = "pw_bordercontrol_testing"
	testVersion      = "0.0.0 (aabbccd), 2024-01-06T11:53:19Z"
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

	// test init
	t.Run("test Init", func(t *testing.T) {

		t.Run("bad url", func(t *testing.T) {
			// get host & port for testing
			tmpListener, err := nettest.NewLocalListener("tcp4")
			require.NoError(t, err)
			tmpListener.Close()

			conf := NatsConfig{
				Url:       tmpListener.Addr().String(),
				Instance:  testInstanceName,
				Version:   testVersion,
				StartTime: time.Now(),
			}
			err = conf.Init()
			require.Error(t, err)
		})

		t.Run("ensure not initialised", func(t *testing.T) {
			require.False(t, isInitialised())
		})

		t.Run("working", func(t *testing.T) {
			// get host & port for testing
			conf := NatsConfig{
				Url:       ns.ClientURL(),
				Instance:  testInstanceName,
				Version:   testVersion,
				StartTime: time.Now(),
			}
			err = conf.Init()
			require.Error(t, err)
		})

		t.Run("ensure initialised", func(t *testing.T) {
			require.True(t, isInitialised())
		})
	})

	t.Run("test functions after initialise", func(t *testing.T) {

		t.Run("GetInstance", func(t *testing.T) {
			i, e := GetInstance()
			require.NoError(t, e)
			require.Equal(t, testInstanceName, i)
		})

		t.Run("ThisInstance", func(t *testing.T) {

			t.Run("named", func(t *testing.T) {
				meantForThisInstance, thisInstanceName, err := ThisInstance(testInstanceName)
				require.NoError(t, err)
				require.True(t, meantForThisInstance)
				require.Equal(t, testInstanceName, thisInstanceName)
			})

			t.Run("wildcard", func(t *testing.T) {
				meantForThisInstance, thisInstanceName, err := ThisInstance("*")
				require.NoError(t, err)
				require.True(t, meantForThisInstance)
				require.Equal(t, testInstanceName, thisInstanceName)
			})

			t.Run("regex", func(t *testing.T) {
				meantForThisInstance, thisInstanceName, err := ThisInstance(fmt.Sprintf("%s.*", testInstanceName[:6]))
				require.NoError(t, err)
				require.True(t, meantForThisInstance)
				require.Equal(t, testInstanceName, thisInstanceName)
			})

			t.Run("not this instance, named", func(t *testing.T) {
				meantForThisInstance, thisInstanceName, err := ThisInstance(fmt.Sprintf("not-%s", testInstanceName))
				require.NoError(t, err)
				require.False(t, meantForThisInstance)
				require.Equal(t, testInstanceName, thisInstanceName)
			})

			t.Run("not this instance, regex", func(t *testing.T) {
				meantForThisInstance, thisInstanceName, err := ThisInstance(fmt.Sprintf("^not-%s.*", testInstanceName))
				require.NoError(t, err)
				require.False(t, meantForThisInstance)
				require.Equal(t, testInstanceName, thisInstanceName)
			})

		})

	})

}
