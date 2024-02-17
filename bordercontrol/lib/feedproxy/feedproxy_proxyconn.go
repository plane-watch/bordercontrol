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
	"strings"
	"time"

	"github.com/rs/zerolog"
)

var (
	noDataWaitDuration = time.Minute
)

type ProxyConnection struct {
	Connection                  net.Conn              // Incoming connection from feeder out on the internet
	ConnectionProtocol          feedprotocol.Protocol // Incoming connection protocol
	ConnectionNumber            uint                  // Connection number
	InnerConnectionPort         int                   // Server-side port to proxy connection to (default: 12345 for BEAST & 12346 MLAT)
	FeedInContainerPrefix       string                // feed-in container prefix
	Logger                      zerolog.Logger        // logger context to use
	FeederValidityCheckInterval time.Duration         // how often to check feeder is still valid in atc
}

func (c *ProxyConnection) Start(ctx context.Context) error {
	// Starts proxying incoming connection. Will create feed-in container if required.

	defer c.Connection.Close()

	// Ensure initialized
	if !isInitialised() {
		return ErrNotInitialised
	}

	var (
		connOut           net.Conn
		clientDetails     feederClient
		lastAuthCheck     time.Time
		lastStatsUpdate   time.Time
		err               error
		dstContainerName  string
		bytesIn, bytesOut int
	)

	// update log context
	log := c.Logger.With().
		Strs("func", []string{"feeder_conn.go", "proxyClientConnection"}).
		Uint("connNum", c.ConnectionNumber).
		Logger()

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(c.Connection.RemoteAddr().String(), ":")[0])
	log = log.With().IPAddr("src", remoteIP).Logger()

	// check for too-frequent incoming connections
	log.Trace().Msg("incomingConnTracker.check")
	err = incomingConnTracker.check(remoteIP, c.ConnectionNumber)
	if err != nil {
		log.Err(err).Msg("dropping connection")
		return err
	}

	// make buffer to hold data read from client
	buf := make([]byte, sendRecvBufferSize)

	// give the unauthenticated client 10 seconds to perform TLS handshake
	log.Trace().Msg("c.Connection.SetDeadline")
	c.Connection.SetDeadline(time.Now().Add(time.Second * 10))

	// read data from client
	log.Trace().Msg("readFromClient")
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
	log.Trace().Msg("authenticateFeederWrapper")
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
	log.Trace().Msg("statsGetNumConnections")
	numConnections, err := statsGetNumConnections(clientDetails.clientApiKey, c.ConnectionProtocol)
	if err != nil {
		log.Err(err).Msg("error getting number of connections")
		return err
	}

	// update log context
	log = log.With().Str("connections", fmt.Sprintf("%d/%d", numConnections+1, maxConnectionsPerProto)).Logger()

	// check number of connections, and drop connection if limit exceeded
	log.Trace().Msg("maxConnectionsPerProto")
	if numConnections >= maxConnectionsPerProto {
		err := ErrConnectionLimitExceeded
		log.Err(err).Msg("dropping connection")
		return err
	}

	// to check auth every FeederValidityCheckInterval
	lastAuthCheck = time.Now()

	// If the client has been authenticated, then we can proxy the data

	switch c.ConnectionProtocol {

	// if BEAST protocol, ensure feed-in container started and running
	case feedprotocol.BEAST:

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
			log.Err(err).Msg(fmt.Sprintf("could not start feed-in container"))
			return err
		}

		// connect to feed-in container
		connOut, err = dialContainerWrapper(fmt.Sprintf("%s%s", c.FeedInContainerPrefix, clientDetails.clientApiKey.String()), c.InnerConnectionPort)
		if err != nil {
			// handle connection errors to feed-in container
			log.Err(err).Msg(fmt.Sprintf("error connecting to feed-in container"))
			return err
		}
		defer connOut.Close()

	case feedprotocol.MLAT:

		// update log context
		log.With().Str("dst", fmt.Sprintf("%s:%d", clientDetails.mux, c.InnerConnectionPort))

		// attempt to connect to the mux container
		connOut, err = dialContainerWrapper(clientDetails.mux, c.InnerConnectionPort)
		if err != nil {
			// handle connection errors to mux container
			log.Err(err).Msg(fmt.Sprintf("error connecting to mlat-server"))
			return err
		}
		defer connOut.Close()

	default:
		err := ErrUnsupportedProtocol
		log.Err(err).Msg("could not start proxy")
		return err
	}

	// connected OK...

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

	log.Trace().Msg("after registerConnectionStats")

	// prepare channels
	clientReadChan, clientWriteChan := connToChans(c.Connection, sendRecvBufferSize)
	defer close(clientWriteChan)
	serverReadChan, serverWriteChan := connToChans(connOut, sendRecvBufferSize)
	defer close(serverWriteChan)

	lastStatsUpdate = time.Now()

	// proxy data
	finishProxying := false // method to break out of for loop from inside select
	for {

		// check feeder still valid
		if time.Now().After(lastAuthCheck.Add(c.FeederValidityCheckInterval)) {
			if !isValidApiKeyWrapper(clientDetails.clientApiKey) {
				log.Warn().Msg("feeder no longer valid!")
				break
			}
		}

		select {

		// if no data, check feeder still valid every minute
		// case <-time.After(noDataWaitDuration):

		// handle context closure
		case <-ctx.Done():
			log.Info().Msg("shutting down connection")
			finishProxying = true
			break

		// attempt read from client
		case b, ok := <-clientReadChan:
			if !ok {
				log.Trace().Msg("read from clientReadChan not ok")
				finishProxying = true
				break
			}
			bytesIn += len(b)

			// write data from client to server
			serverWriteChan <- b

		// attempt read from server
		case b, ok := <-serverReadChan:
			if !ok {
				log.Trace().Msg("read from serverReadChan not ok")
				finishProxying = true
				break
			}
			bytesOut += len(b)

			// write data from server to client
			clientWriteChan <- b
		}

		// update stats if either
		//  - its been 1 second; or
		//  - we're getting close to integer bounds
		if time.Now().After(lastStatsUpdate.Add(time.Second*1)) || bytesIn > 20000 || bytesOut > 20000 {
			// increment status counter
			err := statsIncrementByteCounters(
				clientDetails.clientApiKey,
				c.ConnectionNumber,
				c.ConnectionProtocol,
				uint64(bytesIn),
				uint64(bytesOut))
			// if error, bail out
			if err != nil {
				break
			}
			// zero counters
			bytesIn = 0
			bytesOut = 0
		}

		// if there's been an issue, then break out of the loop
		if finishProxying {
			break
		}
	}

	log.Info().Msg(fmt.Sprintf("%s disconnected", c.ConnectionProtocol.Name()))

	return nil
}
