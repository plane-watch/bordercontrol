package stats

import (
	"errors"
	"fmt"
	"net"
	"pw_bordercontrol/lib/feedprotocol"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testInstance = "testInst"

	ErrTesting = errors.New("Error injected for testing")
)

func TestNats(t *testing.T) {

	t.Run("initNats", func(t *testing.T) {

		t.Run("GetInstance error", func(t *testing.T) {
			// copy original func
			natsGetInstanceOriginal := natsGetInstance

			// override func for testing
			natsGetInstance = func() (instance string, err error) {
				return testInstance, ErrTesting
			}

			// test
			err := initNats()
			require.Error(t, err)
			require.Equal(t, ErrTesting.Error(), err.Error())

			// revert original func
			natsGetInstance = natsGetInstanceOriginal
		})

		t.Run("Sub error", func(t *testing.T) {
			// copy original func
			natsSubOriginal := natsSub

			// override func for testing
			natsSub = func(subj string, handler func(msg *nats.Msg)) error {
				return ErrTesting
			}

			// test
			err := initNats()
			require.Error(t, err)

			// revert original func
			natsSub = natsSubOriginal
		})

		t.Run("working", func(t *testing.T) {

			//  override functions for testing
			natsGetInstance = func() (instance string, err error) {
				return testInstance, nil
			}
			natsSub = func(subj string, handler func(msg *nats.Msg)) error {
				return nil
			}

			err := initNats()
			require.NoError(t, err)

		})

	})

}

func TestGetProtocolFromLastToken(t *testing.T) {
	fp, err := getProtocolFromLastToken("x.x.x.x.beast")
	require.NoError(t, err)
	require.Equal(t, feedprotocol.BEAST, fp)

	fp, err = getProtocolFromLastToken("x.x.x.x.mlat")
	require.NoError(t, err)
	require.Equal(t, feedprotocol.MLAT, fp)

	_, err = getProtocolFromLastToken("x.x.x.x.gopher")
	require.Error(t, err)
	require.Equal(t, feedprotocol.ErrUnknownProtocol.Error(), err.Error())
}

func TestParseApiKeyFromMsgData(t *testing.T) {

	u := uuid.New()

	msg := nats.NewMsg("x.x.x.x")
	msg.Data = []byte(u.String())
	apiKey, err := parseApiKeyFromMsgData(msg)
	assert.NoError(t, err)
	assert.Equal(t, u, apiKey)

	msg = nats.NewMsg("x.x.x.x")
	msg.Data = []byte("not an api key")
	_, err = parseApiKeyFromMsgData(msg)
	assert.Error(t, err)
}

func TestMetrics(t *testing.T) {

	wg := sync.WaitGroup{}

	// init stats variable
	stats.Feeders = make(map[uuid.UUID]FeederStats)

	stats.Feeders[TestFeederAPIKey] = FeederStats{
		Label:       TestFeederLabel,
		Code:        TestFeederCode,
		Connections: make(map[string]ProtocolDetail),
		TimeUpdated: time.Now(),
	}
	stats.Feeders[TestFeederAPIKey].Connections[feedprotocol.ProtocolNameBEAST] = ProtocolDetail{
		Status:               true,
		ConnectionCount:      1,
		MostRecentConnection: time.Now(),
		ConnectionDetails:    make(map[uint]ConnectionDetail),
	}
	stats.Feeders[TestFeederAPIKey].Connections[feedprotocol.ProtocolNameBEAST].ConnectionDetails[1] = ConnectionDetail{
		Src: &net.TCPAddr{
			IP:   net.IPv4(1, 2, 3, 4),
			Port: 22222,
		},
		Dst: &net.TCPAddr{
			IP:   net.IPv4(3, 4, 5, 6),
			Port: 22222,
		},
		TimeConnected: time.Now(),
		BytesIn:       uint64(123456789),
		BytesOut:      uint64(987654321),
	}
	stats.Feeders[TestFeederAPIKey].Connections[feedprotocol.ProtocolNameMLAT] = ProtocolDetail{
		Status:               true,
		ConnectionCount:      2,
		MostRecentConnection: time.Now(),
		ConnectionDetails:    make(map[uint]ConnectionDetail),
	}
	stats.Feeders[TestFeederAPIKey].Connections[feedprotocol.ProtocolNameMLAT].ConnectionDetails[2] = ConnectionDetail{
		Src: &net.TCPAddr{
			IP:   net.IPv4(1, 2, 3, 4),
			Port: 33333,
		},
		Dst: &net.TCPAddr{
			IP:   net.IPv4(3, 4, 5, 6),
			Port: 33333,
		},
		TimeConnected: time.Now(),
		BytesIn:       uint64(123456789),
		BytesOut:      uint64(987654321),
	}

	t.Run("natsSubjFeedersMetricsHandler", func(t *testing.T) {
		// copy original function
		natsRespondMsgOriginal := natsRespondMsg

		// override function for testing
		wg.Add(1)
		natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
			t.Log(reply.Header)
			t.Log(string(reply.Data))
			wg.Done()
			return nil
		}

		msg := nats.NewMsg("")
		natsSubjFeedersMetricsHandler(msg)
		wg.Wait()

		// restore original function
		natsRespondMsg = natsRespondMsgOriginal
	})

	t.Run("natsSubjFeederMetricsAllProtocolsHandler", func(t *testing.T) {
		// copy original function
		natsRespondMsgOriginal := natsRespondMsg

		// override function for testing
		wg.Add(1)
		natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
			t.Log(reply.Header)
			t.Log(string(reply.Data))
			wg.Done()
			return nil
		}

		msg := nats.NewMsg("")
		msg.Data = []byte(TestFeederAPIKey.String())
		natsSubjFeederMetricsAllProtocolsHandler(msg)
		wg.Wait()

		// restore original function
		natsRespondMsg = natsRespondMsgOriginal
	})

	t.Run(fmt.Sprintf("natsSubjFeederHandler %s", natsSubjFeederMetricsBEAST), func(t *testing.T) {
		// copy original function
		natsRespondMsgOriginal := natsRespondMsg

		// override function for testing
		wg.Add(1)
		natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
			t.Log(reply.Header)
			t.Log(string(reply.Data))
			wg.Done()
			return nil
		}

		msg := nats.NewMsg(natsSubjFeederMetricsBEAST)
		msg.Data = []byte(TestFeederAPIKey.String())
		natsSubjFeederHandler(msg)
		wg.Wait()

		// restore original function
		natsRespondMsg = natsRespondMsgOriginal
	})

	t.Run(fmt.Sprintf("natsSubjFeederHandler %s", natsSubjFeederMetricsMLAT), func(t *testing.T) {
		// copy original function
		natsRespondMsgOriginal := natsRespondMsg

		// override function for testing
		wg.Add(1)
		natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
			t.Log(reply.Header)
			t.Log(string(reply.Data))
			wg.Done()
			return nil
		}

		msg := nats.NewMsg(natsSubjFeederMetricsMLAT)
		msg.Data = []byte(TestFeederAPIKey.String())
		natsSubjFeederHandler(msg)
		wg.Wait()

		// restore original function
		natsRespondMsg = natsRespondMsgOriginal
	})

	t.Run(fmt.Sprintf("natsSubjFeederHandler %s", natsSubjFeederConnectedBEAST), func(t *testing.T) {
		// copy original function
		natsRespondMsgOriginal := natsRespondMsg

		// override function for testing
		wg.Add(1)
		natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
			t.Log(reply.Header)
			t.Log(string(reply.Data))
			wg.Done()
			return nil
		}

		msg := nats.NewMsg(natsSubjFeederConnectedBEAST)
		msg.Data = []byte(TestFeederAPIKey.String())
		natsSubjFeederHandler(msg)
		wg.Wait()

		// restore original function
		natsRespondMsg = natsRespondMsgOriginal
	})

	t.Run(fmt.Sprintf("natsSubjFeederHandler %s", natsSubjFeederConnectedMLAT), func(t *testing.T) {
		// copy original function
		natsRespondMsgOriginal := natsRespondMsg

		// override function for testing
		wg.Add(1)
		natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
			t.Log(reply.Header)
			t.Log(string(reply.Data))
			wg.Done()
			return nil
		}

		msg := nats.NewMsg(natsSubjFeederConnectedMLAT)
		msg.Data = []byte(TestFeederAPIKey.String())
		natsSubjFeederHandler(msg)
		wg.Wait()

		// restore original function
		natsRespondMsg = natsRespondMsgOriginal
	})

}
