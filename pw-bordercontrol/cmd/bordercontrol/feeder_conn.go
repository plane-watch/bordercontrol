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
)

type (

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
		clientApiKey           uuid.UUID
		refLat, refLon         float64
		mux, label, feederCode string
	}

	// struct for proxy goroutines
	proxyStatus struct {
		mu  sync.RWMutex
		run bool
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

	// network send/recv buffer size
	sendRecvBufferSize = 256 * 1024 // 256kB
)

func (t *incomingConnectionTracker) getNum() (num uint) {
	// return a non-duplicate connection number

	// log := log.With().
	// 	Strs("func", []string{"feeder_conn.go", "getNum"}).
	// 	Logger()

	// log.Trace().Msg("started")

	var dupe bool

	t.mu.Lock()
	defer t.mu.Unlock()

	for {

		// determine next available connection number
		t.connectionNumber++
		if t.connectionNumber == 0 {
			t.connectionNumber++
		}

		// log.Trace().
		// 	Uint("connectionNumber", t.connectionNumber).
		// 	Msg("checking for duplicate connection number")

		// is the connection number already in use
		dupe = false
		for _, c := range t.connections {
			if t.connectionNumber == c.connNum {
				dupe = true
				// log.Trace().
				// 	Uint("connectionNumber", t.connectionNumber).
				// 	Msg("duplicate connection number!")
				break
			}
		}

		// if not a duplicate, break out of loop
		if !dupe {
			break
		}
	}

	// log.Trace().
	// 	Uint("connectionNumber", t.connectionNumber).
	// 	Msg("finished")

	return t.connectionNumber
}

func (t *incomingConnectionTracker) evict() {
	// evicts connections from the tracker if older than 10 seconds

	// log := log.With().
	// 	Strs("func", []string{"feeder_conn.go", "evict"}).
	// 	Logger()

	// log.Trace().Msg("started")

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

	// log.Trace().
	// 	Int("active_connections", i).
	// 	Int("evicted_connections", evictedConnections).
	// 	Msg("finished")
}

func (t *incomingConnectionTracker) check(srcIP net.IP, connNum uint) (err error) {
	// checks an incoming connection
	// allows 'maxIncomingConnectionRequestsPerProto' connections every 10 seconds

	var connCount uint

	// log := log.With().
	// 	Strs("func", []string{"feeder_conn.go", "check"}).
	// 	IPAddr("srcIP", srcIP).
	// 	Uint("connNum", connNum).
	// 	Logger()

	// log.Trace().Msg("started")

	// count number of connections from this source IP
	t.mu.RLock()
	for _, c := range t.connections {
		if c.srcIP.Equal(srcIP) {
			connCount++
		}
	}
	t.mu.RUnlock()
	// log.Trace().
	// 	Uint("connCount", connCount).
	// 	Msg("connections from this source")

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
	// log.Trace().Uint("connCount", connCount).AnErr("err", err).Msg("finished")

	return err
}

func lookupContainerTCP(container string, port int) (n *net.TCPAddr, err error) {

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "lookupContainerTCP"}).
		Str("container", container).
		Int("port", port).
		Logger()

	// log.Trace().Msg("started")

	// perform DNS lookup
	var dstIP net.IP
	dstIPs, err := net.LookupIP(container)
	if err != nil {
		// error performing lookup
		// log.Trace().AnErr("err", err).Msg("error performing net.LookupIP")
	} else {
		// if no error

		// if dstIPs contains at least one element

		// look for first IPv4
		found := false
		for _, i := range dstIPs {
			if i.To4() != nil {
				dstIP = i
				found = true
			}
		}
		if !found {
			err = errors.New("container DNS lookup returned no IPv4 addresses")
		}

		// update logger with IP address
		log = log.With().IPAddr("ip", dstIP).Logger()

		// prep address to connect to
		n = &net.TCPAddr{
			IP:   dstIP,
			Port: port,
		}
	}

	// log.Trace().Msg("finished")
	return n, err
}

func dialContainerTCP(container string, port int) (c *net.TCPConn, err error) {

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "dialContainerTCP"}).
		Str("container", container).
		Int("port", port).
		Logger()

	// log.Trace().Msg("started")

	// lookup container IP, return TCP address
	dstTCPAddr, err := lookupContainerTCP(container, port)
	if err != nil {
		return c, err
	}

	// update logger
	log = log.With().Str("dstTCPAddr", dstTCPAddr.IP.String()).Int("port", port).Logger()

	// dial feed-in container
	// log.Trace().Msg("performing DialTCP to IP")
	c, err = net.DialTCP("tcp", nil, dstTCPAddr)
	if err != nil {
		return c, err
	}

	// log.Trace().Msg("finished")
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
	// log.Trace().Msg("TLS handshake complete")

	// check valid uuid was returned as ServerName (sni)
	clientDetails.clientApiKey, err = getUUIDfromSNI(connIn)
	if err != nil {
		return clientDetails, err
	}
	log = log.With().
		Str("uuid", clientDetails.clientApiKey.String()).
		Str("code", clientDetails.feederCode).
		Logger()
	// log.Trace().Msg("feeder API key received from SNI")

	// check valid api key against atc
	if !isValidApiKey(clientDetails.clientApiKey) {
		// if API is not valid, then kill the connection
		err := errors.New("client sent invalid api key")
		return clientDetails, err
	}
	// log.Trace().Msg("feeder API key valid")

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

type protocolProxyConfig struct {
	clientConn    net.Conn       // Client-side connection (feeder out on the internet).
	serverConn    *net.TCPConn   // Server-side connection (feed-in container or mlat server).
	connNum       uint           // Connection number (used for statistics and stuff).
	clientApiKey  uuid.UUID      // Client's API Key (from stunnel SNI).
	pStatus       *proxyStatus   // Proxy status. Provides the ability to tell the proxy to self-terminate.
	lastAuthCheck *time.Time     // Timestamp for when the client's API key was checked for validity (to handle kicked/banned feeders).
	log           zerolog.Logger // Log. This allows the proxy to inherit a logging context.
}

func proxyClientToServer(conf protocolProxyConfig) {
	log := conf.log.With().Str("proxy", "ClientToServer").Logger()
	buf := make([]byte, sendRecvBufferSize)
	for {

		// quit if directed by other goroutine
		conf.pStatus.mu.RLock()
		if !conf.pStatus.run {
			conf.pStatus.mu.RUnlock()
			break
		}
		conf.pStatus.mu.RUnlock()

		// read from feeder client
		err := conf.clientConn.SetReadDeadline(time.Now().Add(time.Second * 2))
		if err != nil {
			log.Err(err).Msg("error setting read deadline on clientConn")
			break
		}
		bytesRead, err := conf.clientConn.Read(buf)
		if err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				log.Err(err).Msg("error reading from client")
				break
			}
		} else {

			// write to server
			err := conf.serverConn.SetWriteDeadline(time.Now().Add(time.Second * 2))
			if err != nil {
				log.Err(err).Msg("error setting write deadline on serverConn")
				break
			}
			_, err = conf.serverConn.Write(buf[:bytesRead])
			if err != nil {
				log.Err(err).Msg("error writing to server")
				break
			}

			// update stats
			stats.incrementByteCounters(conf.clientApiKey, conf.connNum, uint64(bytesRead), 0)
		}

		// check feeder is still valid (every 60 secs)
		if time.Now().After(conf.lastAuthCheck.Add(time.Second * 60)) {
			if !isValidApiKey(conf.clientApiKey) {
				log.Warn().Msg("disconnecting feeder as uuid is no longer valid")
				break
			}
			*conf.lastAuthCheck = time.Now()
		}
	}
}

func proxyServerToClient(conf protocolProxyConfig) {
	log := conf.log.With().Str("proxy", "ServerToClient").Logger()
	buf := make([]byte, sendRecvBufferSize)
	for {

		// quit if directed by other goroutine
		conf.pStatus.mu.RLock()
		if !conf.pStatus.run {
			conf.pStatus.mu.RUnlock()
			break
		}
		conf.pStatus.mu.RUnlock()

		// read from server
		err := conf.serverConn.SetReadDeadline(time.Now().Add(time.Second * 2))
		if err != nil {
			log.Err(err).Msg("error setting read deadline on serverConn")
			break
		}
		bytesRead, err := conf.serverConn.Read(buf)
		if err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				log.Err(err).Msg("error reading from server")
				break
			}
		} else {

			// write to feeder client
			err := conf.clientConn.SetWriteDeadline(time.Now().Add(time.Second * 2))
			if err != nil {
				log.Err(err).Msg("error setting write deadline on clientConn")
				break
			}
			_, err = conf.clientConn.Write(buf[:bytesRead])
			if err != nil {
				log.Err(err).Msg("error writing to feeder")
				break
			}

			// update stats
			stats.incrementByteCounters(conf.clientApiKey, conf.connNum, 0, uint64(bytesRead))
		}
	}
}

type proxyConfig struct {
	connIn                     net.Conn                    // Incoming connection from feeder out on the internet.
	connProto                  feedProtocol                // Incoming connection protocol.
	connNum                    uint                        // Connection number.
	containersToStartRequests  chan startContainerRequest  // Channel to send container start requests.
	containersToStartResponses chan startContainerResponse // Channel to receive container start responses.
}

func proxyClientConnection(conf proxyConfig) error {
	// handles incoming BEAST connections

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "proxyClientConnection"}).
		Str("connProto", string(conf.connProto)).
		Uint("connNum", conf.connNum).
		Logger()

	defer conf.connIn.Close()

	var (
		connOut          *net.TCPConn
		clientDetails    *feederClient
		lastAuthCheck    time.Time
		err              error
		wg               sync.WaitGroup
		dstContainerName string
	)

	// log.Trace().Msg("started")

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(conf.connIn.RemoteAddr().String(), ":")[0])
	log = log.With().IPAddr("src", remoteIP).Logger()

	// check for too-frequent incoming connections
	err = incomingConnTracker.check(remoteIP, conf.connNum)
	if err != nil {
		log.Err(err).Msg("dropping connection")
		return err
	}

	// make buffer to hold data read from client
	buf := make([]byte, sendRecvBufferSize)

	// give the unauthenticated client 10 seconds to perform TLS handshake
	conf.connIn.SetDeadline(time.Now().Add(time.Second * 10))

	// read data from client
	bytesRead, err := readFromClient(conf.connIn, buf)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			// log.Trace().AnErr("err", err).Msg("error reading from client")
			return err
		} else {
			log.Err(err).Msg("error reading from client")
			return err
		}
	}

	// When the first data is sent, the TLS handshake should take place.
	// Accordingly, we need to track the state...
	clientDetails, err = authenticateFeeder(conf.connIn)
	if err != nil {
		log.Err(err).Msg("error authenticating feeder")
		return err
	}

	// update logger
	log = log.With().
		Str("uuid", clientDetails.clientApiKey.String()).
		Str("mux", clientDetails.mux).
		Str("label", clientDetails.label).
		Str("code", clientDetails.feederCode).
		Logger()

	// check number of connections, and drop connection if limit exceeded
	if stats.getNumConnections(clientDetails.clientApiKey, conf.connProto) > maxConnectionsPerProto {
		err := errors.New("connection limit exceeded")
		log.Err(err).
			Int("connections", stats.Feeders[clientDetails.clientApiKey].Connections[protoMLAT].ConnectionCount).
			Int("max", maxConnectionsPerProto).
			Msg("dropping connection")
		return err
	}

	lastAuthCheck = time.Now()

	// If the client has been authenticated, then we can do stuff with the data

	switch conf.connProto {
	case protoBEAST:

		log = log.With().
			Str("dst", fmt.Sprintf("%s%s", feedInContainerPrefix, clientDetails.clientApiKey.String())).
			Logger()

		// request start of the feed-in container with submission timeout
		select {
		case conf.containersToStartRequests <- startContainerRequest{
			clientDetails: clientDetails,
			srcIP:         remoteIP,
		}:
		case <-time.After(5 * time.Second):
			err := errors.New("5s timeout waiting to submit container start request")
			log.Err(err).Msg("could not start feed-in container")
			return err
		}

		// wait for request to be actioned
		startedContainer := <-conf.containersToStartResponses

		// check for start errors
		if startedContainer.err != nil {
			log.Err(startedContainer.err).Msg("error starting feed-in container")
		}

		// wait for container start if needed
		if startedContainer.containerStartDelay {
			log.Debug().Msg("waiting for feed-in container to start")
			time.Sleep(5 * time.Second)
		}

		// connect to feed-in container
		connOut, err = dialContainerTCP(fmt.Sprintf("%s%s", feedInContainerPrefix, clientDetails.clientApiKey.String()), 12345)
		if err != nil {
			// handle connection errors to feed-in container
			log.Err(err).Msg("error connecting to feed-in container")
			return err
		}

		dstContainerName = "feed-in container"

	case protoMLAT:
		// update log context
		log.With().Str("dst", fmt.Sprintf("%s:12346", clientDetails.mux))

		// attempt to connect to the mux container
		connOut, err = dialContainerTCP(clientDetails.mux, 12346)
		if err != nil {
			// handle connection errors to mux container
			log.Err(err).Msg("error connecting to mux container")
			return err
		}

		dstContainerName = "mux container"

	default:
		err := errors.New("unsupported protocol")
		log.Err(err).Msg("unsupported protocol")
		return err
	}

	// connected OK...

	defer connOut.Close()

	// attempt to set tcp keepalive with 1 sec interval
	err = connOut.SetKeepAlive(true)
	if err != nil {
		log.Err(err).Msg("error setting keep alive")
		return err
	}
	err = connOut.SetKeepAlivePeriod(1 * time.Second)
	if err != nil {
		log.Err(err).Msg("error setting keep alive period")
		return err
	}

	log.Info().Msg(fmt.Sprintf("connected to %s", dstContainerName))

	// write any outstanding data
	_, err = connOut.Write(buf[:bytesRead])
	if err != nil {
		log.Err(err).Msg(fmt.Sprintf("error writing to %s", dstContainerName))
		return err
	}

	// update stats
	stats.addConnection(clientDetails.clientApiKey, conf.connIn.RemoteAddr(), connOut.RemoteAddr(), conf.connProto, clientDetails.feederCode, conf.connNum)
	defer stats.delConnection(clientDetails.clientApiKey, conf.connProto, conf.connNum)

	// method to signal goroutines to exit
	pStatus := proxyStatus{
		run: true,
	}

	// prepare proxy config
	protoProxyConf := protocolProxyConfig{
		clientConn:    conf.connIn,
		serverConn:    connOut,
		connNum:       conf.connNum,
		clientApiKey:  clientDetails.clientApiKey,
		pStatus:       &pStatus,
		lastAuthCheck: &lastAuthCheck,
		log:           log,
	}

	// handle data from feeder client to feed-in container
	wg.Add(1)
	go func() {
		defer wg.Done()
		proxyClientToServer(protoProxyConf)

		// tell other goroutine to exit
		pStatus.mu.Lock()
		defer pStatus.mu.Unlock()
		pStatus.run = false
	}()

	// handle data from server container to feeder client (if required, no point doing this for BEAST)
	switch conf.connProto {
	case protoMLAT:
		wg.Add(1)
		go func() {
			defer wg.Done()
			proxyServerToClient(protoProxyConf)

			// tell other goroutine to exit
			pStatus.mu.Lock()
			defer pStatus.mu.Unlock()
			pStatus.run = false
		}()
	}

	// wait for goroutines to finish
	wg.Wait()
	// log.Trace().Msg("finished")
	return nil
}
