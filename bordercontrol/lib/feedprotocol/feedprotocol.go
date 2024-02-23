// Package feedprotocol provides standardised representations of supported feed protocols (BEAST/MLAT).

package feedprotocol

import (
	"encoding/json"
	"errors"
	"fmt"
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

// function that returns an error for unknown protocol
func ErrUnknownProtocol(p any) error {
	return errors.New(fmt.Sprintf("unknown protocol: %v", p))
}

// when this struct is marshalled to JSON, this function returns a string of the protocol name
func (p Protocol) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Name())
}

// when this struct is marshalled to text, this function returns a string of the protocol name
func (p Protocol) MarshalText() ([]byte, error) {
	return []byte(p.Name()), nil
}

// when this struct is unmarshalled from JSON, this function returns a Protocol type from protocol name
func (p *Protocol) UnmarshalJSON(data []byte) error {
	var str string
	err := json.Unmarshal(data, &str)
	if err != nil {
		return err
	}
	*p, err = GetProtoFromName(str)
	if err != nil {
		return err
	}
	return nil
}

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
		return "", ErrUnknownProtocol(p)
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
		return Protocol(0), ErrUnknownProtocol(name)
	}
}
