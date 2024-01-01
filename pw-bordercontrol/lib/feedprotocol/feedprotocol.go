package feedprotocol

import "errors"

type Protocol uint8

const (
	_ Protocol = iota
	BEAST
	MLAT
)

func GetName(p Protocol) (s string, err error) {
	switch p {
	case BEAST:
		s = "BEAST"
	case MLAT:
		s = "MLAT"
	default:
		err = errors.New("unknown protocol")
	}
	return s, err
}
