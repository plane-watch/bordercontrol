package feedproxy

import (
	"errors"
	"net"
	"net/url"
	"pw_bordercontrol/lib/atc"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

var (
	// mock feeder details
	TestFeederAPIKey    = uuid.MustParse("6261B9C8-25C1-4B67-A5A2-51FC688E8A25") // not a real feeder api key, generated with uuidgen
	TestFeederLabel     = "Test Feeder 123"
	TestFeederLatitude  = 123.456789
	TestFeederLongitude = 98.765432
	TestFeederMux       = "test-mux"
	TestFeederCode      = "ABCD-1234"
	TestFeederAddr      = net.IPv4(127, 0, 0, 1)
	TestPWIngestSink    = "nats://pw-ingest-sink:12345"
)

func TestFeedProxy(t *testing.T) {

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
			UpdateFreqency: time.Second * 10,
			ATCUrl:         "\n", // ASCII control character in URL is invalid
		}
		err := Init(&c)
		assert.Error(t, err)
	})

	feedProxyConf := FeedProxyConfig{
		UpdateFreqency: time.Second * 10,
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
	})

	// ---

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
