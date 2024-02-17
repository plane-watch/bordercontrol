package feedproxy

import (
	"flag"
	"net"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

func connToChans(conn net.Conn, readBufSize int) (readChan, writeChan chan []byte) {
	// tcpConnToChans provides two channels, a readChan & writeChan.
	// readChan will be populated with reads from conn.
	// Data sent to writeChan will be written to conn.
	// User should defer close(writeChan).
	// readChan will be closed when writeChan is closed or the connection is closed.

	var wg sync.WaitGroup

	// turn off deadlines
	err := conn.SetDeadline(time.Time{})
	if err != nil {
		log.Err(err).Msg("error disabling deadline")
	}
	err = conn.SetReadDeadline(time.Time{})
	if err != nil {
		log.Err(err).Msg("error disabling read deadline")
	}
	err = conn.SetWriteDeadline(time.Time{})
	if err != nil {
		log.Err(err).Msg("error disabling write deadline")
	}

	// make channels
	readChan = make(chan []byte)
	writeChan = make(chan []byte)

	// read conn into readChan
	wg.Add(1)
	go func() {

		buf := make([]byte, readBufSize)

		wg.Done() // goroutine is running

		for {
			n, err := conn.Read(buf)
			if err != nil {
				// if read error, break out of loop
				log.Err(err).Msg("error reading from connection")
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
				log.Error().Msg("read from writeChan not ok")
				break
			}
			_, err := conn.Write(buf)
			if err != nil {
				// if write error, break out of loop
				log.Err(err).Msg("error writing to connection")
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
