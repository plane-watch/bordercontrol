package nats_io

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

var (
	testInstanceName   = "pw_bordercontrol_testing"
	testVersion        = "0.0.0 (aabbccd), 2024-01-06T11:53:19Z"
	testSubject        = "pw_bordercontrol.testing.test"
	testSubjectToChan  = "pw_bordercontrol.testing.testchan"
	testMsgHeaderName  = "testHeaderName"
	testMsgHeaderValue = "testHeaderValue"
	testMsgData        = "The quick brown fox jumped over the lazy dog 1234567890 times."
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
	require.NoError(t, err)
	t.Cleanup(func() {
		ns.Shutdown()
	})

	// get test client
	testNatsClient, err := nats.Connect(ns.ClientURL())
	t.Cleanup(func() {
		testNatsClient.Close()
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
			require.NoError(t, err)
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

		t.Run("IsConnected", func(t *testing.T) {
			require.True(t, IsConnected())
		})

		t.Run("Sub", func(t *testing.T) {
			wg := sync.WaitGroup{}

			// subscribe to test subject
			wg.Add(1)
			Sub(testSubject, func(msg *nats.Msg) {

				h := msg.Header.Get(testMsgHeaderName)
				assert.Equal(t, testMsgHeaderValue, h)

				d := msg.Data
				assert.Equal(t, testMsgData, string(d))

				require.NoError(t, msg.Ack())

				wg.Done()
			})

			// wait for nats
			time.Sleep(time.Second)

			// send test req
			testMsg := nats.NewMsg(testSubject)
			testMsg.Header.Add(testMsgHeaderName, testMsgHeaderValue)
			testMsg.Data = []byte(testMsgData)
			_, err := testNatsClient.RequestMsg(testMsg, time.Second*5)
			require.NoError(t, err)

			// wait for sub function to complete
			wg.Wait()

		})

		t.Run("SignalSendOnSubj", func(t *testing.T) {

			// test prep
			wg := sync.WaitGroup{}
			ch := make(chan os.Signal)

			// subscribe
			err := SignalSendOnSubj(testSubjectToChan, syscall.SIGHUP, ch)
			require.NoError(t, err)

			// wait for nats
			time.Sleep(time.Second)

			// send test req
			wg.Add(1)
			go func(t *testing.T) {
				testMsg := nats.NewMsg(testSubjectToChan)
				testMsg.Data = []byte("*")
				_, err := testNatsClient.RequestMsg(testMsg, time.Second*5)
				require.NoError(t, err)
				wg.Done()
			}(t)

			// check channel
			wg.Add(1)
			go func(t *testing.T) {
				sig := <-ch
				require.Equal(t, syscall.SIGHUP, sig)
				wg.Done()
			}(t)

			// wait for tests
			wg.Wait()

		})

	})

}
