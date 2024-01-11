// Package feedprotocol provides standardised representations of supported feed protocols (BEAST/MLAT).

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

	// standard protocol name for BEAST
	ProtocolNameBEAST = "BEAST"

	// standard protocol name for MLAT
	ProtocolNameMLAT = "MLAT"
)

var (
	ErrUnknownProtocol = errors.New("unknown protocol")
)

// Name returns the name of the Protocol as a string
func (p *Protocol) Name() string {
	n, err := GetName(*p)
	if err != nil {
		panic(err)
	}
	return n
}

// GetName returns a string of the name of the Protocol
func GetName(p Protocol) (string, error) {
	switch p {
	case BEAST:
		return ProtocolNameBEAST, nil
	case MLAT:
		return ProtocolNameMLAT, nil
	default:
		return "", ErrUnknownProtocol
	}
}

// IsValid returns true if the protocol is valid
func IsValid(p Protocol) bool {
	switch p {
	case BEAST:
		return true
	case MLAT:
		return true
	default:
		return false
	}
}

// GetProtoFromName returns a Protocol from name
func GetProtoFromName(name string) (Protocol, error) {
	switch strings.ToUpper(name) {
	case ProtocolNameBEAST:
		return BEAST, nil
	case ProtocolNameMLAT:
		return MLAT, nil
	default:
		return Protocol(0), ErrUnknownProtocol
	}
}
