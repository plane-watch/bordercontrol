package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const MaxUint = ^uint(0)
const MinUint = 0
const MaxInt = int(MaxUint >> 1)
const MinInt = -MaxInt - 1

func TestGetNum(t *testing.T) {

	iCT := incomingConnectionTracker{}

	// test basic functionality
	assert.Equal(t, uint(1), iCT.GetNum())
	assert.Equal(t, uint(2), iCT.GetNum())

	// test bounds wrap
	iCT.connectionNumber = MaxUint
	assert.Equal(t, uint(1), iCT.GetNum())
	assert.Equal(t, uint(2), iCT.GetNum())

	// dest duplicate avoidance
	iCT = incomingConnectionTracker{}
	iCT.connections = append(iCT.connections, incomingConnection{
		connNum: 1,
	})
	iCT.connections = append(iCT.connections, incomingConnection{
		connNum: 3,
	})
	assert.Equal(t, uint(2), iCT.GetNum())
	assert.Equal(t, uint(4), iCT.GetNum())

}
