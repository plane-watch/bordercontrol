package listener

import (
	"context"
	"net"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/feedproxy"
	"sync"
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

	// override func for testing (remove TLS/SSL)
	stunnelNewListenerWrapper = func(network string, laddr string) (l net.Listener, err error) {
		return net.Listen(network, laddr)
	}

	// override func for testing (no error)
	proxyConnStartWrapper = func(f *feedproxy.ProxyConnection, ctx context.Context) error {
		return nil
	}

	// override func for testing (return number, no error)
	feedproxyGetConnectionNumberWrapper = func() (num uint, err error) {
		return 123, nil
	}

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

		t.Run("ok", func(t *testing.T) {
			listener, err = NewListener(tmpListener.Addr().String(), feedprotocol.MLAT, "test-feed-in")
			require.NoError(t, err)
		})
	})

	t.Run("Run", func(t *testing.T) {

		t.Run("invalid protocol", func(t *testing.T) {

			listener, err = NewListener(tmpListener.Addr().String(), feedprotocol.Protocol(0), "test-feed-in")
			require.NoError(t, err)

			ctx := context.Background()
			err := listener.Run(ctx)
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

		t.Run("working", func(t *testing.T) {

			var (
				wg     sync.WaitGroup
				ctx    context.Context
				cancel context.CancelFunc
			)

			wg.Add(1)
			go func(t *testing.T) {

				listener, err = NewListener(tmpListener.Addr().String(), feedprotocol.MLAT, "test-feed-in")
				require.NoError(t, err)

				ctx, cancel = context.WithCancel(context.Background())

				err := listener.Run(ctx)
				require.NoError(t, err)
				wg.Done()
			}(t)

			t.Run("client connection", func(t *testing.T) {
				conn, err := net.Dial("tcp4", tmpListener.Addr().String())
				require.NoError(t, err)
				conn.Close()
			})

			cancel()

			wg.Wait()

		})

	})

}
