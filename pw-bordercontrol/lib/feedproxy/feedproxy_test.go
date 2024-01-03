package feedproxy

import (
	"errors"
	"net"
	"net/url"
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
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

var (
	// mock feeder details
	TestFeederAPIKey    = uuid.MustParse("6261B9C8-25C1-4B67-A5A2-51FC688E8A25") // not a real feeder api key, generated with uuidgen
	TestFeederLabel     = "Test Feeder 123"
	TestFeederLatitude  = 123.456789
	TestFeederLongitude = 98.765432
	TestFeederMux       = "127.0.0.1"
	TestFeederCode      = "ABCD-1234"
	TestFeederAddr      = net.IPv4(127, 0, 0, 1)
	TestPWIngestSink    = "nats://pw-ingest-sink:12345"
)

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
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})

		t.Run("ProxyConnection.Start", func(t *testing.T) {
			c := ProxyConnection{}
			err := c.Start()
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})

		t.Run("ProxyConnection.Stop", func(t *testing.T) {
			c := ProxyConnection{}
			err := c.Stop()
			assert.Error(t, err)
			assert.Equal(t, ErrNotInitialised.Error(), err.Error())
		})

	})

	t.Run("initialise feedproxy subsystem error", func(t *testing.T) {
		c := FeedProxyConfig{
			UpdateFrequency: time.Second * 10,
			ATCUrl:          "\n", // ASCII control character in URL is invalid
		}
		err := Init(&c)
		assert.Error(t, err)
	})

	feedProxyConf := FeedProxyConfig{
		UpdateFrequency: time.Second * 10,
	}

	t.Run("initialise feedproxy subsystem", func(t *testing.T) {
		err := Init(&feedProxyConf)
		assert.NoError(t, err)
	})

	t.Run("test connection tracker", func(t *testing.T) {
		srcIP := net.IPv4(1, 1, 1, 1)

		i := incomingConnectionTracker{}

		// first connection, should work
		cn, err := GetConnectionNumber()
		assert.NoError(t, err)
		err = i.check(srcIP, cn)
		assert.NoError(t, err)

		// second connection, should work
		cn, err = GetConnectionNumber()
		assert.NoError(t, err)
		err = i.check(srcIP, cn)
		assert.NoError(t, err)

		// third connection, should work
		cn, err = GetConnectionNumber()
		assert.NoError(t, err)
		err = i.check(srcIP, cn)
		assert.NoError(t, err)

		// fourth connection, should fail
		cn, err = GetConnectionNumber()
		assert.NoError(t, err)
		err = i.check(srcIP, cn)
		assert.Error(t, err)

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
		assert.NoError(t, err)
		err = i.check(srcIP, cn)
		assert.NoError(t, err)
	})

	t.Run("test feedersGaugeFunc", func(t *testing.T) {
		assert.Equal(t, float64(1), feedersGaugeFunc())
	})

	t.Run("test isValidApiKey", func(t *testing.T) {
		assert.True(t, isValidApiKey(TestFeederAPIKey))
		assert.False(t, isValidApiKey(uuid.New()))
	})

	t.Run("test getFeederInfo", func(t *testing.T) {
		f := feederClient{clientApiKey: TestFeederAPIKey}
		err := getFeederInfo(&f)
		assert.NoError(t, err)

		f = feederClient{clientApiKey: uuid.New()}
		err = getFeederInfo(&f)
		assert.Error(t, err)
	})

	t.Run("lookupContainerTCP", func(t *testing.T) {
		n, err := lookupContainerTCP("localhost", 8080)
		assert.NoError(t, err)
		assert.Equal(t, "127.0.0.1", n.IP.String())
		assert.Equal(t, 8080, n.Port)

		_, err = lookupContainerTCP("invalid.invalid", 8080)
		assert.Error(t, err)
	})

	t.Run("dialContainerTCP error dialling", func(t *testing.T) {
		l, err := nettest.NewLocalListener("tcp4")
		assert.NoError(t, err)
		ip := strings.Split(l.Addr().String(), ":")[0]
		port, err := strconv.Atoi(strings.Split(l.Addr().String(), ":")[1])
		assert.NoError(t, err)
		err = l.Close()
		assert.NoError(t, err)
		_, err = dialContainerTCP(ip, port)
		assert.Error(t, err)
	})

	t.Run("dialContainerTCP", func(t *testing.T) {

		testData := "Hello World!"

		wg := sync.WaitGroup{}

		// create listener
		listener, err := nettest.NewLocalListener("tcp")
		t.Cleanup(func() {
			listener.Close()
		})
		assert.NoError(t, err)

		// set up test server
		wg.Add(1)
		go func(t *testing.T) {

			buf := make([]byte, len(testData))

			conn, err := listener.Accept()
			assert.NoError(t, err)
			t.Cleanup(func() {
				conn.Close()
			})

			n, err := readFromClient(conn, buf)
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)

			t.Run("authenticateFeeder working", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return TestFeederAPIKey, nil }
				handshakeComplete = func(c net.Conn) bool { return true }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }

				fc, err := authenticateFeeder(conn)
				assert.NoError(t, err)
				assert.Equal(t, TestFeederAPIKey, fc.clientApiKey)
				assert.Equal(t, TestFeederLatitude, fc.refLat)
				assert.Equal(t, TestFeederLongitude, fc.refLon)
				assert.Equal(t, TestFeederMux, fc.mux)
				assert.Equal(t, TestFeederLabel, fc.label)
				assert.Equal(t, TestFeederCode, fc.feederCode)
			})

			t.Run("authenticateFeeder getUUIDfromSNI error", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
					return TestFeederAPIKey, errors.New("injected error for testing")
				}
				handshakeComplete = func(c net.Conn) bool { return true }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }

				_, err = authenticateFeeder(conn)
				assert.Error(t, err)
			})

			t.Run("authenticateFeeder handshakeComplete error", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return TestFeederAPIKey, nil }
				handshakeComplete = func(c net.Conn) bool { return false }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }

				_, err = authenticateFeeder(conn)
				assert.Error(t, err)
			})

			t.Run("authenticateFeeder isValidApiKey error", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) {
					return uuid.New(), nil
				}
				handshakeComplete = func(c net.Conn) bool { return true }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return nil }

				_, err = authenticateFeeder(conn)
				assert.Error(t, err)
			})

			t.Run("authenticateFeeder stats error", func(t *testing.T) {
				// override functions for testing
				getUUIDfromSNI = func(c net.Conn) (u uuid.UUID, err error) { return TestFeederAPIKey, nil }
				handshakeComplete = func(c net.Conn) bool { return true }
				RegisterFeederWithStats = func(f stats.FeederDetails) error { return errors.New("injected error for testing") }

				_, err = authenticateFeeder(conn)
				assert.Error(t, err)
			})

			conn.Close()

			t.Run("readFromClient error", func(t *testing.T) {
				_, err := readFromClient(conn, buf)
				assert.Error(t, err)
			})

			wg.Done()
		}(t)

		ip := strings.Split(listener.Addr().String(), ":")[0]
		port, err := strconv.Atoi(strings.Split(listener.Addr().String(), ":")[1])
		assert.NoError(t, err)

		conn, err := dialContainerTCP(ip, port)
		assert.NoError(t, err)
		t.Cleanup(func() {
			conn.Close()
		})

		n, err := conn.Write([]byte(testData))
		assert.NoError(t, err)
		assert.Equal(t, len(testData), n)

		wg.Wait()

		t.Run("lookupContainerTCP error", func(t *testing.T) {
			_, err := dialContainerTCP("invalid.invalid", 8080)
			assert.Error(t, err)
		})

	})

	// ---
	t.Run("ProxyConnection", func(t *testing.T) {

		// t.Run("error too-frequent incoming connections", func(t *testing.T) {

		// 	wg := sync.WaitGroup{}

		// 	conn1, conn2 := net.Pipe()
		// 	t.Cleanup(func() {
		// 		conn1.Close()
		// 	})
		// 	t.Cleanup(func() {
		// 		conn2.Close()
		// 	})
		// 	conn3, conn4 := net.Pipe()
		// 	t.Cleanup(func() {
		// 		conn3.Close()
		// 	})
		// 	t.Cleanup(func() {
		// 		conn4.Close()
		// 	})

		// 	c1 := ProxyConnection{
		// 		Connection:                  conn1,
		// 		ConnectionProtocol:          feedprotocol.MLAT,
		// 		FeedInContainerPrefix:       "test-feed-in-",
		// 		FeederValidityCheckInterval: time.Second * 5,
		// 	}

		// 	c2 := ProxyConnection{
		// 		Connection:                  conn2,
		// 		ConnectionProtocol:          feedprotocol.MLAT,
		// 		FeedInContainerPrefix:       "test-feed-in-",
		// 		FeederValidityCheckInterval: time.Second * 5,
		// 	}

		// 	c3 := ProxyConnection{
		// 		Connection:                  conn3,
		// 		ConnectionProtocol:          feedprotocol.MLAT,
		// 		FeedInContainerPrefix:       "test-feed-in-",
		// 		FeederValidityCheckInterval: time.Second * 5,
		// 	}

		// 	c4 := ProxyConnection{
		// 		Connection:                  conn4,
		// 		ConnectionProtocol:          feedprotocol.MLAT,
		// 		FeedInContainerPrefix:       "test-feed-in-",
		// 		FeederValidityCheckInterval: time.Second * 5,
		// 	}

		// 	// make connections
		// 	wg.Add(1)
		// 	go func(t *testing.T) {
		// 		_ = c1.Start()
		// 		t.Cleanup(func() {
		// 			c1.Stop()
		// 		})
		// 		wg.Done()
		// 	}(t)
		// 	wg.Add(1)
		// 	go func(t *testing.T) {
		// 		_ = c2.Start()
		// 		t.Cleanup(func() {
		// 			c2.Stop()
		// 		})
		// 		wg.Done()
		// 	}(t)
		// 	wg.Add(1)
		// 	go func(t *testing.T) {
		// 		_ = c3.Start()
		// 		t.Cleanup(func() {
		// 			c3.Stop()
		// 		})
		// 		wg.Done()
		// 	}(t)
		// 	wg.Add(1)
		// 	go func(t *testing.T) {
		// 		err := c4.Start()
		// 		assert.Error(t, err)
		// 		fmt.Println(err)
		// 		t.Cleanup(func() {
		// 			c4.Stop()
		// 		})
		// 		wg.Done()
		// 	}(t)
		// 	wg.Wait()
		// })

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
			statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, bytesIn, bytesOut uint64) error { return nil }
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
			server, err := net.Listen("tcp4", "127.0.0.1:12346")
			assert.NoError(t, err)
			t.Cleanup(func() {
				server.Close()
			})

			// start server
			wg.Add(1)
			go func(t *testing.T) {

				buf := make([]byte, len(testData))

				sconn, err := server.Accept()
				assert.NoError(t, err)
				t.Log("server accepts connection")
				t.Cleanup(func() {
					sconn.Close()
				})

				t.Log("server sets deadline")
				err = sconn.SetDeadline(time.Now().Add(time.Second * 30))
				assert.NoError(t, err)

				n, err := readFromClient(sconn, buf)
				assert.NoError(t, err)
				assert.Equal(t, len(testData), n)
				t.Logf("server read from client: %s", string(buf[:n]))

				n, err = sconn.Write(buf)
				assert.NoError(t, err)
				assert.Equal(t, len(testData), n)
				t.Logf("server write to client: %s", string(buf[:n]))

				testsFinished <- true
				_ = <-stopServer

				sconn.Close()

				wg.Done()

			}(t)

			// create listener
			listener, err := nettest.NewLocalListener("tcp")
			assert.NoError(t, err)
			t.Cleanup(func() {
				listener.Close()
			})

			// start listener
			wg.Add(1)
			go func(t *testing.T) {

				lconn, err := listener.Accept()
				assert.NoError(t, err)
				t.Log("listener accepts connection")
				t.Cleanup(func() {
					lconn.Close()
				})

				err = lconn.SetDeadline(time.Now().Add(time.Second * 30))
				assert.NoError(t, err)
				t.Log("listener sets deadline")

				t.Log("listener GetConnectionNumber")
				connNum, err := GetConnectionNumber()
				assert.NoError(t, err)

				c := ProxyConnection{
					Connection:                  lconn,
					ConnectionProtocol:          feedprotocol.MLAT,
					ConnectionNumber:            connNum,
					FeedInContainerPrefix:       "test-feed-in-",
					FeederValidityCheckInterval: time.Second * 5,
				}
				t.Log("listener starts proxy")
				err = c.Start()
				t.Log("proxy returns")
				assert.NoError(t, err)

				testsFinished <- true
				_ = <-stopListener

				c.Stop()

				wg.Done()

			}(t)

			time.Sleep(time.Second)

			// start client
			t.Log("client dials listener")
			conn, err := net.Dial("tcp", listener.Addr().String())
			assert.NoError(t, err)
			t.Cleanup(func() {
				conn.Close()
			})
			time.Sleep(time.Second)

			err = conn.SetDeadline(time.Now().Add(time.Second * 30))
			assert.NoError(t, err)

			n, err := conn.Write([]byte(testData))
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)
			t.Logf("client writes data: %s", testData)
			time.Sleep(time.Second)

			buf := make([]byte, len(testData))
			n, err = conn.Read(buf)
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)
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
			statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, bytesIn, bytesOut uint64) error { return nil }
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
			assert.NoError(t, err)
			t.Cleanup(func() {
				server.Close()
			})

			// start server
			wg.Add(1)
			go func(t *testing.T) {

				buf := make([]byte, len(testData))

				sconn, err := server.Accept()
				assert.NoError(t, err)
				t.Log("server accepts connection")
				t.Cleanup(func() {
					sconn.Close()
				})

				t.Log("server sets deadline")
				err = sconn.SetDeadline(time.Now().Add(time.Second * 30))
				assert.NoError(t, err)

				n, err := readFromClient(sconn, buf)
				assert.NoError(t, err)
				assert.Equal(t, len(testData), n)
				t.Logf("server read from client: %s", string(buf[:n]))

				testsFinished <- true
				_ = <-stopServer

				sconn.Close()

				wg.Done()

			}(t)

			// create listener
			listener, err := nettest.NewLocalListener("tcp")
			assert.NoError(t, err)
			t.Cleanup(func() {
				listener.Close()
			})

			// start listener
			wg.Add(1)
			go func(t *testing.T) {

				lconn, err := listener.Accept()
				assert.NoError(t, err)
				t.Log("listener accepts connection")
				t.Cleanup(func() {
					lconn.Close()
				})

				err = lconn.SetDeadline(time.Now().Add(time.Second * 30))
				assert.NoError(t, err)
				t.Log("listener sets deadline")

				t.Log("listener GetConnectionNumber")
				connNum, err := GetConnectionNumber()
				assert.NoError(t, err)

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
				t.Log("listener starts proxy")
				err = c.Start()
				t.Log("proxy returns")
				assert.NoError(t, err)

				testsFinished <- true
				_ = <-stopListener

				c.Stop()

				wg.Done()

			}(t)

			time.Sleep(time.Second)

			// start client
			t.Log("client dials listener")
			conn, err := net.Dial("tcp", listener.Addr().String())
			assert.NoError(t, err)
			t.Cleanup(func() {
				conn.Close()
			})
			time.Sleep(time.Second)

			err = conn.SetDeadline(time.Now().Add(time.Second * 30))
			assert.NoError(t, err)

			n, err := conn.Write([]byte(testData))
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)
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

			statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, bytesIn, bytesOut uint64) error { return nil }

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
			assert.NoError(t, err)

			lastAuthCheck := time.Now()

			conf := protocolProxyConfig{
				clientConn:   conn2,
				serverConn:   conn3,
				connNum:      connNum,
				clientApiKey: TestFeederAPIKey,

				mgmt:                        &goRoutineManager{},
				lastAuthCheck:               &lastAuthCheck,
				feederValidityCheckInterval: time.Second * 5,
			}

			wg.Add(1)
			go func(t *testing.T) {
				proxyClientToServer(&conf)
				wg.Done()
			}(t)

			n, err := conn1.Write([]byte(testData))
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)

			buf := make([]byte, len(testData))
			n, err = conn4.Read(buf)
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)
			assert.Equal(t, []byte(testData), buf)

			conf.mgmt.Stop()

			wg.Wait()

		})

	})

	t.Run("proxyServerToClient", func(t *testing.T) {

		t.Run("normal operation", func(t *testing.T) {

			testData := "Hello World!"

			wg := sync.WaitGroup{}

			statsIncrementByteCounters = func(uuid uuid.UUID, connNum uint, bytesIn, bytesOut uint64) error { return nil }

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
			assert.NoError(t, err)

			lastAuthCheck := time.Now()

			conf := protocolProxyConfig{
				clientConn:   conn2,
				serverConn:   conn3,
				connNum:      connNum,
				clientApiKey: TestFeederAPIKey,

				mgmt:                        &goRoutineManager{},
				lastAuthCheck:               &lastAuthCheck,
				feederValidityCheckInterval: time.Second * 5,
			}

			wg.Add(1)
			go func(t *testing.T) {
				proxyServerToClient(&conf)
				wg.Done()
			}(t)

			n, err := conn4.Write([]byte(testData))
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)

			buf := make([]byte, len(testData))
			n, err = conn1.Read(buf)
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)
			assert.Equal(t, []byte(testData), buf)

			conf.mgmt.Stop()

			wg.Wait()

		})

	})

	// ---

	t.Run("GetConnectionNumber", func(t *testing.T) {

		const MaxUint = ^uint(0)

		for n := 0; n <= 100; n++ {
			_, err := GetConnectionNumber()
			assert.NoError(t, err)
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
			assert.NoError(t, err)
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

	t.Run("stop feedproxy subsystem", func(t *testing.T) {
		feedProxyConf.stopMu.Lock()
		feedProxyConf.stop = true
		feedProxyConf.stopMu.Unlock()
		// wait for stop
		time.Sleep(time.Second * 15)
	})

}

func TestGoRoutineManager(t *testing.T) {

	g := goRoutineManager{}

	g.mu.Lock()
	assert.Equal(t, false, g.stop)
	g.mu.Unlock()

	g.Stop()

	g.mu.Lock()
	assert.Equal(t, true, g.stop)
	g.mu.Unlock()

	assert.Equal(t, true, g.CheckForStop())
}
