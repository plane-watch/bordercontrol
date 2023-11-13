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

	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
)

type (
	// for connection state machines, see consts below
	stateBeast int64
	stateMLAT  int64

	// structs to hold incoming connection data
	incomingConnectionTracker struct {
		mu               sync.RWMutex
		connections      []incomingConnection
		connectionNumber uint // to allocate connection numbers
	}
	incomingConnection struct { // used to limit the number of connections over time from a single source IP
		srcIP    net.IP
		connTime time.Time
		connNum  uint
	}

	// struct to hold feeder client information
	feederClient struct {
		clientApiKey   uuid.UUID
		refLat, refLon float64
		mux, label     string
	}
)

const (
	// limits maximum number of connections per feeder, per protocol to this many:
	maxConnectionsPerProto = 1

	// limits connection attempts to
	//   "maxIncomingConnectionRequestsPerSrcIP" (2) attempts in a
	//   "maxIncomingConnectionRequestSeconds" (10) second period
	maxIncomingConnectionRequestsPerSrcIP = 3
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

func (t *incomingConnectionTracker) getNum() (num uint) {
	// return a non-duplicate connection number

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "getNum"}).
		Logger()

	log.Trace().Msg("started")

	var dupe bool

	t.mu.Lock()
	defer t.mu.Unlock()

	for {

		// determine next available connection number
		t.connectionNumber++
		if t.connectionNumber == 0 {
			t.connectionNumber++
		}

		log.Trace().
			Uint("connectionNumber", t.connectionNumber).
			Msg("checking for duplicate connection number")

		// is the connection number already in use
		dupe = false
		for _, c := range t.connections {
			if t.connectionNumber == c.connNum {
				dupe = true
				log.Trace().
					Uint("connectionNumber", t.connectionNumber).
					Msg("duplicate connection number!")
				break
			}
		}

		// if not a duplicate, break out of loop
		if !dupe {
			break
		}
	}

	log.Trace().
		Uint("connectionNumber", t.connectionNumber).
		Msg("finished")

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
	evictedConnections := 0
	for _, c := range t.connections {

		// keep connections tracked if less than maxIncomingConnectionRequestSeconds seconds old
		if !c.connTime.Add(time.Second * maxIncomingConnectionRequestSeconds).Before(time.Now()) {
			t.connections[i] = c
			i++
		} else {
			evictedConnections++
		}
	}
	t.connections = t.connections[:i]

	log.Trace().
		Int("active_connections", i).
		Int("evicted_connections", evictedConnections).
		Msg("finished")
}

func (t *incomingConnectionTracker) check(srcIP net.IP, connNum uint) (err error) {
	// checks an incoming connection
	// allows 'maxIncomingConnectionRequestsPerProto' connections every 10 seconds

	var connCount uint

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "check"}).
		IPAddr("srcIP", srcIP).
		Uint("connNum", connNum).
		Logger()

	log.Trace().Msg("started")

	// count number of connections from this source IP
	t.mu.RLock()
	for _, c := range t.connections {
		if c.srcIP.Equal(srcIP) {
			connCount++
		}
	}
	t.mu.RUnlock()
	log.Trace().
		Uint("connCount", connCount).
		Msg("connections from this source")

	if connCount >= maxIncomingConnectionRequestsPerSrcIP {
		// if connecting too frequently, raise an error
		err = errors.New(fmt.Sprintf("more than %d connections from src within a %d second period",
			maxIncomingConnectionRequestsPerSrcIP,
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
	log.Trace().Uint("connCount", connCount).AnErr("err", err).Msg("finished")

	return err
}

func lookupContainerTCP(container string, port int) (n *net.TCPAddr, err error) {

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "lookupContainerTCP"}).
		Str("container", container).
		Int("port", port).
		Logger()

	log.Trace().Msg("started")

	// perform DNS lookup
	var dstIP net.IP
	dstIPs, err := net.LookupIP(container)
	if err != nil {
		// error performing lookup
		log.Trace().AnErr("err", err).Msg("error performing net.LookupIP")
	} else {
		// if no error

		if len(dstIPs) > 0 {
			// if dstIPs contains at least one element

			// take the first returned address
			dstIP = dstIPs[0]

			// update logger with IP address
			log = log.With().IPAddr("ip", dstIP).Logger()

			// prep address to connect to
			n = &net.TCPAddr{
				IP:   dstIP,
				Port: port,
			}

		} else {
			// if dstIPs contains no elements

			err = errors.New("container DNS lookup returned no IPs")
			log.Trace().AnErr("err", err).Msg("no elements in dstIPs")
		}
	}

	log.Trace().Msg("finished")
	return n, err
}

func dialContainerTCP(container string, port int) (c *net.TCPConn, err error) {

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "dialContainerTCP"}).
		Str("container", container).
		Int("port", port).
		Logger()

	log.Trace().Msg("started")

	// lookup container IP, return TCP address
	dstTCPAddr, err := lookupContainerTCP(container, port)

	// update logger
	log = log.With().IPAddr("dstTCPAddr", dstTCPAddr.IP).Int("port", port).Logger()

	// dial feed-in container
	log.Trace().Msg("performing DialTCP to IP")
	c, err = net.DialTCP("tcp", nil, dstTCPAddr)
	if err != nil {
		log = log.With().AnErr("err", err).Logger()
	}

	log.Trace().Msg("finished")
	return c, err
}

func checkConnTLSHandshakeComplete(c net.Conn) bool {
	return c.(*tls.Conn).ConnectionState().HandshakeComplete
}

func getUUIDfromSNI(c net.Conn) (u uuid.UUID, err error) {
	return uuid.Parse(c.(*tls.Conn).ConnectionState().ServerName)
}

func authenticateFeeder(connIn net.Conn) (clientDetails *feederClient, err error) {
	// authenticates a feeder

	clientDetails = &feederClient{}

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "authenticateFeeder"}).
		Logger()

	// check TLS handshake
	if !checkConnTLSHandshakeComplete(connIn) {
		// if TLS handshake is not complete, then kill the connection
		err := errors.New("data received before tls handshake")
		return clientDetails, err
	}
	log = log.With().Bool("TLSHandshakeComplete", true).Logger()
	log.Trace().Msg("TLS handshake complete")

	// check valid uuid was returned as ServerName (sni)
	clientDetails.clientApiKey, err = getUUIDfromSNI(connIn)
	if err != nil {
		return clientDetails, err
	}
	log = log.With().Str("uuid", clientDetails.clientApiKey.String()).Logger()
	log.Trace().Msg("feeder API key received from SNI")

	// check valid api key against atc
	if !isValidApiKey(clientDetails.clientApiKey) {
		// if API is not valid, then kill the connection
		err := errors.New("client sent invalid api key")
		return clientDetails, err
	}
	log.Trace().Msg("feeder API key valid")

	// get feeder info (lat/lon/mux/label) from atc cache
	err = getFeederInfo(clientDetails)

	// update stats
	stats.setFeederDetails(clientDetails)

	return clientDetails, err
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

func clientMLATConnection(ctx *cli.Context, clientConn net.Conn, tlsConfig *tls.Config, connNum uint) error {
	// handles incoming MLAT connections

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "clientMLATConnection"}).
		Str("listener", protoMLAT).
		Uint("connNum", connNum).
		Logger()

	defer clientConn.Close()

	var (
		connectionState    = stateMLATNotAuthenticated
		sendRecvBufferSize = 256 * 1024 // 256kB
		muxConn            *net.TCPConn
		muxConnErr         error
		clientDetails      *feederClient
		bytesRead          int
		err                error
		lastAuthCheck      time.Time
	)

	log = log.With().Any("connectionState", connectionState).Logger()
	log.Trace().Msg("started")

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

	// give the unauthenticated client 10 seconds to perform TLS handshake
	clientConn.SetDeadline(time.Now().Add(time.Second * 10))

	// read data from client
	bytesRead, err = readFromClient(clientConn, inBuf)
	if err != nil {
		log.Err(err).Msg("error reading from client")
		break MLATOuterLoop
	}

	// When the first data is sent, the TLS handshake should take place.
	// Accordingly, we need to track the state...
	clientDetails, err = authenticateFeeder(clientConn)
	if err != nil {
		log.Err(err).Msg("error authenticating feeder")
		return err
	}

	// update logger
	log = log.With().
		Str("uuid", clientDetails.clientApiKey.String()).
		Str("mux", clientDetails.mux).
		Str("label", clientDetails.label).
		Logger()

	// check number of connections, and drop connection if limit exceeded
	if stats.getNumConnections(clientDetails.clientApiKey, protoMLAT) > maxConnectionsPerProto {
		err := errors.New("connection limit exceeded")
		log.Err(err).
			Int("connections", stats.Feeders[clientDetails.clientApiKey].Connections[protoMLAT].ConnectionCount).
			Int("max", maxConnectionsPerProto).
			Msg("dropping connection")
		return err
	}

	// update state
	lastAuthCheck = time.Now()

	// If the client has been authenticated, then we can do stuff with the data

	// update log context
	log.With().Str("dst", fmt.Sprintf("%s:12346", clientDetails.mux))

	// attempt to connect to the mux container
	muxConn, err = dialContainerTCP(clientDetails.mux, 12346)
	if err != nil {
		// handle connection errors to mux container
		log.Err(err).Msg("error connecting to mux container")
		return err
	}
	defer muxConn.Close()

	// attempt to set tcp keepalive with 1 sec interval
	err = muxConn.SetKeepAlive(true)
	if err != nil {
		log.Err(err).Msg("error setting keep alive")
		return err
	}
	err = muxConn.SetKeepAlivePeriod(1 * time.Second)
	if err != nil {
		log.Err(err).Msg("error setting keep alive period")
		return err
	}

	log.Info().Msg("client connected to mux")

	// write any outstanding data
	_, err = muxConn.Write(inBuf[:bytesRead])
	if err != nil {
		log.Err(err).Msg("error writing to mux")
		return err
	}

	// update stats
	stats.addConnection(clientDetails.clientApiKey, clientConn.RemoteAddr(), muxConn.RemoteAddr(), protoMLAT, connNum)
	defer stats.delConnection(clientDetails.clientApiKey, protoMLAT, connNum)

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
				stats.incrementByteCounters(clientDetails.clientApiKey, connNum, uint64(bytesRead), 0)
			}

			// check feeder is still valid (every 60 secs)
			if time.Now().After(lastAuthCheck.Add(time.Second * 60)) {
				if !isValidApiKey(clientDetails.clientApiKey) {
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
				stats.incrementByteCounters(clientDetails.clientApiKey, connNum, 0, uint64(bytesRead))
			}
		}

		// tell other goroutine to exit
		runProxyMu.Lock()
		defer runProxyMu.Unlock()
		runProxy = false
	}()

	// wait for goroutines to finish
	wg.Wait()

	log.Trace().Msg("finished")

	return nil
}

func clientBEASTConnection(ctx *cli.Context, connIn net.Conn, containersToStart chan startContainerRequest, connNum uint) {
	// handles incoming BEAST connections

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "clientBEASTConnection"}).
		Str("listener", protoBeast).
		Uint("connNum", connNum).
		Logger()

	defer connIn.Close()

	var (
		connectionState    = stateBeastNotAuthenticated
		sendRecvBufferSize = 256 * 1024 // 256kB
		connOut            *net.TCPConn
		connOutErr         error
		clientDetails      *feederClient
		lastAuthCheck      time.Time
		err                error
		wg                 sync.WaitGroup
	)

	log = log.With().Any("connectionState", connectionState).Logger()
	log.Trace().Msg("started")

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
			log = log.With().Any("connectionState", connectionState).Logger()
			break
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if connectionState == stateBeastNotAuthenticated {
			clientDetails, err = authenticateFeeder(ctx, connIn)
			if err != nil {
				log.Err(err)
				connectionState = stateBeastCloseConnection
				log = log.With().Any("connectionState", connectionState).Logger()
				break
			}
			lastAuthCheck = time.Now()

			// update logger
			log = log.With().
				Str("uuid", clientDetails.clientApiKey.String()).
				Str("mux", clientDetails.mux).
				Str("label", clientDetails.label).
				Logger()

			// check number of connections, and drop connection if limit exceeded
			if stats.getNumConnections(clientDetails.clientApiKey, protoBeast) > maxConnectionsPerProto {
				log.Warn().
					Int("connections", stats.Feeders[clientDetails.clientApiKey].Connections[protoBeast].ConnectionCount).
					Int("max", maxConnectionsPerProto).
					Msg("dropping connection as limit of connections exceeded")
				connectionState = stateBeastCloseConnection
				log = log.With().Any("connectionState", connectionState).Logger()
				break
			}

			// start the container
			// used a chan here so it blocks while waiting for the request to be popped off the chan

			containerStartDelay := false
			wg.Add(1)
			containersToStart <- startContainerRequest{
				clientDetails:       clientDetails,
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
			log = log.With().Any("connectionState", connectionState).Logger()
		}

		// If the client has been authenticated, then we can do stuff with the data
		if connectionState == stateBeastAuthenticated {

			log = log.With().Str("dst", fmt.Sprintf("feed-in-%s", clientDetails.clientApiKey.String())).Logger()
			connOut, connOutErr = dialContainerTCP(fmt.Sprintf("feed-in-%s", clientDetails.clientApiKey.String()), 12345)
			if connOutErr != nil {

				// handle connection errors to feed-in container
				log.Warn().AnErr("error", connOutErr).Msg("error connecting to feed-in container")
				time.Sleep(1 * time.Second)
				connectionState = stateBeastCloseConnection
				log = log.With().Any("connectionState", connectionState).Logger()
				break

			} else {

				// connected OK...

				defer connOut.Close()

				// attempt to set tcp keepalive with 1 sec interval
				err := connOut.SetKeepAlive(true)
				if err != nil {
					log.Err(err).Msg("error setting keep alive")
					connectionState = stateBeastCloseConnection
					log = log.With().Any("connectionState", connectionState).Logger()
					break
				}
				err = connOut.SetKeepAlivePeriod(1 * time.Second)
				if err != nil {
					log.Err(err).Msg("error setting keep alive period")
					connectionState = stateBeastCloseConnection
					log = log.With().Any("connectionState", connectionState).Logger()
					break
				}

				// update state
				connectionState = stateBeastFeedInContainerConnected
				log = log.With().Any("connectionState", connectionState).Logger()
				log.Info().Msg("client connected to feed-in container")

				// update stats
				stats.addConnection(clientDetails.clientApiKey, connIn.RemoteAddr(), connOut.RemoteAddr(), protoBeast, connNum)
				defer stats.delConnection(clientDetails.clientApiKey, protoBeast, connNum)

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
					log = log.With().Any("connectionState", connectionState).Logger()
					break
				}

				// attempt to write data in buf (that was read from client connection earlier)
				_, err := connOut.Write(buf[:bytesRead])
				if err != nil {
					log.Err(err).Msg("error writing to feed-in container")
					connectionState = stateBeastCloseConnection
					log = log.With().Any("connectionState", connectionState).Logger()
					break
				}

				// update stats
				stats.incrementByteCounters(clientDetails.clientApiKey, connNum, uint64(bytesRead), 0)
			}
		}

		// check feeder is still valid (every 60 secs)
		if time.Now().After(lastAuthCheck.Add(time.Second * 60)) {
			if !isValidApiKey(clientDetails.clientApiKey) {
				log.Warn().Msg("disconnecting feeder as uuid is no longer valid")
				connectionState = stateBeastCloseConnection
				log = log.With().Any("connectionState", connectionState).Logger()
				break
			}
			lastAuthCheck = time.Now()
		}
	}
	log.Trace().Msg("finished")
}
