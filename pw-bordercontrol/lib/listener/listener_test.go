package listener

import (
	"pw_bordercontrol/lib/feedprotocol"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
)

func TestListener(t *testing.T) {

	// get temp listener addr
	tmpListener, err := nettest.NewLocalListener("tcp4")
	require.NoError(t, err)
	tmpListener.Close()

	t.Run("NewListener invalid port", func(t *testing.T) {
		_, err := NewListener("0.0.0.0:12345c", feedprotocol.MLAT, "test-feed-in")
		assert.NoError(t, err)

	})

}
