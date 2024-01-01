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
		return "BEAST", nil
	case MLAT:
		return "MLAT", nil
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
