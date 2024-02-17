package feedproxy

import (
	"flag"
	"net"
	"sync"

	"github.com/rs/zerolog/log"
)

func connToChans(conn net.Conn, readBufSize int) (readChan, writeChan chan []byte) {
	// tcpConnToChans provides two channels, a readChan & writeChan.
	// readChan will be populated with reads from conn.
	// Data sent to writeChan will be written to conn.
	// User should defer close(writeChan).
	// readChan will be closed when writeChan is closed or the connection is closed.

	var wg sync.WaitGroup

	// make channels
	readChan = make(chan []byte)
	writeChan = make(chan []byte)

	// read conn into readChan
	wg.Add(1)
	go func() {

		// logging for during testing
		if flag.Lookup("test.v") != nil {
			log.Trace().Msg("read goroutine started")
			defer log.Trace().Msg("read goroutine finished")
		}

		buf := make([]byte, readBufSize)

		wg.Done() // goroutine is running

		for {
			n, err := conn.Read(buf)
			if err != nil {
				// if read error, break out of loop
				break
			}
			readChan <- buf[:n]
		}
		// If here, then there's been an error. Close channel & connection.
		close(readChan)
		conn.Close()
	}()

	// write to conn from writeChan
	wg.Add(1)
	go func() {

		// logging for during testing
		if flag.Lookup("test.v") != nil {
			log.Trace().Msg("write goroutine started")
			defer log.Trace().Msg("write goroutine finished")
		}

		wg.Done() // goroutine is running

		for {
			buf, ok := <-writeChan
			if !ok {
				// if error reading from chan, break out of loop
				break
			}
			_, err := conn.Write(buf)
			if err != nil {
				// if write error, break out of loop
				break
			}
		}
		// If here, then there's been an error. Close channel & connection.
		conn.Close()
	}()

	// wait for goroutines to initialise
	wg.Wait()

	return readChan, writeChan
}
