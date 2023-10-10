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

	// struct to allocate connection numbers
	connectionNumber struct {
		mu  sync.RWMutex
		num uint
	}

	// structs to hold incoming connection data
	incomingConnectionTracker struct {
		mu          sync.RWMutex
		connections []incomingConnection
	}
	incomingConnection struct {
		srcIP    net.IP
		connTime time.Time
	}
)

const (
	// limits maximum number of connections per feeder, per protocol to this many:
	maxConnectionsPerProto = 2

	// limits connection attempts to
	//   "maxIncomingConnectionRequestsPerProto" (2) attempts in a
	//   "maxIncomingConnectionRequestSeconds" (10) second period
	maxIncomingConnectionRequestsPerProto = 2
	maxIncomingConnectionRequestSeconds   = 10
)

const (
	stateBeastNotAuthenticated stateBeast = iota
	stateBeastAuthenticated
	stateBeastFeedInContainerConnected
)

const (
	stateMLATNotAuthenticated stateMLAT = iota
	stateMLATAuthenticated
	stateMLATMuxContainerConnected
)

func (connNum *connectionNumber) GetNum() (num uint) {
	connNum.mu.Lock()
	defer connNum.mu.Unlock()
	connNum.num++
	if connNum.num == 0 {
		connNum.num++
	}
	return connNum.num
}

func (t *incomingConnectionTracker) evict() {
	// evicts connections from the tracker if older than 10 seconds

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
}

func (t *incomingConnectionTracker) check(srcIP net.IP) (err error) {
	// checks an incoming connection
	// allows 'maxIncomingConnectionRequestsPerProto' connections every 10 seconds

	var connCount uint

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
		})
		t.mu.Unlock()
	}

	return err
}

func dialContainerTCP(container string, port int) (c *net.TCPConn, err error) {

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

	// prep address to connect to
	dstTCPAddr := net.TCPAddr{
		IP:   dstIP,
		Port: port,
	}

	// dial feed-in container
	c, err = net.DialTCP("tcp", nil, &dstTCPAddr)

	return c, err

}

func authenticateFeeder(ctx *cli.Context, connIn net.Conn, log zerolog.Logger) (clientApiKey uuid.UUID, refLat, refLon float64, mux, label string, err error) {
	// authenticates a feeder

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

	cLog := log.With().Str("listener", protoMLAT).Uint("conn#", connNum).Logger()

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
	cLog = cLog.With().IPAddr("src", remoteIP).Logger()

	// check for too-frequent incoming connections
	err = incomingConnTracker.check(remoteIP)
	if err != nil {
		cLog.Err(err).Msg("dropping connection")
		return
	}

	// make buffer to hold data read from client
	inBuf := make([]byte, sendRecvBufferSize)

	for {

		if connectionState == stateMLATNotAuthenticated {
			// give the unauthenticated client 10 seconds to perform TLS handshake
			clientConn.SetDeadline(time.Now().Add(time.Second * 10))
		}

		// read data from client
		bytesRead, err = readFromClient(clientConn, inBuf)
		if err != nil {
			cLog.Err(err).Msg("could not read from client")
			break
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if connectionState == stateMLATNotAuthenticated {
			clientApiKey, _, _, mux, label, err = authenticateFeeder(ctx, clientConn, cLog)
			if err != nil {
				cLog.Err(err)
				break
			}

			// update logger
			cLog = cLog.With().Str("uuid", clientApiKey.String()).Str("mux", mux).Str("label", label).Logger()

			// check number of connections, and drop connection if limit exceeded
			if stats.getNumConnections(clientApiKey, protoMLAT) > maxConnectionsPerProto {
				cLog.Warn().Int("connections", stats.Feeders[clientApiKey].Connections[protoMLAT].ConnectionCount).Int("max", maxConnectionsPerProto).Msg("dropping connection as limit of connections exceeded")
				break
			}

			// update state
			connectionState = stateMLATAuthenticated
			lastAuthCheck = time.Now()
		}

		// If the client has been authenticated, then we can do stuff with the data
		if connectionState == stateMLATAuthenticated {

			// update log context
			cLog.With().Str("dst", fmt.Sprintf("%s:12346", mux))

			// attempt to connect to the mux container
			muxConn, muxConnErr = dialContainerTCP(mux, 12346)
			if muxConnErr != nil {

				// handle connection errors to feed-in container

				cLog.Warn().AnErr("error", muxConnErr).Msg("could not connect to mux container")
				time.Sleep(1 * time.Second)

				e := clientConn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close clientConn")
				}
				break

			} else {

				// connected OK...
				defer muxConn.Close()

				// attempt to set tcp keepalive with 1 sec interval
				err := muxConn.SetKeepAlive(true)
				if err != nil {
					cLog.Err(err).Msg("could not set keep alive")
					e := clientConn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("could not close clientConn")
					}
					break
				}
				err = muxConn.SetKeepAlivePeriod(1 * time.Second)
				if err != nil {
					cLog.Err(err).Msg("could not set keep alive period")
					e := clientConn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("could not close clientConn")
					}
					break
				}

				// update state
				connectionState = stateMLATMuxContainerConnected

				cLog.Info().Msg("connected to mux")

			}
		}
		// if we are ready to output data to the feed-in container...
		if connectionState == stateMLATMuxContainerConnected {
			break
		}
	}

	// if we are ready to output data to the feed-in container...
	if connectionState == stateMLATMuxContainerConnected {

		// reset deadline
		clientConn.SetDeadline(time.Time{})

		// write outstanding data
		_, err := muxConn.Write(inBuf[:bytesRead])
		if err != nil {
			cLog.Err(err).Msg("error writing to client")
		}

		// update stats
		stats.addConnection(clientApiKey, clientConn.RemoteAddr(), muxConn.RemoteAddr(), protoMLAT, connNum)
		defer stats.delConnection(clientApiKey, connNum)

		wg := sync.WaitGroup{}

		// handle data from feeder client to mlat server
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, sendRecvBufferSize)
			for {

				// read from feeder client
				bytesRead, err := clientConn.Read(buf)
				if err != nil {
					cLog.Err(err).Msg("could not read from client")
					return
				}

				// write to mlat server
				_, err = muxConn.Write(buf[:bytesRead])
				if err != nil {
					cLog.Err(err).Msg("error writing to mux")
					return
				}

				// update stats
				stats.incrementByteCounters(clientApiKey, connNum, uint64(bytesRead), 0)

				// check feeder is still valid (every 60 secs)
				if time.Now().After(lastAuthCheck.Add(time.Second * 60)) {
					if !isValidApiKey(clientApiKey) {
						cLog.Warn().Msg("disconnecting feeder as uuid is no longer valid")
						return
					}
					lastAuthCheck = time.Now()
				}
			}
		}()

		// handle data from mlat server to feeder client
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, sendRecvBufferSize)
			for {

				// read from mlat server
				bytesRead, err := muxConn.Read(buf)
				if err != nil {
					cLog.Err(err).Msg("could not read from mux")
					return
				}

				// write to feeder client
				_, err = clientConn.Write(buf[:bytesRead])
				if err != nil {
					cLog.Err(err).Msg("error writing to feeder")
					return
				}

				// update stats
				stats.incrementByteCounters(clientApiKey, connNum, 0, uint64(bytesRead))
			}
		}()

		// todo: find a way to kill above goroutines if one finishes

		wg.Wait()

	}
}

func clientBEASTConnection(ctx *cli.Context, connIn net.Conn, containersToStart chan startContainerRequest, connNum uint) {
	// handles incoming BEAST connections

	cLog := log.With().Str("listener", protoBeast).Uint("conn#", connNum).Logger()

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
	)

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(connIn.RemoteAddr().String(), ":")[0])
	cLog = cLog.With().IPAddr("src", remoteIP).Logger()

	// check for too-frequent incoming connections
	err = incomingConnTracker.check(remoteIP)
	if err != nil {
		cLog.Err(err).Msg("dropping connection")
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
			cLog.Warn().Err(err).Msg("could not read from client")
			break
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if connectionState == stateBeastNotAuthenticated {
			clientApiKey, refLat, refLon, mux, label, err = authenticateFeeder(ctx, connIn, cLog)
			if err != nil {
				cLog.Err(err)
				break
			}
			lastAuthCheck = time.Now()

			// update logger
			cLog = cLog.With().Str("uuid", clientApiKey.String()).Str("mux", mux).Str("label", label).Logger()

			// check number of connections, and drop connection if limit exceeded
			if stats.getNumConnections(clientApiKey, protoBeast) > maxConnectionsPerProto {
				cLog.Warn().Int("connections", stats.Feeders[clientApiKey].Connections[protoBeast].ConnectionCount).Int("max", maxConnectionsPerProto).Msg("dropping connection as limit of connections exceeded")
				break
			}

			// start the container
			// used a chan here so it blocks while waiting for the request to be popped off the chan
			containersToStart <- startContainerRequest{
				uuid:   clientApiKey,
				refLat: refLat,
				refLon: refLon,
				mux:    mux,
				label:  label,
				srcIP:  remoteIP,
			}

			// wait for container start
			time.Sleep(5 * time.Second)

			// update state
			connectionState = stateBeastAuthenticated
		}

		// If the client has been authenticated, then we can do stuff with the data
		if connectionState == stateBeastAuthenticated {

			cLog = cLog.With().Str("dst", fmt.Sprintf("feed-in-%s", clientApiKey.String())).Logger()
			connOut, connOutErr = dialContainerTCP(fmt.Sprintf("feed-in-%s", clientApiKey.String()), 12345)
			if connOutErr != nil {

				// handle connection errors to feed-in container
				cLog.Warn().AnErr("error", connOutErr).Msg("could not connect to feed-in container")
				time.Sleep(1 * time.Second)

				break

			} else {

				// connected OK...

				defer connOut.Close()

				// attempt to set tcp keepalive with 1 sec interval
				err := connOut.SetKeepAlive(true)
				if err != nil {
					cLog.Err(err).Msg("could not set keep alive")
					break
				}
				err = connOut.SetKeepAlivePeriod(1 * time.Second)
				if err != nil {
					cLog.Err(err).Msg("could not set keep alive period")
					break
				}

				cLog.Info().Msg("connected to feed-in")

				// update state
				connectionState = stateBeastFeedInContainerConnected

				// update stats
				stats.addConnection(clientApiKey, connIn.RemoteAddr(), connOut.RemoteAddr(), protoBeast, connNum)
				defer stats.delConnection(clientApiKey, connNum)

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
					cLog.Err(wdErr).Msg("could not set deadline on connection")
					break
				}

				// attempt to write data in buf (that was read from client connection earlier)
				_, err := connOut.Write(buf[:bytesRead])
				if err != nil {
					cLog.Err(err).Msg("error writing to feed-in container")
					break
				}

				// update stats
				stats.incrementByteCounters(clientApiKey, connNum, uint64(bytesRead), 0)
			}
		}

		// check feeder is still valid (every 60 secs)
		if time.Now().After(lastAuthCheck.Add(time.Second * 60)) {
			if !isValidApiKey(clientApiKey) {
				cLog.Warn().Msg("disconnecting feeder as uuid is no longer valid")
				break
			}
			lastAuthCheck = time.Now()
		}
	}
}
