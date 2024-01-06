package feedprotocol

import (
	"errors"
	"strings"
)

// type for protocol
type Protocol uint8

const (
	// protocol constants
	_     Protocol = iota // 0 = invalid/unsupported
	BEAST                 // 1 = BEAST
	MLAT                  // 2 = MLAT

	// name constants
	ProtocolNameBEAST = "BEAST"
	ProtocolNameMLAT  = "MLAT"
)

var (
	// errors
	ErrUnknownProtocol = errors.New("unknown protocol")
)

func (p *Protocol) Name() string {
	// return the name of the protocol as a string
	n, err := GetName(*p)
	if err != nil {
		panic(err)
	}
	return n
}

func GetName(p Protocol) (string, error) {
	// returns a string of the name of the protocol
	switch p {
	case BEAST:
		return ProtocolNameBEAST, nil
	case MLAT:
		return ProtocolNameMLAT, nil
	default:
		return "", ErrUnknownProtocol
	}
}

func IsValid(p Protocol) bool {
	// returns true if the protocol is valid
	switch p {
	case BEAST:
		return true
	case MLAT:
		return true
	default:
		return false
	}
}

func GetProtoFromName(name string) (Protocol, error) {
	// returns protocol from name
	switch strings.ToUpper(name) {
	case ProtocolNameBEAST:
		return BEAST, nil
	case ProtocolNameMLAT:
		return MLAT, nil
	default:
		return Protocol(0), ErrUnknownProtocol
	}
}
