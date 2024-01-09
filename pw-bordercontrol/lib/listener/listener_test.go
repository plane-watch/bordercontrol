package listener

import (
	"context"
	"net"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/feedproxy"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
)

func TestListener(t *testing.T) {

	// get temp listener addr
	tmpListener, err := nettest.NewLocalListener("tcp4")
	require.NoError(t, err)
	tmpListener.Close()

	// copy original function & override for testing (remove TLS/SSL as tested separately)
	stunnelNewListenerWrapperOriginal := stunnelNewListenerWrapper
	stunnelNewListenerWrapper = func(network string, laddr string) (l net.Listener, err error) {
		return net.Listen(network, laddr)
	}

	// copy original function & override for testing (bypass proxy as tested elsewhere)
	proxyConnStartWrapperOriginal := proxyConnStartWrapper
	proxyConnStartWrapper = func(f *feedproxy.ProxyConnection, ctx context.Context) error {
		return nil
	}

	// copy original function & override for testing (return number, no error)
	feedproxyGetConnectionNumberWrapperOriginal := feedproxyGetConnectionNumberWrapper
	feedproxyGetConnectionNumberWrapper = func() (num uint, err error) {
		return 123, nil
	}

	// revert original functions
	t.Cleanup(func() {
		stunnelNewListenerWrapper = stunnelNewListenerWrapperOriginal
		proxyConnStartWrapper = proxyConnStartWrapperOriginal
		feedproxyGetConnectionNumberWrapper = feedproxyGetConnectionNumberWrapperOriginal
	})

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
				wg       sync.WaitGroup
				ctx      context.Context
				cancelMu sync.RWMutex
				cancel   context.CancelFunc
			)

			// get temp listener addr
			tmpListener, err := nettest.NewLocalListener("tcp4")
			require.NoError(t, err)
			tmpListener.Close()

			wg.Add(1)
			go func(t *testing.T) {

				listener, err = NewListener(tmpListener.Addr().String(), feedprotocol.MLAT, "test-feed-in")
				require.NoError(t, err)

				cancelMu.Lock()
				ctx, cancel = context.WithCancel(context.Background())
				cancelMu.Unlock()

				err := listener.Run(ctx)
				require.NoError(t, err)
				wg.Done()
			}(t)

			// wait for listener
			time.Sleep(time.Second)

			t.Run("client connection", func(t *testing.T) {
				conn, err := net.Dial("tcp4", tmpListener.Addr().String())
				require.NoError(t, err)
				conn.Close()
			})

			cancelMu.RLock()
			cancel()
			cancelMu.RUnlock()

			wg.Wait()

		})

	})

}
