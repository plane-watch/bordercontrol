package atc

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAuthenticate_Working(t *testing.T) {
	// test proper functionality

	server := PrepMockATCServer(t, MockServerTestScenarioWorking)
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: TestUser,
		Password: TestPassword,
	}

	// test
	token, err := authenticate(&s)
	assert.NoError(t, err)
	assert.Equal(t, TestAuthToken, token)

}

func TestAuthenticate_BadCreds(t *testing.T) {
	// test proper functionality

	server := PrepMockATCServer(t, MockServerTestScenarioBadCredentials)
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: fmt.Sprintf("%s-wronguser", TestUser),
		Password: fmt.Sprintf("%s-wrongpass", TestPassword),
	}

	// test
	_, err = authenticate(&s)
	assert.Error(t, err)

}

func TestAuthenticate_NoAuthToken(t *testing.T) {
	// test no auth token returned

	// prep test server
	server := PrepMockATCServer(t, MockServerTestScenarioNoAuthToken)
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: TestUser,
		Password: TestPassword,
	}

	// test
	_, err = authenticate(&s)
	assert.Error(t, err)
}

func TestAuthenticate_BadResponse(t *testing.T) {
	// test bad server response

	// prep test server
	server := PrepMockATCServer(t, MockServerTestScenarioBadResponseCodeSignIn)
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: TestUser,
		Password: TestPassword,
	}

	// test
	_, err = authenticate(&s)
	assert.Error(t, err)
}

func TestAuthenticate_NoResponse(t *testing.T) {
	// test server not responding

	// prep test server
	server := PrepMockATCServer(t, MockServerTestScenarioNoResponse)

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: TestUser,
		Password: TestPassword,
	}

	// test
	_, err = authenticate(&s)
	assert.Error(t, err)
}

func TestGetFeeders_Working(t *testing.T) {

	server := PrepMockATCServer(t, MockServerTestScenarioWorking)
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: TestUser,
		Password: TestPassword,
	}

	feeders, err := GetFeeders(&s)
	assert.NoError(t, err)

	expectedFeeders := Feeders{
		[]Feeder{{
			ApiKey:     uuid.MustParse(TestFeederAPIKeyWorking),
			Label:      TestFeederLabel,
			Latitude:   TestFeederLatitude,
			Longitude:  TestFeederLongitude,
			Mux:        TestFeederMux,
			FeederCode: TestFeederCode,
		}},
	}

	assert.Equal(t, expectedFeeders, feeders)
}

func TestGetFeeders_BadResponse(t *testing.T) {

	// prep test server
	server := PrepMockATCServer(t, MockServerTestScenarioBadResponseCodeFeeders)
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: TestUser,
		Password: TestPassword,
	}

	_, err = GetFeeders(&s)
	assert.Error(t, err)

}

func TestGetFeeders_NoResponse(t *testing.T) {

	// prep test server
	server := PrepMockATCServer(t, MockServerTestScenarioNoResponse)

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: TestUser,
		Password: TestPassword,
	}

	_, err = GetFeeders(&s)
	assert.Error(t, err)

}
