package feedproxy

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"pw_bordercontrol/lib/atc"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/stats"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
)

var (
	// mock feeder details
	TestFeederAPIKey    = uuid.New()
	TestFeederLabel     = "Test Feeder 123"
	TestFeederLatitude  = 123.456789
	TestFeederLongitude = 98.765432
	TestFeederMux       = "127.0.0.1"
	TestFeederCode      = "ABCD-1234"
	TestFeederAddr      = net.IPv4(127, 0, 0, 1)
	TestPWIngestSink    = "nats://pw-ingest-sink:12345"
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})
}

func TestFeedProxy(t *testing.T) {

	// set logging to trace level
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	getDataFromATCMu.Lock()
	getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
		f := atc.Feeders{
			Feeders: []atc.Feeder{
				{
					ApiKey:     TestFeederAPIKey,
					Latitude:   TestFeederLatitude,
					Longitude:  TestFeederLongitude,
					Mux:        TestFeederMux,
					Label:      TestFeederLabel,
					FeederCode: TestFeederCode,
				},
			},
		}
		return f, nil
	}
	getDataFromATCMu.Unlock()

	t.Run("test not initialised", func(t *testing.T) {

		t.Run("GetConnectionNumber", func(t *testing.T) {
			_, err := GetConnectionNumber()
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})

		t.Run("ProxyConnection.Start", func(t *testing.T) {
			ctx := context.Background()
			c := ProxyConnection{}
			err := c.Start(ctx)
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})

		t.Run("ProxyConnection.Stop", func(t *testing.T) {
			c := ProxyConnection{}
			err := c.Stop()
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})

	})

	pctx := context.Background()

	t.Run("initialise feedproxy subsystem error", func(t *testing.T) {
		c := ProxyConfig{
			UpdateFrequency: time.Second * 10,
			ATCUrl:          "\n", // ASCII control character in URL is invalid
		}
		err := Init(pctx, &c)
		require.Error(t, err)
	})

	feedProxyConf := ProxyConfig{
		UpdateFrequency: time.Second * 10,
	}

	t.Run("initialise feedproxy subsystem", func(t *testing.T) {
		err := Init(pctx, &feedProxyConf)
		require.NoError(t, err)
	})

	t.Run("test connection tracker", func(t *testing.T) {
		srcIP := net.IPv4(1, 1, 1, 1)

		i := incomingConnectionTracker{}

		// first connection, should work
		cn, err := GetConnectionNumber()
		require.NoError(t, err)
		err = i.check(srcIP, cn)
		require.NoError(t, err)

		// second connection, should work
		cn, err = GetConnectionNumber()
		require.NoError(t, err)
		err = i.check(srcIP, cn)
		require.NoError(t, err)

		// third connection, should work
		cn, err = GetConnectionNumber()
		require.NoError(t, err)
		err = i.check(srcIP, cn)
		require.NoError(t, err)

		// fourth connection, should fail
		cn, err = GetConnectionNumber()
		require.NoError(t, err)
		err = i.check(srcIP, cn)
		require.Error(t, err)

		// wait for evictor
		time.Sleep(time.Second * 15)

		// add connection to evict
		incomingConnTracker.mu.Lock()
		c := incomingConnection{
			connNum:  10,
			connTime: time.Now().Add(-time.Minute),
		}
		incomingConnTracker.connections = append(incomingConnTracker.connections, c)
		incomingConnTracker.mu.Unlock()

		// run evictor
		i.evict()

		// fourth connection, should now work
		cn, err = GetConnectionNumber()
		require.NoError(t, err)
		err = i.check(srcIP, cn)
		require.NoError(t, err)
	})

	t.Run("test feedersGaugeFunc", func(t *testing.T) {
		require.Equal(t, float64(1), feedersGaugeFunc())
	})

	t.Run("test isValidApiKey", func(t *testing.T) {
		require.True(t, isValidApiKey(TestFeederAPIKey))
		require.False(t, isValidApiKey(uuid.New()))
	})

	t.Run("test getFeederInfo", func(t *testing.T) {
		f := feederClient{clientApiKey: TestFeederAPIKey}
		err := getFeederInfo(&f)
		require.NoError(t, err)

		f = feederClient{clientApiKey: uuid.New()}
		err = getFeederInfo(&f)
		require.Error(t, err)
	})

	t.Run("lookupContainerTCP", func(t *testing.T) {
		n, err := lookupContainerTCP("localhost", 8080)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1", n.IP.String())
		require.Equal(t, 8080, n.Port)

		_, err = lookupContainerTCP("invalid.invalid", 8080)
		require.Error(t, err)
	})

	t.Run("dialContainerTCP error dialling", func(t *testing.T) {
		l, err := nettest.NewLocalListener("tcp4")
		require.NoError(t, err)
		ip := strings.Split(l.Addr().String(), ":")[0]
		port, err := strconv.Atoi(strings.Split(l.Addr().String(), ":")[1])
		require.NoError(t, err)
		err = l.Close()
		require.NoError(t, err)
		_, err = dialContainerTCP(ip, port)
		require.Error(t, err)
	})

	t.Run("dialContainerTCP", func(t *testing.T) {

		testData := "Hello World!"

		wg := sync.WaitGroup{}

		// create listener
		listener, err := nettest.NewLocalListener("tcp")
		t.Cleanup(func() {
			listener.Close()
		})
		require.NoError(t, err)

		// set up test server
		wg.Add(1)
		go func(t *testing.T) {

			buf := make([]byte, len(testData))

			conn, err := listener.Accept()
			require.NoError(t, err)
			t.Cleanup(func() {
				conn.Close()
			})

			n, err := readFromClient(conn, buf)
			require.NoError(t, err)
			require.Equal(t, len(testData), n)

			t.Run("authenticateFeeder working", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return TestFeederAPIKey, nil }
				handshakeComplete = func(c net.Conn) bool { return true }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }

				fc, err := authenticateFeeder(conn)
				require.NoError(t, err)
				require.Equal(t, TestFeederAPIKey, fc.clientApiKey)
				require.Equal(t, TestFeederLatitude, fc.refLat)
				require.Equal(t, TestFeederLongitude, fc.refLon)
				require.Equal(t, TestFeederMux, fc.mux)
				require.Equal(t, TestFeederLabel, fc.label)
				require.Equal(t, TestFeederCode, fc.feederCode)
			})

			t.Run("authenticateFeeder getUUIDfromSNI error", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
					return TestFeederAPIKey, errors.New("injected error for testing")
				}
				handshakeComplete = func(c net.Conn) bool { return true }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }

				_, err = authenticateFeeder(conn)
				require.Error(t, err)
			})

			t.Run("authenticateFeeder handshakeComplete error", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return TestFeederAPIKey, nil }
				handshakeComplete = func(c net.Conn) bool { return false }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }

				_, err = authenticateFeeder(conn)
				require.Error(t, err)
			})

			t.Run("authenticateFeeder isValidApiKey error", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
					return uuid.New(), nil
				}
				handshakeComplete = func(c net.Conn) bool { return true }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }

				_, err = authenticateFeeder(conn)
				require.Error(t, err)
			})

			t.Run("authenticateFeeder stats error", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return TestFeederAPIKey, nil }
				handshakeComplete = func(c net.Conn) bool { return true }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return errors.New("injected error for testing") }

				_, err = authenticateFeeder(conn)
				require.Error(t, err)
			})

			conn.Close()

			t.Run("readFromClient error", func(t *testing.T) {
				_, err := readFromClient(conn, buf)
				require.Error(t, err)
			})

			wg.Done()
		}(t)

		ip := strings.Split(listener.Addr().String(), ":")[0]
		port, err := strconv.Atoi(strings.Split(listener.Addr().String(), ":")[1])
		require.NoError(t, err)

		conn, err := dialContainerTCP(ip, port)
		require.NoError(t, err)
		t.Cleanup(func() {
			conn.Close()
		})

		n, err := conn.Write([]byte(testData))
		require.NoError(t, err)
		require.Equal(t, len(testData), n)

		wg.Wait()

		t.Run("lookupContainerTCP error", func(t *testing.T) {
			_, err := dialContainerTCP("invalid.invalid", 8080)
			require.Error(t, err)
		})

	})

	// ---
	t.Run("ProxyConnection", func(t *testing.T) {

		t.Run("MLAT", func(t *testing.T) {

			stopListener := make(chan bool)
			stopServer := make(chan bool)
			testsFinished := make(chan bool)

			testData := "Hello World!"

			wg := sync.WaitGroup{}

			// override functions for testing
			getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return TestFeederAPIKey, nil }
			handshakeComplete = func(c net.Conn) bool { return true }
			RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }
			registerConnectionStats = func(conn stats.Connection) error { return nil }
			unregisterConnectionStats = func(conn stats.Connection) error { return nil }
			statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) { return 0, nil }
			statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, proto feedprotocol.Protocol, bytesIn, bytesOut uint64) error {
				return nil
			}
			authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
				return feederClient{
					clientApiKey: TestFeederAPIKey,
					refLat:       TestFeederLatitude,
					refLon:       TestFeederLongitude,
					mux:          TestFeederMux,
					label:        TestFeederLabel,
					feederCode:   TestFeederCode,
				}, nil
			}

			// get addr for testing
			tmpListener, err := nettest.NewLocalListener("tcp4")
			require.NoError(t, err)
			tmpListener.Close()

			// create server
			server, err := net.Listen("tcp4", tmpListener.Addr().String())
			require.NoError(t, err)
			t.Cleanup(func() {
				server.Close()
			})

			// start server
			wg.Add(1)
			go func(t *testing.T) {

				buf := make([]byte, len(testData))

				sconn, err := server.Accept()
				require.NoError(t, err)
				t.Log("server accepts connection")
				t.Cleanup(func() {
					sconn.Close()
				})

				t.Log("server sets deadline")
				err = sconn.SetDeadline(time.Now().Add(time.Second * 30))
				require.NoError(t, err)

				n, err := readFromClient(sconn, buf)
				require.NoError(t, err)
				require.Equal(t, len(testData), n)
				t.Logf("server read from client: %s", string(buf[:n]))

				n, err = sconn.Write(buf)
				require.NoError(t, err)
				require.Equal(t, len(testData), n)
				t.Logf("server write to client: %s", string(buf[:n]))

				testsFinished <- true
				_ = <-stopServer

				sconn.Close()

				wg.Done()

			}(t)

			// create listener
			listener, err := nettest.NewLocalListener("tcp")
			require.NoError(t, err)
			t.Cleanup(func() {
				listener.Close()
			})

			// start listener
			wg.Add(1)
			go func(t *testing.T) {

				lconn, err := listener.Accept()
				require.NoError(t, err)
				t.Log("listener accepts connection")
				t.Cleanup(func() {
					lconn.Close()
				})

				err = lconn.SetDeadline(time.Now().Add(time.Second * 30))
				require.NoError(t, err)
				t.Log("listener sets deadline")

				t.Log("listener GetConnectionNumber")
				connNum, err := GetConnectionNumber()
				require.NoError(t, err)

				port, err := strconv.Atoi(strings.Split(tmpListener.Addr().String(), ":")[1])
				require.NoError(t, err)

				c := ProxyConnection{
					Connection:                  lconn,
					ConnectionProtocol:          feedprotocol.MLAT,
					ConnectionNumber:            connNum,
					FeedInContainerPrefix:       "test-feed-in-",
					FeederValidityCheckInterval: time.Second * 5,
					InnerConnectionPort:         port,
				}

				ctx := context.Background()

				t.Log("listener starts proxy")
				err = c.Start(ctx)

				t.Log("proxy returns")
				require.NoError(t, err)

				testsFinished <- true
				_ = <-stopListener

				c.Stop()

				wg.Done()

			}(t)

			time.Sleep(time.Second)

			// start client
			t.Log("client dials listener")
			conn, err := net.Dial("tcp", listener.Addr().String())
			require.NoError(t, err)
			t.Cleanup(func() {
				conn.Close()
			})
			time.Sleep(time.Second)

			err = conn.SetDeadline(time.Now().Add(time.Second * 30))
			require.NoError(t, err)

			n, err := conn.Write([]byte(testData))
			require.NoError(t, err)
			require.Equal(t, len(testData), n)
			t.Logf("client writes data: %s", testData)
			time.Sleep(time.Second)

			buf := make([]byte, len(testData))
			n, err = conn.Read(buf)
			require.NoError(t, err)
			require.Equal(t, len(testData), n)
			t.Logf("client reads data: %s", string(buf[:n]))
			time.Sleep(time.Second)

			// allow feeder validity check to happen
			t.Log("waiting for feeder validity check")
			time.Sleep(time.Second * 10)

			// make feeder invalid
			t.Log("making feeder invalid")
			getDataFromATCMu.Lock()
			getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
				f := atc.Feeders{
					Feeders: []atc.Feeder{},
				}
				return f, nil
			}
			getDataFromATCMu.Unlock()

			// allow feeder validity check to happen
			t.Log("waiting for feeder validity check")
			time.Sleep(time.Second * 10)

			// todo - check feeder connection no longer working / valid

			// wait for all tests
			_ = <-testsFinished
			_ = <-testsFinished

			// clean up
			stopServer <- true
			stopListener <- true
			wg.Wait()

		})

		// restore original function
		getDataFromATCMu.Lock()
		getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
			f := atc.Feeders{
				Feeders: []atc.Feeder{
					{
						ApiKey:     TestFeederAPIKey,
						Latitude:   TestFeederLatitude,
						Longitude:  TestFeederLongitude,
						Mux:        TestFeederMux,
						Label:      TestFeederLabel,
						FeederCode: TestFeederCode,
					},
				},
			}
			return f, nil
		}
		getDataFromATCMu.Unlock()

		t.Run("BEAST", func(t *testing.T) {

			stopListener := make(chan bool)
			stopServer := make(chan bool)
			testsFinished := make(chan bool)

			testData := "Hello World!"

			wg := sync.WaitGroup{}

			// override functions for testing
			getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return TestFeederAPIKey, nil }
			handshakeComplete = func(c net.Conn) bool { return true }
			RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }
			registerConnectionStats = func(conn stats.Connection) error { return nil }
			unregisterConnectionStats = func(conn stats.Connection) error { return nil }
			statsGetNumConnections = func(uuid uuid.UUID, proto feedprotocol.Protocol) (int, error) { return 0, nil }
			statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, proto feedprotocol.Protocol, bytesIn, bytesOut uint64) error {
				return nil
			}
			authenticateFeederWrapper = func(connIn net.Conn) (clientDetails feederClient, err error) {
				return feederClient{
					clientApiKey: TestFeederAPIKey,
					refLat:       TestFeederLatitude,
					refLon:       TestFeederLongitude,
					mux:          TestFeederMux,
					label:        TestFeederLabel,
					feederCode:   TestFeederCode,
				}, nil
			}

			// create server
			server, err := nettest.NewLocalListener("tcp4")
			require.NoError(t, err)
			t.Cleanup(func() {
				server.Close()
			})

			// start server
			wg.Add(1)
			go func(t *testing.T) {

				buf := make([]byte, len(testData))

				sconn, err := server.Accept()
				require.NoError(t, err)
				t.Log("server accepts connection")
				t.Cleanup(func() {
					sconn.Close()
				})

				t.Log("server sets deadline")
				err = sconn.SetDeadline(time.Now().Add(time.Second * 30))
				require.NoError(t, err)

				n, err := readFromClient(sconn, buf)
				require.NoError(t, err)
				require.Equal(t, len(testData), n)
				t.Logf("server read from client: %s", string(buf[:n]))

				testsFinished <- true
				_ = <-stopServer

				sconn.Close()

				wg.Done()

			}(t)

			// create listener
			listener, err := nettest.NewLocalListener("tcp")
			require.NoError(t, err)
			t.Cleanup(func() {
				listener.Close()
			})

			// start listener
			wg.Add(1)
			go func(t *testing.T) {

				lconn, err := listener.Accept()
				require.NoError(t, err)
				t.Log("listener accepts connection")
				t.Cleanup(func() {
					lconn.Close()
				})

				err = lconn.SetDeadline(time.Now().Add(time.Second * 30))
				require.NoError(t, err)
				t.Log("listener sets deadline")

				t.Log("listener GetConnectionNumber")
				connNum, err := GetConnectionNumber()
				require.NoError(t, err)

				// redirect connection to feed-in container to test server
				dialContainerTCPWrapper = func(addr string, port int) (c *net.TCPConn, err error) {
					addr = strings.Split(server.Addr().String(), ":")[0]
					port, err = strconv.Atoi(strings.Split(server.Addr().String(), ":")[1])
					if err != nil {
						return c, err
					}
					return dialContainerTCP(addr, port)
				}

				c := ProxyConnection{
					Connection:                  lconn,
					ConnectionProtocol:          feedprotocol.BEAST,
					ConnectionNumber:            connNum,
					FeedInContainerPrefix:       "test-feed-in-",
					FeederValidityCheckInterval: time.Second * 5,
				}

				ctx := context.Background()

				t.Log("listener starts proxy")
				err = c.Start(ctx)
				t.Log("proxy returns")
				require.NoError(t, err)

				testsFinished <- true
				_ = <-stopListener

				c.Stop()

				wg.Done()

			}(t)

			time.Sleep(time.Second)

			// start client
			t.Log("client dials listener")
			conn, err := net.Dial("tcp", listener.Addr().String())
			require.NoError(t, err)
			t.Cleanup(func() {
				conn.Close()
			})
			time.Sleep(time.Second)

			err = conn.SetDeadline(time.Now().Add(time.Second * 30))
			require.NoError(t, err)

			n, err := conn.Write([]byte(testData))
			require.NoError(t, err)
			require.Equal(t, len(testData), n)
			t.Logf("client writes data: %s", testData)
			time.Sleep(time.Second)

			// allow feeder validity check to happen
			t.Log("waiting for feeder validity check")
			time.Sleep(time.Second * 10)

			// make feeder invalid
			t.Log("making feeder invalid")
			getDataFromATCMu.Lock()
			getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
				f := atc.Feeders{
					Feeders: []atc.Feeder{},
				}
				return f, nil
			}
			getDataFromATCMu.Unlock()

			// allow feeder validity check to happen
			t.Log("waiting for feeder validity check")
			time.Sleep(time.Second * 10)

			// todo - check feeder connection no longer working / valid

			// wait for all tests
			_ = <-testsFinished
			_ = <-testsFinished

			// clean up
			stopServer <- true
			stopListener <- true
			wg.Wait()

		})

	})

	t.Run("proxyClientToServer", func(t *testing.T) {

		t.Run("normal operation", func(t *testing.T) {

			testData := "Hello World!"

			wg := sync.WaitGroup{}

			statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, proto feedprotocol.Protocol, bytesIn, bytesOut uint64) error {
				return nil
			}

			conn1, conn2 := net.Pipe()
			conn3, conn4 := net.Pipe()
			t.Cleanup(func() {
				conn1.Close()
			})
			t.Cleanup(func() {
				conn2.Close()
			})
			t.Cleanup(func() {
				conn3.Close()
			})
			t.Cleanup(func() {
				conn4.Close()
			})

			connNum, err := GetConnectionNumber()
			require.NoError(t, err)

			ctx := context.Background()

			lastAuthCheck := time.Now()

			conf := protocolProxyConfig{
				clientConn:   conn2,
				serverConn:   conn3,
				connNum:      connNum,
				clientApiKey: TestFeederAPIKey,
				ctx:          ctx,

				lastAuthCheck:               &lastAuthCheck,
				feederValidityCheckInterval: time.Second * 5,
			}

			wg.Add(1)
			go func(t *testing.T) {
				protocolProxy(&conf, clientToServer)
				wg.Done()
			}(t)

			n, err := conn1.Write([]byte(testData))
			require.NoError(t, err)
			require.Equal(t, len(testData), n)

			buf := make([]byte, len(testData))
			n, err = conn4.Read(buf)
			require.NoError(t, err)
			require.Equal(t, len(testData), n)
			require.Equal(t, []byte(testData), buf)

			wg.Wait()

		})

	})

	t.Run("proxyServerToClient", func(t *testing.T) {

		t.Run("normal operation", func(t *testing.T) {

			testData := "Hello World!"

			wg := sync.WaitGroup{}

			statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, proto feedprotocol.Protocol, bytesIn, bytesOut uint64) error {
				return nil
			}

			conn1, conn2 := net.Pipe()
			conn3, conn4 := net.Pipe()
			t.Cleanup(func() {
				conn1.Close()
			})
			t.Cleanup(func() {
				conn2.Close()
			})
			t.Cleanup(func() {
				conn3.Close()
			})
			t.Cleanup(func() {
				conn4.Close()
			})

			connNum, err := GetConnectionNumber()
			require.NoError(t, err)

			lastAuthCheck := time.Now()

			ctx := context.Background()

			conf := protocolProxyConfig{
				clientConn:   conn2,
				serverConn:   conn3,
				connNum:      connNum,
				clientApiKey: TestFeederAPIKey,

				lastAuthCheck:               &lastAuthCheck,
				feederValidityCheckInterval: time.Second * 5,

				ctx: ctx,
			}

			wg.Add(1)
			go func(t *testing.T) {
				protocolProxy(&conf, serverToClient)
				wg.Done()
			}(t)

			n, err := conn4.Write([]byte(testData))
			require.NoError(t, err)
			require.Equal(t, len(testData), n)

			buf := make([]byte, len(testData))
			n, err = conn1.Read(buf)
			require.NoError(t, err)
			require.Equal(t, len(testData), n)
			require.Equal(t, []byte(testData), buf)

			wg.Wait()

		})

	})

	// ---

	t.Run("GetConnectionNumber", func(t *testing.T) {

		const MaxUint = ^uint(0)

		for n := 0; n <= 100; n++ {
			_, err := GetConnectionNumber()
			require.NoError(t, err)
		}

		incomingConnTracker.mu.Lock()
		c1 := incomingConnection{
			connNum:  20,
			connTime: time.Now().Add(-time.Minute),
		}
		c2 := incomingConnection{
			connNum:  25,
			connTime: time.Now().Add(time.Minute),
		}
		incomingConnTracker.connections = append(incomingConnTracker.connections, c1)
		incomingConnTracker.connections = append(incomingConnTracker.connections, c2)
		incomingConnTracker.connectionNumber = MaxUint - 50
		incomingConnTracker.mu.Unlock()

		for n := 0; n <= 100; n++ {
			_, err := GetConnectionNumber()
			require.NoError(t, err)
		}
	})

	t.Run("getDataFromATC error", func(t *testing.T) {
		getDataFromATCMu.Lock()
		getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
			return atc.Feeders{}, errors.New("injected error for testing")
		}
		getDataFromATCMu.Unlock()
		// wait for error
		time.Sleep(time.Second * 15)
	})

	t.Run("Close", func(t *testing.T) {
		err := Close(&feedProxyConf)
		require.NoError(t, err)
	})

}
