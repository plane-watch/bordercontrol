package main

import (
	"errors"
	"net/url"
	"pw_bordercontrol/lib/atc"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
)

// struct for a list of valid feeder uuids (+ mutex for sync)
type atcFeeders struct {
	mu      sync.RWMutex
	Feeders []atc.Feeder
}

var (
	validFeeders atcFeeders // list of valid feeders
)

func isValidApiKey(clientApiKey uuid.UUID) bool {
	// return true of api key clientApiKey is a valid feeder in atc
	validFeeders.mu.RLock()
	defer validFeeders.mu.RUnlock()
	for _, v := range validFeeders.Feeders {
		if v.ApiKey == clientApiKey {
			return true
		}
	}
	return false
}

func getFeederInfo(f feederClient) error {
	// return feeder info from atc, specifically: lat, lon, mux and label
	found := false
	validFeeders.mu.RLock()
	defer validFeeders.mu.RUnlock()
	for _, v := range validFeeders.Feeders {
		if v.ApiKey == f.clientApiKey {
			f.refLat = v.Latitude
			f.refLon = v.Longitude
			f.mux = v.Mux
			f.label = v.Label
			f.feederCode = v.FeederCode
			found = true
			break
		}
	}
	if !found {
		return errors.New("could not find feeder")
	}
	return nil
}

func updateFeederDB(ctx *cli.Context, updateFreq time.Duration) {
	// updates validFeeders with data from atc

	log := log.With().
		Strs("func", []string{"feeder_meta.go", "updateFeederDB"}).
		Logger()

	firstRun := true

	for {

		// sleep for updateFreq
		if !firstRun {
			time.Sleep(updateFreq)
		} else {
			firstRun = false
		}

		// get data from atc
		atcUrl, err := url.Parse(ctx.String("atcurl"))
		if err != nil {
			log.Error().Msg("--atcurl is invalid")
			continue
		}
		s := atc.Server{
			Url:      *atcUrl,
			Username: ctx.String("atcuser"),
			Password: ctx.String("atcpass"),
		}
		f, err := atc.GetFeeders(&s)
		if err != nil {
			log.Err(err).Msg("error updating feeder cache from atc")
			continue
		}
		var newValidFeeders []uuid.UUID
		count := 0
		for _, v := range f.Feeders {
			newValidFeeders = append(newValidFeeders, v.ApiKey)
			// log.Debug().Str("ApiKey", v.ApiKey.String()).Msg("added feeder")
			count += 1
		}

		// update validFeeders
		validFeeders.mu.Lock()
		validFeeders.Feeders = f.Feeders
		validFeeders.mu.Unlock()

		log.Debug().Int("feeders", count).Msg("updated feeder cache from atc")
	}
}
