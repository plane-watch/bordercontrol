package atc

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

var (
	TestFeederAPIKeyWorking = uuid.New()
	TestFeederLabel         = "Test Feeder 123"
	TestFeederLatitude      = 123.456789
	TestFeederLongitude     = 98.765432
	TestFeederMux           = "test-mux"
	TestFeederCode          = "ABCD-1234"
)

const (
	// mock ATC server credentials
	TestUser      = "testuser"
	TestPassword  = "testpass"
	TestAuthToken = "testauthtoken"

	// mock ATC server testing scenarios
	MockServerTestScenarioWorking = iota
	MockServerTestScenarioNoAuthToken
	MockServerTestScenarioBadResponseCodeSignIn
	MockServerTestScenarioBadResponseCodeFeeder
	MockServerTestScenarioBadResponseCodeFeeders
	MockServerTestScenarioNoResponse
	MockServerTestScenarioBadCredentials
	MockServerTestScenarioInvalidJSON
)

func PrepMockATCServer(t *testing.T, testScenario int) *httptest.Server {

	// Thanks to: https://medium.com/zus-health/mocking-outbound-http-requests-in-go-youre-probably-doing-it-wrong-60373a38d2aa

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		switch r.URL.Path {

		case "/api/user/sign_in":

			// check request
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, http.MethodPost, r.Method)
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)

			if testScenario != MockServerTestScenarioBadCredentials {
				assert.Equal(
					t,
					fmt.Sprintf(`{"user":{"email":"%s","password":"%s"}}`, TestUser, TestPassword),
					string(body),
				)
			}

			// mock response

			// Auth token
			if testScenario != MockServerTestScenarioNoAuthToken {
				w.Header().Add("Authorization", TestAuthToken)
			}

			// Response code
			switch testScenario {
			case MockServerTestScenarioBadResponseCodeSignIn:
				w.WriteHeader(http.StatusBadRequest)
			case MockServerTestScenarioBadCredentials:
				w.WriteHeader(http.StatusUnauthorized)
			default:
				w.WriteHeader(http.StatusOK)
			}

		case fmt.Sprintf("/api/v1/feeders/%s.json", strings.ToLower(TestFeederAPIKeyWorking.String())):

			// check request
			assert.Equal(t, TestAuthToken, r.Header.Get("Authorization"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, http.MethodGet, r.Method)

			// mock response

			resp := fmt.Sprintf(
				`{"feeder":{"api_key":"%s","label":"%s","latitude":"%f","longitude":"%f","mux":"%s","FeederCode:"%s"}}`,
				TestFeederAPIKeyWorking,
				TestFeederLabel,
				TestFeederLatitude,
				TestFeederLongitude,
				TestFeederMux,
				TestFeederCode,
			)

			// response code
			switch testScenario {
			case MockServerTestScenarioBadResponseCodeFeeder:
				w.WriteHeader(http.StatusBadRequest)
			default:
				w.WriteHeader(http.StatusOK)
			}

			// response body
			switch testScenario {
			case MockServerTestScenarioInvalidJSON:
				w.Write([]byte(resp)[2:])
			default:
				w.Write([]byte(resp))
			}

		case fmt.Sprintf("/api/v1/feeders.json"):

			// check request
			assert.Equal(t, TestAuthToken, r.Header.Get("Authorization"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, http.MethodGet, r.Method)

			// mock response
			resp := fmt.Sprintf(
				`{"Feeders":[{"ApiKey":"%s","Label":"%s","Latitude":"%f","Longitude":"%f","Mux":"%s", "FeederCode":"%s"}]}`,
				TestFeederAPIKeyWorking,
				TestFeederLabel,
				TestFeederLatitude,
				TestFeederLongitude,
				TestFeederMux,
				TestFeederCode,
			)

			// response code
			switch testScenario {
			case MockServerTestScenarioBadResponseCodeFeeders:
				w.WriteHeader(http.StatusBadRequest)
			default:
				w.WriteHeader(http.StatusOK)
			}

			// response body
			switch testScenario {
			case MockServerTestScenarioInvalidJSON:
				w.Write([]byte(resp)[2:])
			default:
				w.Write([]byte(resp))
			}

		default:
			t.Log("invalid request URL:", r.URL.Path)
			t.FailNow()
		}

	}))

	if testScenario == MockServerTestScenarioNoResponse {
		server.Close()
	}

	return server
}

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
	assert.Contains(t, err.Error(), "Unauthorized")

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
	assert.Equal(t, "No authorization token returned", err.Error())
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
	assert.Contains(t, err.Error(), "Bad Request")
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
	assert.Contains(t, err.Error(), "connect: connection refused")
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
			ApiKey:     uuid.MustParse(TestFeederAPIKeyWorking.String()),
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
	assert.Contains(t, err.Error(), "Bad Request")

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
	assert.Contains(t, err.Error(), "connect: connection refused")
}

func TestGetFeeders_InvalidJSON(t *testing.T) {

	// prep test server
	server := PrepMockATCServer(t, MockServerTestScenarioInvalidJSON)

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
	assert.Contains(t, err.Error(), "invalid")
}
