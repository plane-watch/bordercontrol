package feedproxy

import (
	"errors"
	"fmt"
)

var (
	ErrNotInitialised          = errors.New("feedproxy not initialised")
	ErrDNSReturnsNoResults     = errors.New("DNS lookup returned no IPv4 address(es)")
	ErrTLSHandshakeIncomplete  = errors.New("TLS handshake incomplete")
	ErrClientSentInvalidAPIKey = errors.New("client sent invalid API key")
	ErrConnectionLimitExceeded = errors.New("connection limit exceeded")
	ErrUnsupportedProtocol     = errors.New("unsupported protocol")
	ErrFeederNotFound          = errors.New("feeder not found")
)

func ErrConnectingTooFrequently(maxIncomingConnectionRequestsPerSrcIP, maxIncomingConnectionRequestSeconds int) error {
	return errors.New(fmt.Sprintf("client connecting too frequently: more than %d connections from src within a %d second period",
		maxIncomingConnectionRequestsPerSrcIP,
		maxIncomingConnectionRequestSeconds,
	))
}
