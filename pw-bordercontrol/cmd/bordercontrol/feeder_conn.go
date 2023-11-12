package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
)

type (
	stateBeast int64
	stateMLAT  int64

	// structs to hold incoming connection data
	incomingConnectionTracker struct {
		mu               sync.RWMutex
		connections      []incomingConnection
		connectionNumber uint // to allocate connection numbers
	}
	incomingConnection struct {
		srcIP    net.IP
		connTime time.Time
		connNum  uint
	}
)

const (
	// limits maximum number of connections per feeder, per protocol to this many:
	maxConnectionsPerProto = 1

	// limits connection attempts to
	//   "maxIncomingConnectionRequestsPerProto" (2) attempts in a
	//   "maxIncomingConnectionRequestSeconds" (10) second period
	maxIncomingConnectionRequestsPerProto = 3
	maxIncomingConnectionRequestSeconds   = 10
)

const (
	stateBeastNotAuthenticated stateBeast = iota
	stateBeastAuthenticated
	stateBeastFeedInContainerConnected
	stateBeastCloseConnection
)

const (
	stateMLATNotAuthenticated stateMLAT = iota
	stateMLATAuthenticated
	stateMLATMuxContainerConnected
)

// func (connNum *connectionNumber) GetNum() (num uint) {

// 	// log := log.With().
// 	// 	Strs("func", []string{"feeder_conn.go", "GetNum"}).
// 	// 	Uint("num", num).
// 	// 	Logger()

// 	connNum.mu.Lock()
// 	defer connNum.mu.Unlock()
// 	connNum.num++
// 	if connNum.num == 0 {
// 		connNum.num++
// 	}
// 	return connNum.num
// }

func (t *incomingConnectionTracker) GetNum() (num uint) {

	var dupe bool

	t.mu.Lock()
	defer t.mu.Unlock()

	for {

		t.connectionNumber++
		if t.connectionNumber == 0 {
			t.connectionNumber++
		}

		dupe = false

		for _, c := range t.connections {
			if t.connectionNumber == c.connNum {
				dupe = true
				break
			}
		}

		if !dupe {
			break
		}
	}

	return t.connectionNumber
}

func (t *incomingConnectionTracker) evict() {
	// evicts connections from the tracker if older than 10 seconds

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "evict"}).
		Logger()

	log.Trace().Msg("started")

	t.mu.Lock()
	defer t.mu.Unlock()

	// slice magic: https://stackoverflow.com/questions/20545743/how-to-remove-items-from-a-slice-while-ranging-over-it
	i := 0
	for _, c := range t.connections {

		// keep connections tracked if less than maxIncomingConnectionRequestSeconds seconds old
		if !c.connTime.Add(time.Second * maxIncomingConnectionRequestSeconds).Before(time.Now()) {
			t.connections[i] = c
			i++
		}
	}
	t.connections = t.connections[:i]

	log.Trace().Int("active_connections", i).Msg("evicted connections")
}

func (t *incomingConnectionTracker) check(srcIP net.IP, connNum uint) (err error) {
	// checks an incoming connection
	// allows 'maxIncomingConnectionRequestsPerProto' connections every 10 seconds

	var connCount uint

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "check"}).
		IPAddr("srcIP", srcIP).
		Logger()

	log.Debug().Msg("started")

	// count number of connections from this source IP
	t.mu.RLock()
	for _, c := range t.connections {
		if c.srcIP.Equal(srcIP) {
			connCount++
		}
	}
	t.mu.RUnlock()

	if connCount >= maxIncomingConnectionRequestsPerProto {
		// if connecting too frequently, raise an error
		err = errors.New(fmt.Sprintf("more than %d connections from src within a %d second period",
			maxIncomingConnectionRequestsPerProto,
			maxIncomingConnectionRequestSeconds,
		))

	} else {
		// otherwise, don't raise an error but add this connection to the tracker
		t.mu.Lock()
		t.connections = append(t.connections, incomingConnection{
			srcIP:    srcIP,
			connTime: time.Now(),
			connNum:  connNum,
		})
		t.mu.Unlock()
	}
	log.Debug().Uint("connection_count", connCount).AnErr("err", err).Msg("finished")

	return err
}

func dialContainerTCP(container string, port int) (c *net.TCPConn, err error) {

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "dialContainerTCP"}).
		Str("container", container).
		Int("port", port).
		Logger()

	// perform DNS lookup
	var dstIP net.IP
	dstIPs, err := net.LookupIP(container)
	if err != nil {
		return c, err
	}
	if len(dstIPs) > 0 {
		dstIP = dstIPs[0]
	} else {
		return c, errors.New("container DNS lookup returned no IPs")
	}
	log = log.With().IPAddr("ip", dstIP).Logger()

	// prep address to connect to
	dstTCPAddr := net.TCPAddr{
		IP:   dstIP,
		Port: port,
	}

	// dial feed-in container
	log.Debug().Msg("performing DialTCP to IP")
	c, err = net.DialTCP("tcp", nil, &dstTCPAddr)

	return c, err
}

func authenticateFeeder(ctx *cli.Context, connIn net.Conn, log zerolog.Logger) (clientApiKey uuid.UUID, refLat, refLon float64, mux, label string, err error) {
	// authenticates a feeder

	log = log.With().
		Strs("func", []string{"feeder_conn.go", "authenticateFeeder"}).
		Logger()

	// check TLS handshake
	tlscon := connIn.(*tls.Conn)
	if tlscon.ConnectionState().HandshakeComplete {

		// check valid uuid was returned as ServerName (sni)
		clientApiKey, err = uuid.Parse(tlscon.ConnectionState().ServerName)
		if err != nil {
			err := errors.New("client sent invalid SNI")
			return clientApiKey, refLat, refLon, mux, label, err
		}

		// check valid api key
		if isValidApiKey(clientApiKey) {

			// update log context with client uuid
			log = log.With().Str("uuid", clientApiKey.String()).Logger()
			log.Info().Msg("client connected")

			// get feeder info (lat/lon/mux/label) from atc cache
			refLat, refLon, mux, label, err = getFeederInfo(clientApiKey)
			if err != nil {
				return clientApiKey, refLat, refLon, mux, label, err
			}

			// update stats
			stats.setFeederDetails(clientApiKey, label, refLat, refLon)

		} else {
			// if API is not valid, then kill the connection
			err := errors.New("client sent invalid api key")
			return clientApiKey, refLat, refLon, mux, label, err
		}

	} else {
		// if TLS handshake is not complete, then kill the connection
		err := errors.New("data received before tls handshake")
		return clientApiKey, refLat, refLon, mux, label, err
	}
	return clientApiKey, refLat, refLon, mux, label, err
}

func readFromClient(c net.Conn, buf []byte) (n int, err error) {
	// reads data from incoming client connection

	// log := log.With().
	// 	Strs("func", []string{"feeder_conn.go", "readFromClient"}).
	// 	Logger()

	n, err = c.Read(buf)
	if err != nil {
		if err.Error() == "tls: first record does not look like a TLS handshake" {
			defer c.Close()
			return n, err
		} else if err.Error() == "EOF" {
			defer c.Close()
			return n, err
		} else {
			defer c.Close()
			return n, err
		}
	}
	return n, err
}

func clientMLATConnection(ctx *cli.Context, clientConn net.Conn, tlsConfig *tls.Config, connNum uint) {
	// handles incoming MLAT connections

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "clientMLATConnection"}).
		Str("listener", protoMLAT).
		Uint("connNum", connNum).
		Logger()

	log.Debug().Msg("started")

	defer clientConn.Close()

	var (
		connectionState    = stateMLATNotAuthenticated
		sendRecvBufferSize = 256 * 1024 // 256kB
		muxConn            *net.TCPConn
		muxConnErr         error
		clientApiKey       uuid.UUID
		mux, label         string
		bytesRead          int
		err                error
		lastAuthCheck      time.Time
	)

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(clientConn.RemoteAddr().String(), ":")[0])
	log = log.With().IPAddr("src", remoteIP).Logger()

	// check for too-frequent incoming connections
	err = incomingConnTracker.check(remoteIP, connNum)
	if err != nil {
		log.Err(err).Msg("dropping connection")
		return
	}

	// make buffer to hold data read from client
	inBuf := make([]byte, sendRecvBufferSize)

	for {

		if connectionState == stateMLATNotAuthenticated {
			// give the unauthenticated client 10 seconds to perform TLS handshake
			clientConn.SetDeadline(time.Now().Add(time.Second * 10))
		} else {

		}

		// read data from client
		bytesRead, err = readFromClient(clientConn, inBuf)
		if err != nil {
			log.Err(err).Msg("error reading from client")
			break
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if connectionState == stateMLATNotAuthenticated {
			clientApiKey, _, _, mux, label, err = authenticateFeeder(ctx, clientConn, log)
			if err != nil {
				log.Err(err)
				break
			}

			// update logger
			log = log.With().Str("uuid", clientApiKey.String()).Str("mux", mux).Str("label", label).Logger()

			// check number of connections, and drop connection if limit exceeded
			if stats.getNumConnections(clientApiKey, protoMLAT) > maxConnectionsPerProto {
				log.Warn().Int("connections", stats.Feeders[clientApiKey].Connections[protoMLAT].ConnectionCount).Int("max", maxConnectionsPerProto).Msg("dropping connection as limit of connections exceeded")
				break
			}

			// update state
			connectionState = stateMLATAuthenticated
			lastAuthCheck = time.Now()
		}

		// If the client has been authenticated, then we can do stuff with the data
		if connectionState == stateMLATAuthenticated {

			// update log context
			log.With().Str("dst", fmt.Sprintf("%s:12346", mux))

			// attempt to connect to the mux container
			muxConn, muxConnErr = dialContainerTCP(mux, 12346)
			if muxConnErr != nil {

				// handle connection errors to feed-in container

				log.Warn().AnErr("error", muxConnErr).Msg("error connecting to mux container")
				time.Sleep(1 * time.Second)

				err := clientConn.Close()
				if err != nil {
					log.Err(err).Caller().Msg("error closing clientConn")
				}
				break

			} else {

				// connected OK...
				defer muxConn.Close()

				// attempt to set tcp keepalive with 1 sec interval
				err := muxConn.SetKeepAlive(true)
				if err != nil {
					log.Err(err).Msg("error setting keep alive")
					e := clientConn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("error closing client connection")
					}
					break
				}
				err = muxConn.SetKeepAlivePeriod(1 * time.Second)
				if err != nil {
					log.Err(err).Msg("error setting keep alive period")
					e := clientConn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("error closing client connection")
					}
					break
				}

				// update state
				connectionState = stateMLATMuxContainerConnected

				log.Info().Msg("connected to mux")

			}
		}
		// if we are ready to output data to the feed-in container...
		if connectionState == stateMLATMuxContainerConnected {
			break
		}
	}

	// if we are ready to output data to the feed-in container...
	if connectionState == stateMLATMuxContainerConnected {

		// write outstanding data
		_, err := muxConn.Write(inBuf[:bytesRead])
		if err != nil {
			log.Err(err).Msg("error writing to client")
		}

		// update stats
		stats.addConnection(clientApiKey, clientConn.RemoteAddr(), muxConn.RemoteAddr(), protoMLAT, connNum)
		defer stats.delConnection(clientApiKey, protoMLAT, connNum)

		// prep waitgroup to hold function execution until all goroutines finish
		wg := sync.WaitGroup{}

		// method to signal goroutines to exit
		runProxy := true
		var runProxyMu sync.RWMutex

		// handle data from feeder client to mlat server
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, sendRecvBufferSize)
			for {

				// quit if directed by other goroutine
				runProxyMu.RLock()
				if !runProxy {
					runProxyMu.RUnlock()
					break
				}
				runProxyMu.RUnlock()

				// read from feeder client
				err := clientConn.SetReadDeadline(time.Now().Add(time.Second * 2))
				if err != nil {
					log.Err(err).Msg("error setting read deadline on clientConn")
					break
				}
				bytesRead, err := clientConn.Read(buf)
				if err != nil {
					if !errors.Is(err, os.ErrDeadlineExceeded) {
						log.Err(err).Msg("error reading from client")
						break
					}
				} else {

					// write to mlat server
					err := muxConn.SetWriteDeadline(time.Now().Add(time.Second * 2))
					if err != nil {
						log.Err(err).Msg("error setting write deadline on muxConn")
						break
					}
					_, err = muxConn.Write(buf[:bytesRead])
					if err != nil {
						log.Err(err).Msg("error writing to mux")
						break
					}

					// update stats
					stats.incrementByteCounters(clientApiKey, connNum, uint64(bytesRead), 0)
				}

				// check feeder is still valid (every 60 secs)
				if time.Now().After(lastAuthCheck.Add(time.Second * 60)) {
					if !isValidApiKey(clientApiKey) {
						log.Warn().Msg("disconnecting feeder as uuid is no longer valid")
						break
					}
					lastAuthCheck = time.Now()
				}
			}

			// tell other goroutine to exit
			runProxyMu.Lock()
			defer runProxyMu.Unlock()
			runProxy = false
		}()

		// handle data from mlat server to feeder client
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, sendRecvBufferSize)
			for {

				// quit if directed by other goroutine
				runProxyMu.RLock()
				if !runProxy {
					runProxyMu.RUnlock()
					break
				}
				runProxyMu.RUnlock()

				// read from mlat server
				err := muxConn.SetReadDeadline(time.Now().Add(time.Second * 2))
				if err != nil {
					log.Err(err).Msg("error setting read deadline on muxConn")
					break
				}
				bytesRead, err := muxConn.Read(buf)
				if err != nil {
					if !errors.Is(err, os.ErrDeadlineExceeded) {
						log.Err(err).Msg("error reading from mux")
						break
					}
				} else {

					// write to feeder client
					err := clientConn.SetWriteDeadline(time.Now().Add(time.Second * 2))
					if err != nil {
						log.Err(err).Msg("error setting write deadline on clientConn")
						break
					}
					_, err = clientConn.Write(buf[:bytesRead])
					if err != nil {
						log.Err(err).Msg("error writing to feeder")
						break
					}

					// update stats
					stats.incrementByteCounters(clientApiKey, connNum, 0, uint64(bytesRead))
				}
			}

			// tell other goroutine to exit
			runProxyMu.Lock()
			defer runProxyMu.Unlock()
			runProxy = false
		}()

		// wait for goroutines to finish
		wg.Wait()

	}
	log.Debug().Msg("finished")
}

func clientBEASTConnection(ctx *cli.Context, connIn net.Conn, containersToStart chan startContainerRequest, connNum uint) {
	// handles incoming BEAST connections

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "clientBEASTConnection"}).
		Str("listener", protoBeast).
		Uint("connNum", connNum).
		Logger()

	log.Debug().Msg("started")

	defer connIn.Close()

	var (
		connectionState    = stateBeastNotAuthenticated
		sendRecvBufferSize = 256 * 1024 // 256kB
		connOut            *net.TCPConn
		connOutErr         error
		clientApiKey       uuid.UUID
		refLat, refLon     float64
		mux, label         string
		lastAuthCheck      time.Time
		err                error
		wg                 sync.WaitGroup
	)

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(connIn.RemoteAddr().String(), ":")[0])
	log = log.With().IPAddr("src", remoteIP).Logger()

	// check for too-frequent incoming connections
	err = incomingConnTracker.check(remoteIP, connNum)
	if err != nil {
		log.Err(err).Msg("dropping connection")
		return
	}

	buf := make([]byte, sendRecvBufferSize)
	for {

		if connectionState == stateBeastNotAuthenticated {
			// give the unauthenticated client 10 seconds to perform TLS handshake
			connIn.SetDeadline(time.Now().Add(time.Second * 10))
		}

		// read data from client
		bytesRead, err := readFromClient(connIn, buf)
		if os.IsTimeout(err) {
			break // suppress constant i/o timeout messages
		} else if err != nil {
			log.Err(err).Msg("error reading from client")
			connectionState = stateBeastCloseConnection
			break
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if connectionState == stateBeastNotAuthenticated {
			clientApiKey, refLat, refLon, mux, label, err = authenticateFeeder(ctx, connIn, log)
			if err != nil {
				log.Err(err)
				connectionState = stateBeastCloseConnection
				break
			}
			lastAuthCheck = time.Now()

			// update logger
			log = log.With().Str("uuid", clientApiKey.String()).Str("mux", mux).Str("label", label).Logger()

			// check number of connections, and drop connection if limit exceeded
			if stats.getNumConnections(clientApiKey, protoBeast) > maxConnectionsPerProto {
				log.Warn().Int("connections", stats.Feeders[clientApiKey].Connections[protoBeast].ConnectionCount).Int("max", maxConnectionsPerProto).Msg("dropping connection as limit of connections exceeded")
				connectionState = stateBeastCloseConnection
				break
			}

			// start the container
			// used a chan here so it blocks while waiting for the request to be popped off the chan

			containerStartDelay := false
			wg.Add(1)
			containersToStart <- startContainerRequest{
				uuid:                clientApiKey,
				refLat:              refLat,
				refLon:              refLon,
				mux:                 mux,
				label:               label,
				srcIP:               remoteIP,
				wg:                  &wg,
				containerStartDelay: &containerStartDelay,
			}

			// wait for request to be actioned
			wg.Wait()

			// wait for container start if needed
			if containerStartDelay {
				log.Debug().Msg("waiting for feed-in container to start")
				time.Sleep(5 * time.Second)
			}

			// update state
			connectionState = stateBeastAuthenticated
		}

		// If the client has been authenticated, then we can do stuff with the data
		if connectionState == stateBeastAuthenticated {

			log = log.With().Str("dst", fmt.Sprintf("feed-in-%s", clientApiKey.String())).Logger()
			connOut, connOutErr = dialContainerTCP(fmt.Sprintf("feed-in-%s", clientApiKey.String()), 12345)
			if connOutErr != nil {

				// handle connection errors to feed-in container
				log.Warn().AnErr("error", connOutErr).Msg("error connecting to feed-in container")
				time.Sleep(1 * time.Second)
				connectionState = stateBeastCloseConnection
				break

			} else {

				// connected OK...

				defer connOut.Close()

				// attempt to set tcp keepalive with 1 sec interval
				err := connOut.SetKeepAlive(true)
				if err != nil {
					log.Err(err).Msg("error setting keep alive")
					connectionState = stateBeastCloseConnection
					break
				}
				err = connOut.SetKeepAlivePeriod(1 * time.Second)
				if err != nil {
					log.Err(err).Msg("error setting keep alive period")
					connectionState = stateBeastCloseConnection
					break
				}

				log.Info().Msg("connected to feed-in")

				// update state
				connectionState = stateBeastFeedInContainerConnected

				// update stats
				stats.addConnection(clientApiKey, connIn.RemoteAddr(), connOut.RemoteAddr(), protoBeast, connNum)
				defer stats.delConnection(clientApiKey, protoBeast, connNum)

				// reset deadline
				connIn.SetDeadline(time.Time{})

			}
		}

		// if we are ready to output data to the feed-in container...
		if connectionState == stateBeastFeedInContainerConnected {

			// if we have data to write...
			if bytesRead > 0 {

				// set deadline of 5 second
				wdErr := connOut.SetDeadline(time.Now().Add(5 * time.Second))
				if wdErr != nil {
					log.Err(wdErr).Msg("error setting deadline on connection")
					connectionState = stateBeastCloseConnection
					break
				}

				// attempt to write data in buf (that was read from client connection earlier)
				_, err := connOut.Write(buf[:bytesRead])
				if err != nil {
					log.Err(err).Msg("error writing to feed-in container")
					connectionState = stateBeastCloseConnection
					break
				}

				// update stats
				stats.incrementByteCounters(clientApiKey, connNum, uint64(bytesRead), 0)
			}
		}

		// check feeder is still valid (every 60 secs)
		if time.Now().After(lastAuthCheck.Add(time.Second * 60)) {
			if !isValidApiKey(clientApiKey) {
				log.Warn().Msg("disconnecting feeder as uuid is no longer valid")
				connectionState = stateBeastCloseConnection
				break
			}
			lastAuthCheck = time.Now()
		}
	}
	log.Debug().Msg("finished")
}
