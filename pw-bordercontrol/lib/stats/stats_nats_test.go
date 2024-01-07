package stats

import (
	"pw_bordercontrol/lib/feedprotocol"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testInstance = "testInst"
)

func TestInit(t *testing.T) {
	//  override functions for testing
	natsGetInstance = func() (instance string, err error) {
		return testInstance, nil
	}
	natsSub = func(subj string, handler func(msg *nats.Msg)) error {
		return nil
	}

	err := initNats()
	require.NoError(t, err)
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

func TestNatsSubjFeedersMetricsHandler(t *testing.T) {

	wg := sync.WaitGroup{}

	// override function for testing
	wg.Add(1)
	natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
		t.Log(reply.Header)
		t.Log(reply.Data)
		wg.Done()
		return nil
	}

	msg := nats.NewMsg("")
	natsSubjFeedersMetricsHandler(msg)
	wg.Wait()

}
