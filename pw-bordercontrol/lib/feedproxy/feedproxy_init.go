package feedproxy

import (
	"net/url"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

var (
	initialised   bool
	initialisedMu sync.RWMutex
)

type FeedProxyConfig struct {
	UpdateFreqency time.Duration // how often to refresh allowed feeder DB from ATC
	ATCUrl         string        // ATC API URL
	ATCUser        string        // ATC API Username
	ATCPass        string        // ATC API Password

	stop   bool // set to true to stop goroutine, use mutex below for sync
	stopMu sync.Mutex

	atcUrl *url.URL
}

func Init(c *FeedProxyConfig) error {

	// parse atc url
	var err error
	c.atcUrl, err = url.Parse(c.ATCUrl)
	if err != nil {
		log.Error().Msg("--atcurl is invalid")
		return err
	}

	// start updateFeederDB
	go updateFeederDB(c)

	// prepare incoming connection tracker (to allow dropping too-frequent connections)
	// start evictor for incoming connection tracker
	go func() {
		for {
			incomingConnTracker.evict()
			time.Sleep(time.Second * 1)
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
