package feedproxy

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionTracking_getNum(t *testing.T) {

	t.Run("normal", func(t *testing.T) {
		ct := incomingConnectionTracker{}
		for i := uint(1); i <= 3; i++ {
			n := ct.getNum()
			assert.Equal(t, i, n)
		}
	})

	t.Run("getNum_maxuint", func(t *testing.T) {
		ct := incomingConnectionTracker{}
		ct.connectionNumber = ^uint(0)
		for i := uint(1); i <= 3; i++ {
			n := ct.getNum()
			assert.Equal(t, i, n)
		}
	})

	t.Run("getNum_inuse", func(t *testing.T) {
		ct := incomingConnectionTracker{}

		ct.connections = append(ct.connections, incomingConnection{
			connNum: 1,
		})

		ct.connectionNumber = ^uint(0)
		n := ct.getNum()
		assert.Equal(t, uint(2), n)
	})
}

func TestConnectionTracking_evict(t *testing.T) {

	t.Run("evict", func(t *testing.T) {
		ct := incomingConnectionTracker{}
		ct.connections = append(ct.connections, incomingConnection{
			connTime: time.Now().Add(-((maxIncomingConnectionRequestSeconds + 1) * time.Second)),
		})
		ct.evict()
		assert.Equal(t, 0, len(ct.connections))
	})

	t.Run("no evict", func(t *testing.T) {
		ct := incomingConnectionTracker{}
		ct.connections = append(ct.connections, incomingConnection{
			connTime: time.Now(),
		})
		ct.evict()
		assert.Equal(t, 1, len(ct.connections))
	})
}

func TestConnectionTracking_check(t *testing.T) {

	t.Run("normal", func(t *testing.T) {
		ct := incomingConnectionTracker{}

		testIPAddr, err := net.ResolveIPAddr("", "127.0.0.1")
		require.NoError(t, err)

		connNum := ct.getNum()

		err = ct.check(testIPAddr.IP, connNum)
		require.NoError(t, err)
	})

	t.Run("too frequent", func(t *testing.T) {
		ct := incomingConnectionTracker{}

		testIPAddr, err := net.ResolveIPAddr("", "127.0.0.1")
		require.NoError(t, err)

		for i := 1; i <= maxIncomingConnectionRequestsPerSrcIP+2; i++ {
			connNum := ct.getNum()

			err = ct.check(testIPAddr.IP, connNum)

			if i > maxIncomingConnectionRequestsPerSrcIP {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "client connecting too frequently")
			} else {
				require.NoError(t, err)
			}
		}
	})
}

func TestGetConnectionNumber(t *testing.T) {

	t.Run("not initialised", func(t *testing.T) {
		_, err := GetConnectionNumber()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotInitialised)
	})

	initialised = true

	t.Run("not initialised", func(t *testing.T) {
		for i := uint(1); i <= 3; i++ {
			n, err := GetConnectionNumber()
			require.NoError(t, err)
			assert.Equal(t, i, n)
		}
	})

}
