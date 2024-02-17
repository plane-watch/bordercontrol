package feedproxy

import (
	"net"
	"pw_bordercontrol/lib/stats"

	"github.com/google/uuid"

	"github.com/rs/zerolog/log"
)

type (

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

	// network send/recv buffer size
	sendRecvBufferSize = 256 * 1024 // 256kB
)

var (
	incomingConnTracker incomingConnectionTracker
)

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

func dialContainer(container string, port int) (c net.Conn, err error) {
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
	c, err = net.Dial("tcp", dstTCPAddr.String())
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
	if !isValidApiKeyWrapper(clientDetails.clientApiKey) {
		// if API is not valid, then kill the connection
		err := ErrClientSentInvalidAPIKey
		return clientDetails, err
	}

	// get feeder info (lat/lon/mux/label) from atc cache
	err = getFeederInfoWrapper(&clientDetails)
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
