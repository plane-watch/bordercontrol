package listener

import (
	"context"
	"net"
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

	var listener *listener

	t.Run("NewListener", func(t *testing.T) {

		t.Run("invalid port", func(t *testing.T) {
			_, err := NewListener("0.0.0.0:12345c", feedprotocol.MLAT, "test-feed-in")
			assert.Error(t, err)

		})

		t.Run("0.0.0.0", func(t *testing.T) {
			_, err := NewListener(":", feedprotocol.MLAT, "test-feed-in")
			require.Error(t, err)
		})

		// override func for testing (remove TLS/SSL)
		stunnelNewListenerWrapper = func(network string, laddr string) (l net.Listener, err error) {
			return net.Listen(network, laddr)
		}

		t.Run("ok", func(t *testing.T) {
			listener, err := NewListener(tmpListener.Addr().String(), feedprotocol.MLAT, "test-feed-in")
			require.NoError(t, err)
		})
	})

	t.Run("Run", func(t *testing.T) {

		t.Run("invalid protocol", func(t *testing.T) {

			listenerInvalidProto := listener
			listenerInvalidProto.Protocol = feedprotocol.Protocol(0)

			ctx := context.Background()
			err := listenerInvalidProto.Run(ctx)
			require.Error(t, err)
		})

		t.Run("addr in use", func(t *testing.T) {
			nl, err := nettest.NewLocalListener("tcp4")
			require.NoError(t, err)
			t.Cleanup(func() {
				nl.Close()
			})

			listenerAddrInUse, err := NewListener(nl.Addr().String(), feedprotocol.MLAT, "test-feed-in")
			require.NoError(t, err)

			ctx := context.Background()
			err = listenerAddrInUse.Run(ctx)
			require.Error(t, err)

		})

	})

}
