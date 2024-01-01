package feedprotocol

import "errors"

type Protocol uint8

const (
	_ Protocol = iota
	BEAST
	MLAT
)

var (
	ErrUnknownProtocol = errors.New("unknown protocol")
)

func GetName(p Protocol) (string, error) {
	// returns a string of the name of the protocol
	switch p {
	case BEAST:
		return "BEAST", nil
	case MLAT:
		return "MLAT", nil
	default:
		return "", ErrUnknownProtocol
	}
}
