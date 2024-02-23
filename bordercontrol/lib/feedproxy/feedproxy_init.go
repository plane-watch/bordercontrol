// Package feedproxy contains the proxying implementation for bordercontrol.

package feedproxy

import (
	"context"
	"net/url"
	"pw_bordercontrol/lib/atc"
	"pw_bordercontrol/lib/stats"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

var (

	// set to true when Init() has been run
	initialised bool

	// mutex for initialised
	initialisedMu sync.RWMutex

	// client struct for ATC API calls
	atcClient *atc.Client

	// context for this package
	ctx context.Context

	// cancel function for this package's context
	cancelCtx context.CancelFunc

	// waitgroup for goroutines started in this package
	wg sync.WaitGroup

	// prep prom collector for valid feeders
	promCollectorNumFeeders = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: stats.PromNamespace,
		Subsystem: stats.PromSubsystem,
		Name:      "feeders",
		Help:      "The total number of feeders configured in ATC (active and inactive).",
	},
		feedersGaugeFunc)
)

// ProxyConfig holds the configuration for the proxy
type ProxyConfig struct {

	// How often to refresh allowed feeder DB from ATC
	UpdateFrequency time.Duration

	// ATC API URL
	ATCUrl string

	// ATC API Username
	ATCUser string

	// ATC API Password
	ATCPass string

	atcUrl *url.URL
}

// Init initialises the proxy subsystem. It must be run prior to accepting connections.
// parentContext holds a context that can be used to perform clean shutdown of associated goroutines.
func Init(parentContext context.Context, conf *ProxyConfig) error {

	var err error

	if isInitialised() {
		return ErrAlreadyInitialised
	}

	// prep globals
	incomingConnTracker = incomingConnectionTracker{}
	validFeeders = atcFeeders{}

	// prep context
	ctx, cancelCtx = context.WithCancel(parentContext)

	// prep ATC connection
	atcClient, err = atc.NewClientWithContext(parentContext, conf.ATCUrl, conf.ATCUser, conf.ATCPass)
	if err != nil {
		log.Err(err).Msg("error creating ATC client")
		return err
	}

	// start updateFeederDB
	wg.Add(1)
	go func() {
		defer wg.Done()
		updateFeederDB(conf)
	}()

	err = stats.RegisterPromCollector(promCollectorNumFeeders)
	if err != nil {
		log.Err(err).Msg("could not register prom collector")
		return err
	}

	// prepare incoming connection tracker (to allow dropping too-frequent connections)
	// start evictor for incoming connection tracker
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			incomingConnTracker.evict()
			select {
			case <-time.After(time.Second):
				continue
			case <-ctx.Done():
				log.Debug().Msg("shutting down evictor for incoming connection tracker")
				return
			}
		}
	}()

	// set initialised
	func() {
		initialisedMu.Lock()
		defer initialisedMu.Unlock()
		initialised = true
	}()

	return nil
}

// Close shuts down the proxy subsystem.
func Close(conf *ProxyConfig) error {

	// Must be initialised to run
	if !isInitialised() {
		return ErrNotInitialised
	}

	// cancel context
	cancelCtx()

	// wait for goroutines to finish up
	wg.Wait()

	// unregister prom collectors
	err := stats.UnregisterPromCollector(promCollectorNumFeeders)
	if err != nil {
		log.Err(err).Msg("cannot unregister prom collector")
	}

	// reset initialised
	initialisedMu.Lock()
	defer initialisedMu.Unlock()
	initialised = false

	return nil
}

// isInitialised returns true if Init() has been run
func isInitialised() bool {
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}
