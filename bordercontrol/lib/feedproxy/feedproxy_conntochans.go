package feedproxy

import (
	"net"
)

func connToChans(conn net.Conn, readBufSize int) (readChan, writeChan chan []byte) {
	// tcpConnToChans provides two channels, a readChan & writeChan.
	// readChan will be populated with reads from conn.
	// Any thing sent to writeChan will be written to conn.

	readChan = make(chan []byte)
	writeChan = make(chan []byte)

	// read conn into readChan
	go func() {
		buf := make([]byte, readBufSize)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				// if read error, break out of loop
				break
			}
			readChan <- buf
		}
		// If here, then there's been an error. Close channel & connection.
		close(readChan)
		conn.Close()
	}()

	// write to conn from writeChan
	go func() {
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
		close(readChan)
		conn.Close()
	}()

	return readChan, writeChan
}
