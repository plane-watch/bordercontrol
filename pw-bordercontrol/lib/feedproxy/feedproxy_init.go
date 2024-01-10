package feedproxy

import (
	"context"
	"net/url"
	"pw_bordercontrol/lib/stats"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

var (
	initialised   bool
	initialisedMu sync.RWMutex

	ctx       context.Context
	cancelCtx context.CancelFunc
	wg        sync.WaitGroup

	// prep prom collector for valid feeders
	promCollectorNumFeeders = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: stats.PromNamespace,
		Subsystem: stats.PromSubsystem,
		Name:      "feeders",
		Help:      "The total number of feeders configured in ATC (active and inactive).",
	},
		feedersGaugeFunc)
)

type FeedProxyConfig struct {
	UpdateFrequency time.Duration // how often to refresh allowed feeder DB from ATC
	ATCUrl          string        // ATC API URL
	ATCUser         string        // ATC API Username
	ATCPass         string        // ATC API Password

	atcUrl *url.URL
}

func Init(parentContext context.Context, conf *FeedProxyConfig) error {

	if isInitialised() {
		return ErrAlreadyInitialised
	}

	// prep globals
	incomingConnTracker = incomingConnectionTracker{}
	validFeeders = atcFeeders{}

	// prep context
	ctx, cancelCtx = context.WithCancel(parentContext)

	// parse atc url
	var err error
	conf.atcUrl, err = url.Parse(conf.ATCUrl)
	if err != nil {
		log.Error().Msg("--atcurl is invalid")
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

func Close(conf *FeedProxyConfig) error {

	if !isInitialised() {
		return ErrNotInitialised
	}

	// cancel context
	cancelCtx()

	// wait for goroutines to finish up
	wg.Wait()

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

func isInitialised() bool {
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}
