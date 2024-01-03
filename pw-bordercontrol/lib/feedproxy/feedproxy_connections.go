package feedproxy

import (
	"errors"
	"fmt"
	"net"
	"os"
	"pw_bordercontrol/lib/containers"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/stats"
	"pw_bordercontrol/lib/stunnel"
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
)

const (
	// limits maximum number of connections per feeder, per protocol to this many:
	maxConnectionsPerProto = 1

	// limits connection attempts to
	maxIncomingConnectionRequestsPerSrcIP = 3
	maxIncomingConnectionRequestSeconds   = 10

	// network send/recv buffer size
	sendRecvBufferSize = 256 * 1024 // 256kB
)

var (
	incomingConnTracker incomingConnectionTracker
)

func GetConnectionNumber() (num uint, err error) {
	// return connection number for new connection
	if !isInitialised() {
		return 0, ErrNotInitialised
	}
	return incomingConnTracker.getNum(), nil
}

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
		if t.connectionNumber == 0 { // don't have a connection with number 0
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

// allow override of these functions to simplify testing
var getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return uuid.Parse(stunnel.GetSNI(c)) }
var handshakeComplete = func(c net.Conn) bool { return stunnel.HandshakeComplete(c) }
var RegisterFeederWithStats = func(f stats.FeederDetails) error { return stats.RegisterFeeder(f) }

func authenticateFeeder(connIn net.Conn) (clientDetails feederClient, err error) {
	// authenticates a feeder

	clientDetails = feederClient{}

	log := log.With().
		Strs("func", []string{"feeder_conn.go", "authenticateFeeder"}).
		Logger()

	// check TLS handshake
	if !handshakeComplete(connIn) {
		// if TLS handshake is not complete, then kill the connection
		err := errors.New("tls handshake incomplete")
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
	err = getFeederInfo(&clientDetails)
	if err != nil {
		return clientDetails, err
	}

	// update stats

	err = RegisterFeederWithStats(stats.FeederDetails{
		Label:      clientDetails.label,
		FeederCode: clientDetails.feederCode,
		ApiKey:     clientDetails.clientApiKey,
	})
	if err != nil {
		return clientDetails, err
	}

	return clientDetails, err
}

func readFromClient(c net.Conn, buf []byte) (n int, err error) {
	// reads data from incoming client connection

	n, err = c.Read(buf)
	if err != nil {
		defer c.Close()
		return n, err
	}
	return n, err
}

type protocolProxyConfig struct {
	clientConn                  net.Conn          // Client-side connection (feeder out on the internet).
	serverConn                  net.Conn          // Server-side connection (feed-in container or mlat server).
	connNum                     uint              // Connection number (used for statistics and stuff).
	clientApiKey                uuid.UUID         // Client's API Key (from stunnel SNI).
	mgmt                        *goRoutineManager // Goroutune manager. Provides the ability to tell the proxy to self-terminate.
	lastAuthCheck               *time.Time        // Timestamp for when the client's API key was checked for validity (to handle kicked/banned feeders).
	log                         zerolog.Logger    // Log. This allows the proxy to inherit a logging context.
	feederValidityCheckInterval time.Duration     // How often to check the feeder is still valid.
}

func proxyClientToServer(conf protocolProxyConfig) {
	log := conf.log.With().Str("proxy", "ClientToServer").Logger()
	buf := make([]byte, sendRecvBufferSize)
	for {

		// quit if directed
		if conf.mgmt.CheckForStop() {
			break
		}

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
			stats.IncrementByteCounters(conf.clientApiKey, conf.connNum, uint64(bytesRead), 0)
		}

		// check feeder is still valid (every 60 secs)
		if time.Now().After(conf.lastAuthCheck.Add(conf.feederValidityCheckInterval)) {
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

		// quit if directed
		if conf.mgmt.CheckForStop() {
			break
		}

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
			stats.IncrementByteCounters(conf.clientApiKey, conf.connNum, 0, uint64(bytesRead))
		}

		// check feeder is still valid (every 60 secs)
		if time.Now().After(conf.lastAuthCheck.Add(conf.feederValidityCheckInterval)) {
			if !isValidApiKey(conf.clientApiKey) {
				log.Warn().Msg("disconnecting feeder as uuid is no longer valid")
				break
			}
			*conf.lastAuthCheck = time.Now()
		}
	}
}

// allow override of these functions to simplify testing
var registerConnectionStats = func(conn stats.Connection) error { return conn.RegisterConnection() }
var unregisterConnectionStats = func(conn stats.Connection) error { return conn.UnregisterConnection() }
var statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
	return stats.GetNumConnections(uuid, proto)
}

type ProxyConnection struct {
	Connection            net.Conn              // Incoming connection from feeder out on the internet
	ConnectionProtocol    feedprotocol.Protocol // Incoming connection protocol
	ConnectionNumber      uint                  // Connection number
	FeedInContainerPrefix string                // feed-in container prefix
	Logger                zerolog.Logger        // logger context to use

	stop   bool
	stopMu sync.RWMutex

	protoProxyConf protocolProxyConfig // config for protocol proxy goroutines
}

func (c *ProxyConnection) Stop() error {
	// Stops proxying incoming connection.

	if !isInitialised() {
		return ErrNotInitialised
	}

	c.stopMu.Lock()
	defer c.stopMu.Unlock()
	c.stop = true
	return nil
}

func (c *ProxyConnection) Start() error {
	// Starts proxying incoming connection. Will create feed-in container if required.

	if !isInitialised() {
		return ErrNotInitialised
	}

	log := c.Logger.With().
		Strs("func", []string{"feeder_conn.go", "proxyClientConnection"}).
		Uint("connNum", c.ConnectionNumber).
		Logger()

	defer c.Connection.Close()

	var (
		connOut          *net.TCPConn
		clientDetails    feederClient
		lastAuthCheck    time.Time
		err              error
		wg               sync.WaitGroup
		dstContainerName string
	)

	// log.Trace().Msg("started")

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(c.Connection.RemoteAddr().String(), ":")[0])
	log = log.With().IPAddr("src", remoteIP).Logger()

	// check for too-frequent incoming connections
	err = incomingConnTracker.check(remoteIP, c.ConnectionNumber)
	if err != nil {
		log.Err(err).Msg("dropping connection")
		return err
	}

	// make buffer to hold data read from client
	buf := make([]byte, sendRecvBufferSize)

	// give the unauthenticated client 10 seconds to perform TLS handshake
	c.Connection.SetDeadline(time.Now().Add(time.Second * 10))

	// read data from client
	bytesRead, err := readFromClient(c.Connection, buf)
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
	clientDetails, err = authenticateFeeder(c.Connection)
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

	numConnections, err := statsGetNumConnections(clientDetails.clientApiKey, c.ConnectionProtocol)
	if err != nil {
		log.Err(err).Msg("error getting number of connections")
		return err
	}

	log = log.With().Str("connections", fmt.Sprintf("%d/%d", numConnections+1, maxConnectionsPerProto)).Logger()

	// check number of connections, and drop connection if limit exceeded
	if numConnections >= maxConnectionsPerProto {
		err := errors.New("connection limit exceeded")
		log.Err(err).Msg("dropping connection")
		return err
	}

	lastAuthCheck = time.Now()

	// If the client has been authenticated, then we can do stuff with the data

	switch c.ConnectionProtocol {
	case feedprotocol.BEAST:

		dstContainerName = "feed-in container"

		log = log.With().
			Str("dst", fmt.Sprintf("%s%s", c.FeedInContainerPrefix, clientDetails.clientApiKey.String())).
			Logger()

		// start feed-in container
		feedInContainer := containers.FeedInContainer{
			Lat:        clientDetails.refLat,
			Lon:        clientDetails.refLon,
			Label:      clientDetails.label,
			ApiKey:     clientDetails.clientApiKey,
			FeederCode: clientDetails.feederCode,
			Addr:       remoteIP,
		}
		_, err := feedInContainer.Start()
		if err != nil {
			log.Err(err).Msg(fmt.Sprintf("could not start %s", dstContainerName))
		}

		// connect to feed-in container
		connOut, err = dialContainerTCP(fmt.Sprintf("%s%s", c.FeedInContainerPrefix, clientDetails.clientApiKey.String()), 12345)
		if err != nil {
			// handle connection errors to feed-in container
			log.Err(err).Msg(fmt.Sprintf("error connecting to %s", dstContainerName))
			return err
		}

	case feedprotocol.MLAT:

		dstContainerName = "mlat-server"

		// update log context
		log.With().Str("dst", fmt.Sprintf("%s:12346", clientDetails.mux))

		// attempt to connect to the mux container
		connOut, err = dialContainerTCP(clientDetails.mux, 12346)
		if err != nil {
			// handle connection errors to mux container
			log.Err(err).Msg(fmt.Sprintf("error connecting to %s", dstContainerName))
			return err
		}

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
		log.Err(err).Msgf("error writing to %s", dstContainerName)
		return err
	}

	// update stats
	conn := stats.Connection{
		ApiKey:     clientDetails.clientApiKey,
		SrcAddr:    c.Connection.RemoteAddr(),
		DstAddr:    connOut.RemoteAddr(),
		Proto:      c.ConnectionProtocol,
		FeederCode: clientDetails.feederCode,
		ConnNum:    c.ConnectionNumber,
	}
	err = registerConnectionStats(conn)
	if err != nil {
		log.Err(err).Msg("error registering connection with stats subsystem")
		return err
	}
	defer unregisterConnectionStats(conn)

	// prepare proxy config
	protoProxyConf := protocolProxyConfig{
		clientConn:                  c.Connection,
		serverConn:                  connOut,
		connNum:                     c.ConnectionNumber,
		clientApiKey:                clientDetails.clientApiKey,
		mgmt:                        &goRoutineManager{},
		lastAuthCheck:               &lastAuthCheck,
		log:                         log,
		feederValidityCheckInterval: time.Second * 60,
	}

	// handle data from feeder client to feed-in container
	wg.Add(1)
	go func() {
		defer wg.Done()
		proxyClientToServer(protoProxyConf)

		// tell other goroutine to exit
		protoProxyConf.mgmt.Stop()

		// stop proxy
		c.stopMu.Lock()
		defer c.stopMu.Unlock()
		c.stop = true
	}()

	// handle data from server container to feeder client (if required, no point doing this for BEAST)
	switch c.ConnectionProtocol {
	case feedprotocol.MLAT:
		wg.Add(1)
		go func() {
			defer wg.Done()
			proxyServerToClient(protoProxyConf)

			// tell other goroutine to exit
			protoProxyConf.mgmt.Stop()

			// stop proxy
			c.stopMu.Lock()
			defer c.stopMu.Unlock()
			c.stop = true
		}()
	}

	// "listen" for .Stop()
	for {
		c.stopMu.RLock()
		if c.stop {
			c.stopMu.RUnlock()
			protoProxyConf.mgmt.Stop()
			break
		}
		c.stopMu.RUnlock()
	}

	// wait for goroutines to finish
	wg.Wait()
	// log.Trace().Msg("finished")
	return nil
}
