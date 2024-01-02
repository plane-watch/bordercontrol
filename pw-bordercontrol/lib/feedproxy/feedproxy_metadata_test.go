package feedproxy

import (
	"pw_bordercontrol/lib/atc"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestIsValidApiKey(t *testing.T) {

	// prepare test data
	u := uuid.New()
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey: u,
	})
	validFeeders.mu.Unlock()

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
	validFeeders.mu.Lock()
	validFeeders.Feeders = []atc.Feeder{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey:    u,
		Latitude:  lat,
		Longitude: lon,
		Mux:       mux,
		Label:     label,
	})
	validFeeders.mu.Unlock()

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

// func TestUpdateFeederDB(t *testing.T) {

// 	// reset validFeeders
// 	validFeeders.mu.Lock()
// 	validFeeders.Feeders = []atc.Feeder{}
// 	validFeeders.mu.Unlock()

// 	// set up mock server
// 	server := atc.PrepMockATCServer(t, atc.MockServerTestScenarioWorking)
// 	defer server.Close()

// 	// prep config
// 	conf := updateFeederDBConfig{
// 		updateFreq: time.Second,
// 		atcUrl:     server.URL,
// 		atcUser:    atc.TestUser,
// 		atcPass:    atc.TestPassword,
// 	}

// 	// start updater
// 	go updateFeederDB(&conf)

// 	// wait for ATC update
// 	time.Sleep(time.Second * 2)

// 	// stop ATC update
// 	conf.stopMu.Lock()
// 	conf.stop = true
// 	conf.stopMu.Unlock()

// 	// wait for ATC update to finish
// 	time.Sleep(time.Second * 2)

// 	// check feeders
// 	validFeeders.mu.Lock()
// 	defer validFeeders.mu.Unlock()
// 	assert.Equal(t, uuid.MustParse(atc.TestFeederAPIKeyWorking), validFeeders.Feeders[0].ApiKey)
// 	assert.Equal(t, atc.TestFeederLabel, validFeeders.Feeders[0].Label)
// 	assert.Equal(t, atc.TestFeederLatitude, validFeeders.Feeders[0].Latitude)
// 	assert.Equal(t, atc.TestFeederLongitude, validFeeders.Feeders[0].Longitude)
// 	assert.Equal(t, atc.TestFeederMux, validFeeders.Feeders[0].Mux)
// 	assert.Equal(t, atc.TestFeederCode, validFeeders.Feeders[0].FeederCode)
// }
