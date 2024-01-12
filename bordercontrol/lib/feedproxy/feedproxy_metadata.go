package feedproxy

import (
	"net/url"
	"pw_bordercontrol/lib/atc"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rs/zerolog/log"
)

// struct for a list of valid feeders (+ mutex for sync)
type atcFeeders struct {

	// mutex to prevent data race
	mu sync.RWMutex

	// slice of valid feeders
	Feeders []atc.Feeder
}

var (

	// list of valid feeders
	validFeeders atcFeeders
)

// feedersGaugeFunc returns the number of valid feeders.
// Intended to be run as a prometheus.GaugeFunc
func feedersGaugeFunc() float64 {
	validFeeders.mu.RLock()
	defer validFeeders.mu.RUnlock()
	return float64(len(validFeeders.Feeders))
}

// isValidApiKey returns true of api key clientApiKey is a valid feeder in atc
func isValidApiKey(clientApiKey uuid.UUID) bool {
	validFeeders.mu.RLock()
	defer validFeeders.mu.RUnlock()
	for _, v := range validFeeders.Feeders {
		if v.ApiKey == clientApiKey {
			return true
		}
	}
	return false
}

// getFeederInfo returns feeder info from atc, specifically: lat, lon, mux, label & feeder code
// updates *feederClient in-place
func getFeederInfo(f *feederClient) error {
	found := false
	validFeeders.mu.RLock()
	defer validFeeders.mu.RUnlock()
	for _, vf := range validFeeders.Feeders {
		if vf.ApiKey == f.clientApiKey {
			f.refLat = vf.Latitude
			f.refLon = vf.Longitude
			f.mux = vf.Mux
			f.label = vf.Label
			f.feederCode = vf.FeederCode
			found = true
			break
		}
	}
	if !found {
		return ErrFeederNotFound
	}
	return nil
}

// getDataFromATCMu is the mutex for getDataFromATC. Variableised to allow overriding for testing.
var getDataFromATCMu sync.RWMutex

// getDataFromATC is a wrapper for atc.GetFeeders. Variableised to allow overriding for testing.
var getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
	return atc.GetFeeders(&atc.Server{Url: *atcurl, Username: atcuser, Password: atcpass})
}

// updateFeederDB updates validFeeders with data from atc.
// Intended to be run as a goroutine, as it will loop until context cancellation.
func updateFeederDB(conf *ProxyConfig) {

	// update log context
	log := log.With().
		Strs("func", []string{"feeder_meta.go", "updateFeederDB"}).
		Logger()

	// loop until context cancellation
	for {

		// get data from atc
		getDataFromATCMu.RLock()
		f, err := getDataFromATC(
			conf.atcUrl,
			conf.ATCUser,
			conf.ATCPass,
		)
		getDataFromATCMu.RUnlock()
		if err != nil {
			log.Err(err).Msg("error updating feeder cache from atc")
		}

		// get uuids
		var newValidFeeders []uuid.UUID
		count := 0
		for _, v := range f.Feeders {
			newValidFeeders = append(newValidFeeders, v.ApiKey)
			count += 1
		}

		// update validFeeders
		validFeeders.mu.Lock()
		validFeeders.Feeders = f.Feeders
		validFeeders.mu.Unlock()

		log.Debug().Int("feeders", count).Msg("updated feeder cache from atc")

		// either sleep or exit depending on which comes first
		select {

		// wait for conf.UpdateFrequency to pass, or...
		case <-time.After(conf.UpdateFrequency):
			continue

		// wait for context cancellation
		case <-ctx.Done():
			log.Debug().Msg("shutting down updateFeederDB")
			return
		}
	}
}
