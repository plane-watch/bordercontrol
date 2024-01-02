package feedproxy

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFeedProxy(t *testing.T) {

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

func TestConnectionTracking(t *testing.T) {

	srcIP := net.IPv4(127, 0, 0, 1)

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

	// third connection, should fail
	cn, err = GetConnectionNumber()
	assert.NoError(t, err)
	err = i.check(srcIP, cn)
	assert.Error(t, err)

}
