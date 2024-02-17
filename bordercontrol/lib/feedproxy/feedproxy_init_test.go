package feedproxy

import (
	"context"
	"os"
	"pw_bordercontrol/lib/stats"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})
}

func TestInit_invalid_url(t *testing.T) {
	initialised = false
	defer func() { initialised = false }()
	defer prometheus.Unregister(promCollectorNumFeeders)

	// prep parent context
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	conf := ProxyConfig{
		ATCUrl: "\n", // control character makes URL fail parsing
	}
	err := Init(pctx, &conf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid control character in URL")
}

func TestInit_already_initialised(t *testing.T) {
	initialised = true
	defer func() { initialised = false }()
	defer prometheus.Unregister(promCollectorNumFeeders)

	// prep parent context
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	conf := ProxyConfig{}
	err := Init(pctx, &conf)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAlreadyInitialised)

}

func TestInit_RegisterPromCollector_error(t *testing.T) {
	defer func() { initialised = false }()
	defer prometheus.Unregister(promCollectorNumFeeders)

	// prep parent context
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	// introduce error
	err := stats.RegisterPromCollector(promCollectorNumFeeders)
	require.NoError(t, err)
	defer prometheus.Unregister(promCollectorNumFeeders)

	conf := ProxyConfig{}

	err = Init(pctx, &conf)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate metrics collector registration attempted")
}

func TestInit_working(t *testing.T) {
	defer func() { initialised = false }()
	defer prometheus.Unregister(promCollectorNumFeeders)

	// prep parent context
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	conf := ProxyConfig{}

	err := Init(pctx, &conf)
	require.NoError(t, err)

	// wait a few seconds for evict loop
	time.Sleep(time.Second * 2)

	// clean up
	defer prometheus.Unregister(promCollectorNumFeeders)
}

func TestClose_ErrNotInitialised(t *testing.T) {
	defer func() { initialised = false }()
	defer prometheus.Unregister(promCollectorNumFeeders)

	conf := ProxyConfig{}
	err := Close(&conf)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotInitialised)
}

func TestClose_UnregisterPromCollector_error(t *testing.T) {
	defer func() { initialised = false }()
	defer prometheus.Unregister(promCollectorNumFeeders)

	// prep parent context
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	conf := ProxyConfig{}

	err := Init(pctx, &conf)
	require.NoError(t, err)

	// introduce error
	prometheus.Unregister(promCollectorNumFeeders)

	err = Close(&conf)

	require.NoError(t, err)

}

func TestClose_working(t *testing.T) {
	defer func() { initialised = false }()
	defer prometheus.Unregister(promCollectorNumFeeders)

	// prep parent context
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()

	conf := ProxyConfig{}

	err := Init(pctx, &conf)
	require.NoError(t, err)

	err = Close(&conf)

	require.NoError(t, err)

}
