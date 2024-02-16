package feedproxy

import (
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTcpConnToChans(t *testing.T) {

	// set up connections
	connA, connB := net.Pipe()

	t.Log(runtime.NumGoroutine())
	rC, wC := connToChans(connB, 1024)
	t.Log(runtime.NumGoroutine())

	t.Run("write ok", func(t *testing.T) {

		var wg sync.WaitGroup

		buf := make([]byte, 1024)

		// send data to write channel
		wg.Add(1)
		go func() {
			defer wg.Done()
			wC <- []byte("Hello World! 12345")
		}()

		// read from connection on other end of pipe
		n, err := connA.Read(buf)

		wg.Wait()

		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World! 12345"), buf[:n])

	})

	t.Run("read ok", func(t *testing.T) {

		var wg sync.WaitGroup

		// write to connection on other end of pipe
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := connA.Write([]byte("Hello World! 67890"))
			require.NoError(t, err)
		}()

		// receive data from read channel
		msg, ok := <-rC

		wg.Wait()

		require.True(t, ok)
		assert.Equal(t, []byte("Hello World! 67890"), msg)

	})

	t.Log(runtime.NumGoroutine())

	// close channel
	err := connA.Close()
	require.NoError(t, err)

	// wait for goroutines to finish
	time.Sleep(time.Second)

	t.Log(runtime.NumGoroutine())

	// ensure read channel closed
	_, ok := <-rC
	require.False(t, ok)

	// close write channel
	close(wC)

	// ensure connection closed
	one := make([]byte, 1)
	connB.SetReadDeadline(time.Now())
	_, err = connB.Read(one)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")

	t.Log(runtime.NumGoroutine())

}
