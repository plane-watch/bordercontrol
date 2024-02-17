package feedproxy

import (
	"net"
	"pw_bordercontrol/lib/containers"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/stats"
	"pw_bordercontrol/lib/stunnel"

	"github.com/google/uuid"
)

var (
	// allow override of these functions to simplify testing
	getUUIDfromSNI            = func(c net.Conn) (u uuid.UUID, err error) { return uuid.Parse(stunnel.GetSNI(c)) }
	handshakeComplete         = func(c net.Conn) bool { return stunnel.HandshakeComplete(c) }
	RegisterFeederWithStats   = func(f stats.FeederDetails) error { return stats.RegisterFeeder(f) }
	registerConnectionStats   = func(conn stats.Connection) error { return conn.RegisterConnection() }
	unregisterConnectionStats = func(conn stats.Connection) error { return conn.UnregisterConnection() }
	statsGetNumConnections    = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return stats.GetNumConnections(uuid, proto)
	}
	statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, proto feedprotocol.Protocol, bytesIn, bytesOut uint64) error {
		return stats.IncrementByteCounters(uuid, connNum, proto, bytesIn, bytesOut)
	}
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) { return c.Start() }
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return dialContainer(addr, port)
	}
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return authenticateFeeder(connIn)
	}
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return isValidApiKey(clientApiKey)
	}
	getFeederInfoWrapper = func(f *feederClient) error {
		return getFeederInfo(f)
	}
)
