package main

import (
	"pw_bordercontrol/lib/atc"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestIsValidApiKey(t *testing.T) {

	// prepare test data
	u := uuid.New()
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey: u,
	})

	t.Run("check valid api key", func(t *testing.T) {
		assert.True(t, isValidApiKey(u))
	})

	t.Run("check invalid api key", func(t *testing.T) {
		assert.False(t, isValidApiKey(uuid.New()))
	})
}

func TestGetFeederInfo(t *testing.T) {

	// prepare test data
	u := uuid.New()
	lat := 123.45678
	lon := 87.65432
	mux := "testing"
	label := "testfeeder"
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey:    u,
		Latitude:  lat,
		Longitude: lon,
		Mux:       mux,
		Label:     label,
	})

	f := feederClient{
		clientApiKey: u,
	}

	t.Run("test valid feeder", func(t *testing.T) {
		err := getFeederInfo(&f)
		assert.NoError(t, err)
		assert.Equal(t, lat, f.refLat)
		assert.Equal(t, lon, f.refLon)
		assert.Equal(t, mux, f.mux)
		assert.Equal(t, label, f.label)
	})

	t.Run("test invalid feeder", func(t *testing.T) {
		err := getFeederInfo(&feederClient{})
		assert.Error(t, err)
	})

}

// func getFeederInfo(f *feederClient) error {
// 	// return feeder info from atc, specifically: lat, lon, mux and label
// 	found := false
// 	validFeeders.mu.RLock()
// 	defer validFeeders.mu.RUnlock()
// 	for _, v := range validFeeders.Feeders {
// 		if v.ApiKey == f.clientApiKey {
// 			f.refLat = v.Latitude
// 			f.refLon = v.Longitude
// 			f.mux = v.Mux
// 			f.label = v.Label
// 			found = true
// 			break
// 		}
// 	}
// 	if !found {
// 		return errors.New("could not find feeder")
// 	}
// 	return nil
// }
