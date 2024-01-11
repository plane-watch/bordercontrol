package feedproxy

import (
	"context"
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

	// used to limit the number of connections over time from a single source IP
	incomingConnection struct {
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

	// allow override of these functions to simplify testing
	getUUIDfromSNI            = func(c net.Conn) (u uuid.UUID, err error) { return uuid.Parse(stunnel.GetSNI(c)) }
	handshakeComplete         = func(c net.Conn) bool { return stunnel.HandshakeComplete(c) }
	RegisterFeederWithStats   = func(f stats.FeederDetails) error { return stats.RegisterFeeder(f) }
	registerConnectionStats   = func(conn stats.Connection) error { return conn.RegisterConnection() }
	unregisterConnectionStats = func(conn stats.Connection) error { return conn.UnregisterConnection() }
	statsGetNumConnections    = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return stats.GetNumConnections(uuid, proto)
	}
	statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, bytesIn, bytesOut uint64) error {
		return stats.IncrementByteCounters(uuid, connNum, bytesIn, bytesOut)
	}
	startFeedInContainer    = func(c *containers.FeedInContainer) (containerID string, err error) { return c.Start() }
	dialContainerTCPWrapper = func(addr string, port int) (c *net.TCPConn, err error) {
		return dialContainerTCP(addr, port)
	}
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return authenticateFeeder(connIn)
	}
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

	var dupe bool

	t.mu.Lock()
	defer t.mu.Unlock()

	for {

		// determine next available connection number
		t.connectionNumber++
		if t.connectionNumber == 0 { // don't have a connection with number 0
			t.connectionNumber++
		}

		// is the connection number already in use
		dupe = false
		for _, c := range t.connections {
			if t.connectionNumber == c.connNum {
				dupe = true
				break
			}
		}

		// if not a duplicate, break out of loop
		if !dupe {
			break
		}
	}

	return t.connectionNumber
}

func (t *incomingConnectionTracker) evict() {
	// evicts connections from the tracker if older than 10 seconds

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

}

func (t *incomingConnectionTracker) check(srcIP net.IP, connNum uint) (err error) {
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

	if connCount >= maxIncomingConnectionRequestsPerSrcIP {
		// if connecting too frequently, raise an error
		err = ErrConnectingTooFrequently(maxIncomingConnectionRequestsPerSrcIP, maxIncomingConnectionRequestSeconds)

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

	return err
}

func lookupContainerTCP(container string, port int) (n *net.TCPAddr, err error) {
	// perform DNS lookup & return net.TCPAddr

	var dstIP net.IP
	dstIPs, err := net.LookupIP(container)
	if err != nil {
		// error performing lookup
		log.Err(err).Msg("error performing net.LookupIP")
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
			err = ErrDNSReturnsNoResults
		}

		// prep address to connect to
		n = &net.TCPAddr{
			IP:   dstIP,
			Port: port,
		}
	}

	return n, err
}

func dialContainerTCP(container string, port int) (c *net.TCPConn, err error) {
	// dials a host & returns net.TCPConn

	// update log context
	log := log.With().
		Strs("func", []string{"feeder_conn.go", "dialContainerTCP"}).
		Str("container", container).
		Int("port", port).
		Logger()

	// lookup container IP, return TCP address
	dstTCPAddr, err := lookupContainerTCP(container, port)
	if err != nil {
		return c, err
	}

	// update log context
	log = log.With().Str("dstTCPAddr", dstTCPAddr.IP.String()).Int("port", port).Logger()

	// dial feed-in container
	c, err = net.DialTCP("tcp", nil, dstTCPAddr)
	if err != nil {
		return c, err
	}

	return c, err
}

func authenticateFeeder(connIn net.Conn) (clientDetails feederClient, err error) {
	// authenticates a feeder by checking SNI against valid feeders

	// prep variable to hold feeder details
	clientDetails = feederClient{}

	// update log context
	log := log.With().
		Strs("func", []string{"feeder_conn.go", "authenticateFeeder"}).
		Logger()

	// if TLS handshake is not complete, then kill the connection
	if !handshakeComplete(connIn) {
		err := ErrTLSHandshakeIncomplete
		return clientDetails, err
	}

	// update log context
	log = log.With().Bool("TLSHandshakeComplete", true).Logger()

	// check valid uuid was returned as ServerName (sni)
	clientDetails.clientApiKey, err = getUUIDfromSNI(connIn)
	if err != nil {
		return clientDetails, err
	}

	// update log context
	log = log.With().
		Str("uuid", clientDetails.clientApiKey.String()).
		Str("code", clientDetails.feederCode).
		Logger()

	// check api key against valid feeders from atc
	if !isValidApiKey(clientDetails.clientApiKey) {
		// if API is not valid, then kill the connection
		err := ErrClientSentInvalidAPIKey
		return clientDetails, err
	}

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

type proxyDirection uint8

const (
	_              = iota // 0 = unsupported/invalid
	clientToServer        // 1 = client to server
	serverToClient        // 2 = server to client
)

type protocolProxyConfig struct {
	clientConn                  net.Conn        // Client-side connection (feeder out on the internet).
	serverConn                  net.Conn        // Server-side connection (feed-in container or mlat server).
	connNum                     uint            // Connection number (used for statistics and stuff).
	clientApiKey                uuid.UUID       // Client's API Key (from stunnel SNI).
	lastAuthCheck               *time.Time      // Timestamp for when the client's API key was checked for validity (to handle kicked/banned feeders).
	lastAuthCheckMu             sync.RWMutex    // mutex for lastAuthCheck to prevent data race
	log                         zerolog.Logger  // Log. This allows the proxy to inherit a logging context.
	feederValidityCheckInterval time.Duration   // How often to check the feeder is still valid.
	ctx                         context.Context // connection context
}

func feederStillValid(conf *protocolProxyConfig) error {
	// checks feeder is still valid every feederValidityCheckInterval

	// if validity check interval has been reached
	conf.lastAuthCheckMu.RLock()
	if time.Now().After(conf.lastAuthCheck.Add(conf.feederValidityCheckInterval)) {
		conf.lastAuthCheckMu.RUnlock()

		// check still valid
		if !isValidApiKey(conf.clientApiKey) {
			return ErrFeederNoLongerValid
		}

		// if still valid, reset lastAuthCheck time to now
		conf.lastAuthCheckMu.Lock()
		*conf.lastAuthCheck = time.Now()
		conf.lastAuthCheckMu.Unlock()
	} else {
		conf.lastAuthCheckMu.RUnlock()
	}
	return nil
}

func protocolProxy(conf *protocolProxyConfig, direction proxyDirection) error {
	// proxies connection between client to server

	var (
		connA, connB net.Conn
		// connAName, connBName  string
		incrementByteCounters func(uuid uuid.UUID, connNum uint, bytes uint64) error
	)

	// set up function for specific direction
	switch direction {
	case clientToServer:
		connA = conf.clientConn
		connB = conf.serverConn
		// connAName = "client"
		// connBName = "server"
		incrementByteCounters = func(uuid uuid.UUID, connNum uint, bytes uint64) error {
			return statsIncrementByteCounters(conf.clientApiKey, conf.connNum, uint64(bytes), 0)
		}
	case serverToClient:
		connA = conf.serverConn
		connB = conf.clientConn
		// connAName = "server"
		// connBName = "client"
		incrementByteCounters = func(uuid uuid.UUID, connNum uint, bytes uint64) error {
			return statsIncrementByteCounters(conf.clientApiKey, conf.connNum, 0, uint64(bytes))
		}
	}

	// human readable direction
	// directionStr := fmt.Sprintf("%s to %s", connAName, connBName)

	// log := conf.log.With().Str("proxy", directionStr).Logger()
	buf := make([]byte, sendRecvBufferSize)
	for {

		// quit if directed
		select {
		case <-conf.ctx.Done():
			return nil
		default:

			// read from feeder client
			err := connA.SetReadDeadline(time.Now().Add(time.Second * 1))
			if err != nil {
				return err
			}
			bytesRead, err := connA.Read(buf)
			if err != nil {
				// don't close connection on read deadline exceeded - client may have nothing to send...
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					return err
				}
			} else {

				// write to server
				err := connB.SetWriteDeadline(time.Now().Add(time.Second * 2))
				if err != nil {
					return err
				}
				_, err = connB.Write(buf[:bytesRead])
				if err != nil {
					return err
				}

				// update stats
				err = incrementByteCounters(conf.clientApiKey, conf.connNum, uint64(bytesRead))
				if err != nil {
					return err
				}
			}

			// check feeder is still valid
			err = feederStillValid(conf)
			if err != nil {
				return err
			}
		}
	}
}

type ProxyConnection struct {
	Connection                  net.Conn              // Incoming connection from feeder out on the internet
	ConnectionProtocol          feedprotocol.Protocol // Incoming connection protocol
	ConnectionNumber            uint                  // Connection number
	InnerConnectionPort         int                   // Server-side port to proxy connection to (default: 12345 for BEAST & 12346 MLAT)
	FeedInContainerPrefix       string                // feed-in container prefix
	Logger                      zerolog.Logger        // logger context to use
	FeederValidityCheckInterval time.Duration         // how often to check feeder is still valid in atc

	protoProxyConf protocolProxyConfig // config for protocol proxy goroutines

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (c *ProxyConnection) Stop() error {
	// Stops proxying incoming connection.

	if !isInitialised() {
		return ErrNotInitialised
	}

	c.cancel()

	return nil
}

func (c *ProxyConnection) Start(ctx context.Context) error {
	// Starts proxying incoming connection. Will create feed-in container if required.

	if !isInitialised() {
		return ErrNotInitialised
	}

	// set up inner context
	c.ctx, c.cancel = context.WithCancel(ctx)

	// update log context
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
		dstContainerName string
	)

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
			return err
		} else {
			log.Err(err).Msg("error reading from client")
			return err
		}
	}

	// When the first data is sent, the TLS handshake should take place.
	// Accordingly, we need to track the state...
	clientDetails, err = authenticateFeederWrapper(c.Connection)
	if err != nil {
		log.Err(err).Msg("error authenticating feeder")
		return err
	}

	// update log context
	log = log.With().
		Str("uuid", clientDetails.clientApiKey.String()).
		Str("mux", clientDetails.mux).
		Str("label", clientDetails.label).
		Str("code", clientDetails.feederCode).
		Logger()

	// get number of connections to check for too frequent connections
	numConnections, err := statsGetNumConnections(clientDetails.clientApiKey, c.ConnectionProtocol)
	if err != nil {
		log.Err(err).Msg("error getting number of connections")
		return err
	}

	// update log context
	log = log.With().Str("connections", fmt.Sprintf("%d/%d", numConnections+1, maxConnectionsPerProto)).Logger()

	// check number of connections, and drop connection if limit exceeded
	if numConnections >= maxConnectionsPerProto {
		err := ErrConnectionLimitExceeded
		log.Err(err).Msg("dropping connection")
		return err
	}

	// to check auth every FeederValidityCheckInterval
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
		_, err := startFeedInContainer(&feedInContainer)
		if err != nil {
			log.Err(err).Msg(fmt.Sprintf("could not start %s", dstContainerName))
		}

		// connect to feed-in container
		connOut, err = dialContainerTCPWrapper(fmt.Sprintf("%s%s", c.FeedInContainerPrefix, clientDetails.clientApiKey.String()), c.InnerConnectionPort)
		if err != nil {
			// handle connection errors to feed-in container
			log.Err(err).Msg(fmt.Sprintf("error connecting to %s", dstContainerName))
			return err
		}

	case feedprotocol.MLAT:

		dstContainerName = "mlat-server"

		// update log context
		log.With().Str("dst", fmt.Sprintf("%s:%d", clientDetails.mux, c.InnerConnectionPort))

		// attempt to connect to the mux container
		connOut, err = dialContainerTCP(clientDetails.mux, c.InnerConnectionPort)
		if err != nil {
			// handle connection errors to mux container
			log.Err(err).Msg(fmt.Sprintf("error connecting to %s", dstContainerName))
			return err
		}

	default:
		err := ErrUnsupportedProtocol
		log.Err(err).Msg("could not start proxy")
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

	log.Info().Msg(fmt.Sprintf("%s connected", c.ConnectionProtocol.Name()))

	// write any outstanding data
	_, err = connOut.Write(buf[:bytesRead])
	if err != nil {
		log.Err(err).Msgf("error writing to %s", dstContainerName)
		return err
	}

	// register connection with stats subsystem
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
		lastAuthCheck:               &lastAuthCheck,
		log:                         log,
		feederValidityCheckInterval: c.FeederValidityCheckInterval,
		ctx:                         c.ctx,
	}

	// handle data from feeder client to server (feed-in container, mux, etc)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		err := protocolProxy(&protoProxyConf, clientToServer)
		if err != nil {
			if err.Error() != "EOF" { // don't bother logging EOF here
				log.Err(err).Msg("feeder connection error")
			}
		}

		// cancel context, causing related connections to close, and this ProxyConnection to close
		c.cancel()
	}()

	// handle data from server to feeder client (if required, no point doing this for BEAST)
	switch c.ConnectionProtocol {
	case feedprotocol.MLAT:
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			err := protocolProxy(&protoProxyConf, serverToClient)
			if err != nil {
				if err.Error() != "EOF" { // don't bother logging EOF here
					log.Err(err).Msg("feeder connection error")
				}
			}

			// cancel context, causing related connections to close, and this ProxyConnection to close
			c.cancel()
		}()
	}

	// wait for context closure (either inner or outer)
	select {
	case <-ctx.Done(): // if outer, then close inner
		log.Info().Msg("shutting down connection")
		c.cancel()
	case <-c.ctx.Done(): // if inner, then do nothing (don't close outer!)
	}

	// wait for goroutines to finish
	c.wg.Wait()
	log.Info().Msg(fmt.Sprintf("%s disconnected", c.ConnectionProtocol.Name()))

	return nil
}
