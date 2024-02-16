package feedproxy

import (
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTcpConnToChans(t *testing.T) {

	// set up connections
	connA, connB := net.Pipe()
	rC, wC := connToChans(connB, 1024)

	t.Run("write", func(t *testing.T) {

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

	t.Run("read", func(t *testing.T) {

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

}
