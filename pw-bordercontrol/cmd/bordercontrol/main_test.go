package main

import (
	"crypto/sha256"
	"net"
	"os"
	"os/exec"
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

func TestPrepListenerConfig(t *testing.T) {

	t.Run("BEAST", func(t *testing.T) {
		testFeedInContainerPrefix := "test-feed-in-"
		testListenAddr := "1.2.3.4:1234"
		conf := prepListenerConfig(testListenAddr, feedprotocol.BEAST, testFeedInContainerPrefix)
		assert.Equal(t, testFeedInContainerPrefix, conf.feedInContainerPrefix)
		assert.Equal(t, "tcp", conf.listenAddr.Network())
		assert.Equal(t, testListenAddr, conf.listenAddr.String())
	})

	t.Run("MLAT", func(t *testing.T) {
		testFeedInContainerPrefix := "test-feed-in-"
		testListenAddr := "2.3.4.5:2345"
		conf := prepListenerConfig(testListenAddr, feedprotocol.MLAT, testFeedInContainerPrefix)
		assert.Equal(t, testFeedInContainerPrefix, conf.feedInContainerPrefix)
		assert.Equal(t, "tcp", conf.listenAddr.Network())
		assert.Equal(t, testListenAddr, conf.listenAddr.String())
	})

	t.Run("error invalid addr", func(t *testing.T) {

		if os.Getenv("BE_CRASHER") == "1" {
			testListenAddr := "" // invalid
			_ = prepListenerConfig(testListenAddr, feedprotocol.MLAT, "")
		}

		cmd := exec.Command(os.Args[0], "-test.run=TestCrasher")
		cmd.Env = append(os.Environ(), "BE_CRASHER=1")
		err := cmd.Run()
		if e, ok := err.(*exec.ExitError); ok && !e.Success() {
			return
		}
		t.Fatalf("process ran with err %v, want exit status 1", err)

	})
}

func TestLogNumGoroutines(t *testing.T) {
	sc := make(chan bool)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		logNumGoroutines(time.Second, sc)
		wg.Done()
	}()
	time.Sleep(time.Second * 2)
	sc <- true
	wg.Wait()
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

	// bypass feederproxy as tested elsewhere
	feedproxyGetConnectionNumberWrapper = func() (num uint, err error) {
		return 42, nil
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
		err := listener(&conf)
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
