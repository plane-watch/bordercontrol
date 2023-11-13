package main

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const MaxUint = ^uint(0)
const MinUint = 0
const MaxInt = int(MaxUint >> 1)
const MinInt = -MaxInt - 1

func TestGetNum(t *testing.T) {

	iCT := incomingConnectionTracker{}

	// test basic functionality
	t.Run("test basic functionality", func(t *testing.T) {
		assert.Equal(t, uint(1), iCT.getNum())
		assert.Equal(t, uint(2), iCT.getNum())
	})

	// test bounds wrap
	t.Run("test bounds wrap", func(t *testing.T) {
		iCT.connectionNumber = MaxUint
		assert.Equal(t, uint(1), iCT.getNum())
		assert.Equal(t, uint(2), iCT.getNum())
	})

	// test duplicate avoidance
	t.Run("test duplicate avoidance", func(t *testing.T) {
		iCT = incomingConnectionTracker{}
		iCT.connections = append(iCT.connections, incomingConnection{
			connNum: 1,
		})
		iCT.connections = append(iCT.connections, incomingConnection{
			connNum: 3,
		})
		assert.Equal(t, uint(2), iCT.getNum())
		assert.Equal(t, uint(4), iCT.getNum())
	})
}

func TestEvict(t *testing.T) {

	iCT := incomingConnectionTracker{}

	// add a connection with connection time older than 10 seconds
	iCT.connections = append(iCT.connections, incomingConnection{
		connTime: time.Now().Add(-(time.Second * 11)),
		connNum:  1,
	})

	// add a connection with a connection time newer than 10 seconds
	iCT.connections = append(iCT.connections, incomingConnection{
		connTime: time.Now().Add(-(time.Second * 2)),
		connNum:  2,
	})

	// should be 2 connections in the list prior to running evict
	assert.Equal(t, 2, len(iCT.connections))

	// run evict
	iCT.evict()

	// should be 1 connection in the list after running evict
	assert.Equal(t, 1, len(iCT.connections))

	// connection 2 should be the only connection in the list
	assert.Equal(t, uint(2), iCT.connections[0].connNum)
}

func TestCheck(t *testing.T) {

	// prepare test data
	srcIP := net.IPv4(172, 0, 0, 1)
	iCT := incomingConnectionTracker{}

	// add max number of connections
	t.Run("add max number of connections", func(t *testing.T) {
		for i := uint(0); i < maxIncomingConnectionRequestsPerSrcIP; i++ {
			err := iCT.check(srcIP, iCT.getNum())
			assert.NoError(t, err, "should pass")
		}
	})

	// add next connection (should fail)
	t.Run("add connection exceeding max", func(t *testing.T) {
		err := iCT.check(srcIP, iCT.getNum())
		assert.Error(t, err)
	})
}
