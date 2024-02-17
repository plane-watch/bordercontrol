package feedproxy

import (
	"errors"
	"net"
	"pw_bordercontrol/lib/containers"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/stats"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
)

func TestGetUUIDfromSNI(t *testing.T) {

	var wg sync.WaitGroup

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	x := func() {
		getUUIDfromSNI(conn)
	}

	assert.Panics(t, x)

	wg.Wait()

}

func TestHandshakeComplete(t *testing.T) {

	var wg sync.WaitGroup

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	x := func() {
		handshakeComplete(conn)
	}

	assert.Panics(t, x)

	wg.Wait()
}

func TestRegisterConnectionStats(t *testing.T) {
	c := stats.Connection{}
	err := registerConnectionStats(c)
	require.Error(t, err)
}

func TestRegisterFeederWithStats(t *testing.T) {
	f := stats.FeederDetails{}
	err := RegisterFeederWithStats(f)
	assert.Error(t, err)
}

func TestStatsGetNumConnections(t *testing.T) {
	_, err := statsGetNumConnections(uuid.New(), feedprotocol.BEAST)
	require.Error(t, err)
}

func TestStartFeedInContainer(t *testing.T) {
	c := containers.FeedInContainer{}
	_, err := startFeedInContainer(&c)
	require.Error(t, err)
}

func TestDialContainerWrapper(t *testing.T) {

	var wg sync.WaitGroup

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	port, err := strconv.Atoi(strings.Split(nl.Addr().String(), ":")[1])
	require.NoError(t, err)
	_, err = dialContainerWrapper(strings.Split(nl.Addr().String(), ":")[0], port)
	require.NoError(t, err)

	wg.Wait()
}

func TestAuthenticateFeederWrapper(t *testing.T) {

	var wg sync.WaitGroup

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	x := func() {
		authenticateFeederWrapper(conn)
	}

	assert.Panics(t, x)

	wg.Wait()
}

func TestLookupContainerTCP_lookup_error(t *testing.T) {
	_, err := lookupContainerTCP("nxdomain.nodomain", 30005)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such host")
}

func TestLookupContainerTCP_lookup_ipv6(t *testing.T) {
	_, err := lookupContainerTCP("ipv6.l.google.com", 30005)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDNSReturnsNoResults)
}

func TestDialContainer_lookup_error(t *testing.T) {
	_, err := dialContainer("nxdomain.nodomain", 30005)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such host")
}

func TestDialContainer_no_connect(t *testing.T) {

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	nl.Close()

	port, err := strconv.Atoi(strings.Split(nl.Addr().String(), ":")[1])
	require.NoError(t, err)
	_, err = dialContainer(strings.Split(nl.Addr().String(), ":")[0], port)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")

}

func TestAuthenticateFeeder_ErrTLSHandshakeIncomplete(t *testing.T) {

	var wg sync.WaitGroup

	// replace function for testing
	origHandshakeComplete := handshakeComplete
	defer func() { handshakeComplete = origHandshakeComplete }()
	handshakeComplete = func(c net.Conn) bool {
		return false
	}

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	_, err = authenticateFeeder(conn)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTLSHandshakeIncomplete)

	wg.Wait()

}

func TestAuthenticateFeeder_getUUIDfromSNI_error(t *testing.T) {

	var wg sync.WaitGroup

	// replace function for testing
	origHandshakeComplete := handshakeComplete
	defer func() { handshakeComplete = origHandshakeComplete }()
	handshakeComplete = func(c net.Conn) bool {
		return true
	}

	// replace function for testing
	origGetUUIDfromSNI := getUUIDfromSNI
	defer func() { getUUIDfromSNI = origGetUUIDfromSNI }()
	getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
		return uuid.New(), errors.New("error for testing")
	}

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	_, err = authenticateFeeder(conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()

}

func TestAuthenticateFeeder_isValidApiKey_error(t *testing.T) {

	var wg sync.WaitGroup

	// replace function for testing
	origHandshakeComplete := handshakeComplete
	defer func() { handshakeComplete = origHandshakeComplete }()
	handshakeComplete = func(c net.Conn) bool {
		return true
	}

	// replace function for testing
	origGetUUIDfromSNI := getUUIDfromSNI
	defer func() { getUUIDfromSNI = origGetUUIDfromSNI }()
	getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
		return uuid.New(), nil
	}

	// replace function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return false
	}

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	_, err = authenticateFeeder(conn)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrClientSentInvalidAPIKey)

	wg.Wait()

}

func TestAuthenticateFeeder_getFeederInfo_error(t *testing.T) {

	var wg sync.WaitGroup

	// replace function for testing
	origHandshakeComplete := handshakeComplete
	defer func() { handshakeComplete = origHandshakeComplete }()
	handshakeComplete = func(c net.Conn) bool {
		return true
	}

	// replace function for testing
	origGetUUIDfromSNI := getUUIDfromSNI
	defer func() { getUUIDfromSNI = origGetUUIDfromSNI }()
	getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
		return uuid.New(), nil
	}

	// replace function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return true
	}

	// replace function for testing
	origGetFeederInfoWrapper := getFeederInfoWrapper
	defer func() { getFeederInfoWrapper = origGetFeederInfoWrapper }()
	getFeederInfoWrapper = func(f *feederClient) error {
		return errors.New("error for testing")
	}

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	_, err = authenticateFeeder(conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()

}

func TestAuthenticateFeeder_RegisterFeederWithStats_error(t *testing.T) {

	var wg sync.WaitGroup

	// replace function for testing
	origHandshakeComplete := handshakeComplete
	defer func() { handshakeComplete = origHandshakeComplete }()
	handshakeComplete = func(c net.Conn) bool {
		return true
	}

	// replace function for testing
	origGetUUIDfromSNI := getUUIDfromSNI
	defer func() { getUUIDfromSNI = origGetUUIDfromSNI }()
	getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
		return uuid.New(), nil
	}

	// replace function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return true
	}

	// replace function for testing
	origGetFeederInfoWrapper := getFeederInfoWrapper
	defer func() { getFeederInfoWrapper = origGetFeederInfoWrapper }()
	getFeederInfoWrapper = func(f *feederClient) error {
		return nil
	}

	// replace function for testing
	origRegisterFeederWithStats := RegisterFeederWithStats
	defer func() { RegisterFeederWithStats = origRegisterFeederWithStats }()
	RegisterFeederWithStats = func(f stats.FeederDetails) error {
		return errors.New("error for testing")
	}

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	_, err = authenticateFeeder(conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()

}

func TestAuthenticateFeeder_working(t *testing.T) {

	var wg sync.WaitGroup

	testUUID := uuid.New()

	// replace function for testing
	origHandshakeComplete := handshakeComplete
	defer func() { handshakeComplete = origHandshakeComplete }()
	handshakeComplete = func(c net.Conn) bool {
		return true
	}

	// replace function for testing
	origGetUUIDfromSNI := getUUIDfromSNI
	defer func() { getUUIDfromSNI = origGetUUIDfromSNI }()
	getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
		return testUUID, nil
	}

	// replace function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return true
	}

	// replace function for testing
	origGetFeederInfoWrapper := getFeederInfoWrapper
	defer func() { getFeederInfoWrapper = origGetFeederInfoWrapper }()
	getFeederInfoWrapper = func(f *feederClient) error {
		return nil
	}

	// replace function for testing
	origRegisterFeederWithStats := RegisterFeederWithStats
	defer func() { RegisterFeederWithStats = origRegisterFeederWithStats }()
	RegisterFeederWithStats = func(f stats.FeederDetails) error {
		return nil
	}

	nl, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err, "create test listener")
	defer nl.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		svr, err := nl.Accept()
		require.NoError(t, err, "accept connection to test listener")
		defer svr.Close()
	}()

	conn, err := net.Dial("tcp", nl.Addr().String())
	require.NoError(t, err, "dial test listener to set up connection")
	defer conn.Close()

	f, err := authenticateFeeder(conn)
	require.NoError(t, err)
	assert.Equal(t, testUUID, f.clientApiKey)

	wg.Wait()

}
