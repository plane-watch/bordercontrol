package feedproxy

import (
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

	getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
		f := atc.Feeders{
			Feeders: []atc.Feeder{
				{ApiKey: TestFeederAPIKey},
			},
		}
		return f, nil
	}

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

	t.Run("initialise feedproxy subsystem", func(t *testing.T) {
		c := FeedProxyConfig{
			UpdateFreqency: time.Second * 10,
		}
		err := Init(&c)
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
		time.Sleep(time.Second * 30)

		// run evictor
		i.evict()

		// fourth connection, should now work
		cn, err = GetConnectionNumber()
		assert.NoError(t, err)
		err = i.check(srcIP, cn)
		assert.NoError(t, err)
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
