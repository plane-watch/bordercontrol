package main

import (
	"crypto/sha256"
	"net"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/feedproxy"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

func getListenableAddress(t *testing.T) (tempTcpAddr string) {
	// get testing host/port
	n, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err, "could not generate new local listener for test")
	tempTcpAddr = n.Addr().String()
	err = n.Close()
	assert.NoError(t, err, "could not close temp local listener for test")
	return tempTcpAddr
}

func TestDFWTB(t *testing.T) {
	// don't mess with the banner!
	bannerCheckSum := sha256.Sum256([]byte(banner))
	expectedCheckSum := [32]uint8{28, 39, 253, 10, 28, 108, 170, 133, 71, 150, 147, 107, 235, 39, 187, 141, 112, 229, 54, 58, 2, 39, 205, 10, 136, 172, 42, 112, 13, 56, 182, 97}
	assert.Equal(t, expectedCheckSum, bannerCheckSum, "don't mess with the banner! :-)")
}

func TestGetRepoInfo(t *testing.T) {
	ch, ct := getRepoInfo()

	// return unknown during testing
	assert.Equal(t, "unknown", ch)
	assert.Equal(t, "unknown", ct)
}

func TestListener(t *testing.T) {

	// bypass stunnel stuff as tested elsewhere
	stunnelNewListenerWrapper = func(network, laddr string) (l net.Listener, err error) {
		return net.Listen(network, laddr)
	}

	// bypass proxying as tested elsewhere
	proxyConnStartWrapper = func(f *feedproxy.ProxyConnection) error {
		return nil
	}

	wg := sync.WaitGroup{}

	stopListener := make(chan bool)

	l, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err)

	ip := strings.Split(l.Addr().String(), ":")[0]
	port, err := strconv.Atoi(strings.Split(l.Addr().String(), ":")[1])
	assert.NoError(t, err)

	l.Close()

	addr := net.TCPAddr{
		IP:   net.ParseIP(ip),
		Port: port,
	}

	conf := listenConfig{
		listenProto:           feedprotocol.MLAT,
		listenAddr:            addr,
		feedInContainerPrefix: "test-feed-in-",
	}

	// start listener
	wg.Add(1)
	go func(t *testing.T) {
		err = listener(&conf)
		assert.NoError(t, err)
		_ = <-stopListener
		wg.Done()
	}(t)

	time.Sleep(time.Second)

	// stop after this connection
	conf.stopMu.Lock()
	conf.stop = true
	conf.stopMu.Unlock()

	// connect
	clientConn, err := net.Dial("tcp", l.Addr().String())
	assert.NoError(t, err)
	time.Sleep(time.Second)

	// close connection
	err = clientConn.Close()
	stopListener <- true

	wg.Wait()
}
