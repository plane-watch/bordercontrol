package feedproxy

import (
	"context"
	"errors"
	"net"
	"os"
	"pw_bordercontrol/lib/containers"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/stats"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})
}

func TestIsValidApiKeyWrapper(t *testing.T) {
	assert.False(t, isValidApiKeyWrapper(uuid.New()))
}

func TestStart_ErrNotInitialised(t *testing.T) {

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	conf := ProxyConnection{
		Connection: connB,
	}

	err := conf.Start(pctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotInitialised)

}

func TestStart_too_frequent_connections(t *testing.T) {

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection: connB,
	}

	for i := 1; i <= maxIncomingConnectionRequestsPerSrcIP+2; i++ {
		err := conf.Start(pctx)
		if i > maxIncomingConnectionRequestsPerSrcIP {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "client connecting too frequently")
		}
	}
}

func TestStart_readFromClient_error(t *testing.T) {

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection: connB,
	}

	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "i/o timeout")
}

func TestStart_error_authenticating_feeder(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection: connB,
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()

	// inject error
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return feederClient{}, errors.New("error for testing")
	}

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()
}

func TestStart_error_statsGetNumConnections(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection: connB,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, errors.New("error for testing")
	}

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()
}

func TestStart_error_ErrConnectionLimitExceeded(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection: connB,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 1, nil
	}

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConnectionLimitExceeded)

	wg.Wait()
}

func TestStart_error_ErrUnsupportedProtocol(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection: connB,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "", errors.New("error for testing")
	}

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedProtocol)

	wg.Wait()
}

func TestStart_error_startFeedInContainer_beast(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.BEAST,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "", errors.New("error for testing")
	}

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()
}

func TestStart_error_dialContainerWrapper_beast(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.BEAST,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, errors.New("error for testing")
	}

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()
}

func TestStart_error_dialContainerWrapper_mlat(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.MLAT,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, errors.New("error for testing")
	}

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()
}

func TestStart_error_connOut_Write_beast(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.BEAST,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// inject error
	connC.Close()

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")

	wg.Wait()
}

func TestStart_error_connOut_Write_mlat(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}
	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.MLAT,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// inject error
	connC.Close()

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")

	wg.Wait()
}

func TestStart_error_registerConnectionStats_beast(t *testing.T) {

	var wg sync.WaitGroup

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.BEAST,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// swap out function for testing
	origRegisterConnectionStats := registerConnectionStats
	defer func() { registerConnectionStats = origRegisterConnectionStats }()
	registerConnectionStats = func(conn stats.Connection) error {
		return errors.New("error for testing")
	}

	// write some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
	}()

	// read some data to prevent io timeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := make([]byte, 1024)
		n, err := connD.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
	}()

	// run test
	err = conf.Start(pctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error for testing")

	wg.Wait()
}

func TestStart_beast(t *testing.T) {

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.BEAST,
		Logger:             log.Logger,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// update noDataWaitDuration
	origNoDataWaitDuration := noDataWaitDuration
	defer func() { noDataWaitDuration = origNoDataWaitDuration }()
	noDataWaitDuration = time.Millisecond * 250

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// swap out function for testing
	origRegisterConnectionStats := registerConnectionStats
	defer func() { registerConnectionStats = origRegisterConnectionStats }()
	registerConnectionStats = func(conn stats.Connection) error {
		return nil
	}

	// swap out function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return true
	}

	wg := sync.WaitGroup{}

	// run test
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = conf.Start(pctx)
		require.NoError(t, err)
	}()

	wgA := sync.WaitGroup{}

	// write some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("first write")
	}()

	// read some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := make([]byte, 1024)
		n, err := connD.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("first read")
	}()

	// wait for first i/o to complete
	wgA.Wait()

	// let a few no data loops complete
	time.Sleep(time.Second)

	wgB := sync.WaitGroup{}

	// read/write some data
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("second write")
	}()
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := make([]byte, 1024)
		n, err := connD.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("second read")
	}()

	// wait for second i/o to complete
	wgB.Wait()

	// shutdown
	pcancel()

	// wait for shutdown
	wg.Wait()
}

func TestStart_beast_feeder_no_longer_valid(t *testing.T) {

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.BEAST,
		Logger:             log.Logger,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// swap out function for testing
	origRegisterConnectionStats := registerConnectionStats
	defer func() { registerConnectionStats = origRegisterConnectionStats }()
	registerConnectionStats = func(conn stats.Connection) error {
		return nil
	}

	// swap out function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return false
	}

	wg := sync.WaitGroup{}

	// run test
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = conf.Start(pctx)
		require.NoError(t, err)
	}()

	wgA := sync.WaitGroup{}

	// write some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("first write")
	}()

	// read some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := make([]byte, 1024)
		n, err := connD.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("first read")
	}()

	// wait for first i/o to complete
	wgA.Wait()

	wgB := sync.WaitGroup{}

	// read/write some data
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("second write")
	}()
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := make([]byte, 1024)
		_, err := connD.Read(b)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "EOF")
		t.Log("second read")
	}()

	// wait for second i/o to complete
	wgB.Wait()

	// shutdown
	pcancel()

	// wait for shutdown
	wg.Wait()
}

func TestStart_beast_clientReadChan_close(t *testing.T) {

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.BEAST,
		Logger:             log.Logger,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// update noDataWaitDuration
	origNoDataWaitDuration := noDataWaitDuration
	defer func() { noDataWaitDuration = origNoDataWaitDuration }()
	noDataWaitDuration = time.Millisecond * 250

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// swap out function for testing
	origRegisterConnectionStats := registerConnectionStats
	defer func() { registerConnectionStats = origRegisterConnectionStats }()
	registerConnectionStats = func(conn stats.Connection) error {
		return nil
	}

	// swap out function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return true
	}

	wg := sync.WaitGroup{}

	// run test
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = conf.Start(pctx)
		require.NoError(t, err)
	}()

	wgA := sync.WaitGroup{}

	// write some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("first write")
		connA.Close()
		connB.Close()
	}()

	// read some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := make([]byte, 1024)
		n, err := connD.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("first read")
	}()

	// wait for first i/o to complete
	wgA.Wait()

	// let a few no data loops complete
	time.Sleep(time.Second)

	wgB := sync.WaitGroup{}

	// read/write some data
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := []byte("Hello World!")
		_, err := connA.Write(b)
		require.Error(t, err)
		t.Log("second write")
	}()
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := make([]byte, 1024)
		_, err := connD.Read(b)
		require.Error(t, err)
		t.Log("second read")
	}()

	// wait for second i/o to complete
	wgB.Wait()

	// shutdown
	pcancel()

	// wait for shutdown
	wg.Wait()
}

func TestStart_mlat(t *testing.T) {

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.MLAT,
		Logger:             log.Logger,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// update noDataWaitDuration
	origNoDataWaitDuration := noDataWaitDuration
	defer func() { noDataWaitDuration = origNoDataWaitDuration }()
	noDataWaitDuration = time.Millisecond * 250

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// swap out function for testing
	origRegisterConnectionStats := registerConnectionStats
	defer func() { registerConnectionStats = origRegisterConnectionStats }()
	registerConnectionStats = func(conn stats.Connection) error {
		return nil
	}

	// swap out function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return true
	}

	wg := sync.WaitGroup{}

	// run test
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = conf.Start(pctx)
		require.NoError(t, err)
	}()

	wgA := sync.WaitGroup{}

	// write some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("first write")
	}()

	// read some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := make([]byte, 1024)
		n, err := connD.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("first read")
	}()

	// wait for first i/o to complete
	wgA.Wait()

	// let a few no data loops complete
	time.Sleep(time.Second)

	wgB := sync.WaitGroup{}

	// read/write some data
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := []byte("Hello World!")
		n, err := connD.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("second write")
	}()
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := make([]byte, 1024)
		n, err := connA.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("second read")
	}()

	// wait for second i/o to complete
	wgB.Wait()
	time.Sleep(time.Second)

	// shutdown
	pcancel()

	// wait for shutdown
	wg.Wait()
}

func TestStart_mlat_serverReadChan_close(t *testing.T) {

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.MLAT,
		Logger:             log.Logger,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// update noDataWaitDuration
	origNoDataWaitDuration := noDataWaitDuration
	defer func() { noDataWaitDuration = origNoDataWaitDuration }()
	noDataWaitDuration = time.Millisecond * 250

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// swap out function for testing
	origRegisterConnectionStats := registerConnectionStats
	defer func() { registerConnectionStats = origRegisterConnectionStats }()
	registerConnectionStats = func(conn stats.Connection) error {
		return nil
	}

	// swap out function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return true
	}

	wg := sync.WaitGroup{}

	// run test
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = conf.Start(pctx)
		require.NoError(t, err)
	}()

	wgA := sync.WaitGroup{}

	// write some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("first write")
	}()

	// read some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := make([]byte, 1024)
		n, err := connD.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("first read")
	}()

	// wait for first i/o to complete
	wgA.Wait()

	// let a few no data loops complete
	time.Sleep(time.Second)
	connC.Close()
	connD.Close()

	wgB := sync.WaitGroup{}

	// read/write some data
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := []byte("Hello World!")
		_, err := connD.Write(b)
		require.Error(t, err)
		t.Log("second write")
	}()
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := make([]byte, 1024)
		_, err := connA.Read(b)
		require.Error(t, err)
		t.Log("second read")
	}()

	// wait for second i/o to complete
	wgB.Wait()

	// shutdown
	pcancel()

	// wait for shutdown
	wg.Wait()
}

func TestStart_mlat_stats(t *testing.T) {

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})

	connA, connB := net.Pipe()
	defer connA.Close()
	defer connB.Close()

	connC, connD := net.Pipe()
	defer connC.Close()
	defer connD.Close()

	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	pc := &ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	err := Init(pctx, pc)
	require.NoError(t, err, "error initing test environment")
	defer Close(pc)

	conf := ProxyConnection{
		Connection:         connB,
		ConnectionProtocol: feedprotocol.MLAT,
		Logger:             log.Logger,
	}

	fc := feederClient{
		clientApiKey: uuid.New(),
	}

	// update noDataWaitDuration
	origNoDataWaitDuration := noDataWaitDuration
	defer func() { noDataWaitDuration = origNoDataWaitDuration }()
	noDataWaitDuration = time.Millisecond * 250

	// swap out function for testing
	origAuthenticateFeederWrapper := authenticateFeederWrapper
	defer func() { authenticateFeederWrapper = origAuthenticateFeederWrapper }()
	authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
		return fc, nil
	}

	// swap out function for testing
	origStatsGetNumConnections := statsGetNumConnections
	defer func() { statsGetNumConnections = origStatsGetNumConnections }()
	statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) {
		return 0, nil
	}

	// swap out function for testing
	origStartFeedInContainer := startFeedInContainer
	defer func() { startFeedInContainer = origStartFeedInContainer }()
	startFeedInContainer = func(c *containers.FeedInContainer) (containerID string, err error) {
		return "localhost", nil
	}

	// swap out function for testing
	origDialContainerWrapper := dialContainerWrapper
	defer func() { dialContainerWrapper = origDialContainerWrapper }()
	dialContainerWrapper = func(addr string, port int) (c net.Conn, err error) {
		return connC, nil
	}

	// swap out function for testing
	origRegisterConnectionStats := registerConnectionStats
	defer func() { registerConnectionStats = origRegisterConnectionStats }()
	registerConnectionStats = func(conn stats.Connection) error {
		return nil
	}

	// swap out function for testing
	origIsValidApiKeyWrapper := isValidApiKeyWrapper
	defer func() { isValidApiKeyWrapper = origIsValidApiKeyWrapper }()
	isValidApiKeyWrapper = func(clientApiKey uuid.UUID) bool {
		return true
	}

	// swap out function for testing
	origStatsIncrementByteCounters := statsIncrementByteCounters
	defer func() { statsIncrementByteCounters = origStatsIncrementByteCounters }()
	statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, proto feedprotocol.Protocol, bytesIn, bytesOut uint64) error {
		return nil
	}

	wg := sync.WaitGroup{}

	// run test
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = conf.Start(pctx)
		require.NoError(t, err)
	}()

	wgA := sync.WaitGroup{}

	// write some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := []byte("Hello World!")
		n, err := connA.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("first write")
	}()

	// read some data to prevent io timeout
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		b := make([]byte, 1024)
		n, err := connD.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("first read")
	}()

	// wait for first i/o to complete
	wgA.Wait()

	// let a few no data loops complete
	time.Sleep(time.Second * 2)

	wgB := sync.WaitGroup{}

	// read/write some data
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := []byte("Hello World!")
		n, err := connD.Write(b)
		require.NoError(t, err)
		assert.Equal(t, len(b), n)
		t.Log("second write")
	}()
	wgB.Add(1)
	go func() {
		defer wgB.Done()
		b := make([]byte, 1024)
		n, err := connA.Read(b)
		require.NoError(t, err)
		assert.Equal(t, []byte("Hello World!"), b[:n])
		t.Log("second read")
	}()

	// wait for second i/o to complete
	wgB.Wait()

	// shutdown
	pcancel()

	// wait for shutdown
	wg.Wait()
}
