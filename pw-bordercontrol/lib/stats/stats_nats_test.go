package stats

import (
	"pw_bordercontrol/lib/feedprotocol"
	"testing"

	"github.com/nats-io/nats.go"
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
