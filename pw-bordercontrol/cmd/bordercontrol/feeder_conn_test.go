package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

const MaxUint = ^uint(0)
const MinUint = 0
const MaxInt = int(MaxUint >> 1)
const MinInt = -MaxInt - 1

func TestGetNum(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

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

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

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

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

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

func TestLookupContainerTCP(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// test lookup of loopback
	t.Run("test lookup of loopback", func(t *testing.T) {

		// prepare test data
		port := 12345
		n, err := lookupContainerTCP("localhost", port)

		// expect IPv4 or IPv6 loopback
		expected := []string{
			fmt.Sprintf("127.0.0.1:%d", port),
			fmt.Sprintf("[%s]:%d", net.IPv6loopback.String(), port),
		}

		assert.NoError(t, err)
		assert.Contains(t, expected, n.String())
	})

	// test lookup failure
	t.Run("test lookup failure", func(t *testing.T) {

		// prepare test data
		port := 12345
		_, err := lookupContainerTCP("something.invalid", port)

		assert.Error(t, err)
	})

}

func TestDialContainerTCP(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	// prepare mocked server
	srv, err := nettest.NewLocalListener("tcp4")
	assert.NoError(t, err)
	t.Log("listening on:", srv.Addr())
	go func() {
		for {
			_, err := srv.Accept()
			if err != nil {
				assert.NoError(t, err)
			}
		}
	}()

	port, err := strconv.Atoi(strings.Split(srv.Addr().String(), ":")[1])
	assert.NoError(t, err)

	// test connection
	_, err = dialContainerTCP("localhost", port)
	assert.NoError(t, err)

}
