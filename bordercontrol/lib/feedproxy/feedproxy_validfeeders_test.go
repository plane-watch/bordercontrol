package feedproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"pw_bordercontrol/lib/atc"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGetDataFromATC_error(t *testing.T) {

	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer svr.Close()

	svrUrl, err := url.Parse(svr.URL)
	require.NoError(t, err, "error getting url of test http server")

	_, err = getDataFromATC(svrUrl, "testuser", "testpass")
	require.Error(t, err)
}

func TestFeedersGaugeFunc(t *testing.T) {
	defer func() { validFeeders = atcFeeders{} }()

	validFeeders = atcFeeders{}
	for i := 1; i <= 3; i++ {
		validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{})
	}

	f := feedersGaugeFunc()

	require.Equal(t, float64(3), f)
}

func TestIsValidApiKey(t *testing.T) {
	defer func() { validFeeders = atcFeeders{} }()

	testValidUUID := uuid.New()
	testInvalidUUID := uuid.New()

	validFeeders = atcFeeders{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey: testValidUUID,
	})

	require.True(t, isValidApiKey(testValidUUID))
	require.False(t, isValidApiKey(testInvalidUUID))
}

func TestGetFeederInfo_found(t *testing.T) {
	defer func() { validFeeders = atcFeeders{} }()

	testValidUUID := uuid.New()
	testLat := float64(1.23456)
	testLon := float64(2.34567)
	testMux := "testmux"
	testLabel := "testlabel"
	testFeederCode := "testfeedercode"

	validFeeders = atcFeeders{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey:     testValidUUID,
		Latitude:   testLat,
		Longitude:  testLon,
		Mux:        testMux,
		Label:      testLabel,
		FeederCode: testFeederCode,
	})

	f := feederClient{
		clientApiKey: testValidUUID,
	}
	fExpected := feederClient{
		clientApiKey: testValidUUID,
		refLat:       testLat,
		refLon:       testLon,
		mux:          testMux,
		label:        testLabel,
		feederCode:   testFeederCode,
	}

	err := getFeederInfo(&f)
	require.NoError(t, err)
	require.Equal(t, fExpected, f)
}

func TestGetFeederInfo_not_found(t *testing.T) {
	defer func() { validFeeders = atcFeeders{} }()

	testValidUUID := uuid.New()

	validFeeders = atcFeeders{}
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey: testValidUUID,
	})

	f := feederClient{
		clientApiKey: uuid.New(),
	}

	err := getFeederInfo(&f)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrFeederNotFound)
}

func TestUpdateFeederDB(t *testing.T) {
	defer func() { validFeeders = atcFeeders{} }()

	// overwrite function for testing
	origGetDataFromATC := getDataFromATC
	defer func() { getDataFromATC = origGetDataFromATC }()

	// prep mocked response from getDataFromATC
	testValidUUID := uuid.New()
	testLat := float64(1.23456)
	testLon := float64(2.34567)
	testMux := "testmux"
	testLabel := "testlabel"
	testFeederCode := "testfeedercode"
	f := atc.Feeders{}
	f.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey:     testValidUUID,
		Latitude:   testLat,
		Longitude:  testLon,
		Mux:        testMux,
		Label:      testLabel,
		FeederCode: testFeederCode,
	})

	// overwrite getDataFromATC for testing
	getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
		return f, nil
	}

	// prep config for updateFeederDB
	conf := ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	// prep context
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())

	var wg sync.WaitGroup

	// run updateFeederDB
	wg.Add(1)
	go func() {
		defer wg.Done()
		updateFeederDB(&conf)
	}()

	// let a few iterations happen
	time.Sleep(time.Second)

	// cancel context to stop updateFeederDB
	cancel()

	// wait for goroutine to finish
	wg.Wait()
}

func TestUpdateFeederDB_ATC_error(t *testing.T) {
	defer func() { validFeeders = atcFeeders{} }()

	// overwrite function for testing
	origGetDataFromATC := getDataFromATC
	defer func() { getDataFromATC = origGetDataFromATC }()

	// overwrite getDataFromATC for testing
	getDataFromATC = func(atcurl *url.URL, atcuser, atcpass string) (atc.Feeders, error) {
		return atc.Feeders{}, errors.New("test error")
	}

	// prep config for updateFeederDB
	conf := ProxyConfig{
		UpdateFrequency: time.Millisecond * 500,
	}

	// prep context
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())

	var wg sync.WaitGroup

	// run updateFeederDB
	wg.Add(1)
	go func() {
		defer wg.Done()
		updateFeederDB(&conf)
	}()

	// let a few iterations happen
	time.Sleep(time.Second)

	// cancel context to stop updateFeederDB
	cancel()

	// wait for goroutine to finish
	wg.Wait()
}
