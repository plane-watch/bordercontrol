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

const (
	TestUser      = "testuser"
	TestPassword  = "testpass"
	TestAuthToken = "testauthtoken"

	TestFeederAPIKeyWorking = "6261B9C8-25C1-4B67-A5A2-51FC688E8A25"
	TestFeederLabel         = "Test Feeder 123"
	TestFeederLatitude      = 123.456789
	TestFeederLongitude     = 98.765432
	TestFeederMux           = "test-mux"
)

func prepMockATCServer(t *testing.T) *httptest.Server {
	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		switch r.URL.Path {

		case "/api/user/sign_in":

			// check request
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, http.MethodPost, r.Method)
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(
				t,
				fmt.Sprintf(`{"user":{"email":"%s","password":"%s"}}`, TestUser, TestPassword),
				string(body),
			)

			// mock response
			w.Header().Add("Authorization", TestAuthToken)
			w.WriteHeader(http.StatusOK)

		case fmt.Sprintf("/api/v1/feeders/%s.json", strings.ToLower(TestFeederAPIKeyWorking)):

			// check request
			assert.Equal(t, TestAuthToken, r.Header.Get("Authorization"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, http.MethodGet, r.Method)

			// mock response
			resp := fmt.Sprintf(
				`{"feeder":{"api_key":"%s","label":"%s","latitude":"%f","longitude":"%f","mux":"%s"}}`,
				TestFeederAPIKeyWorking,
				TestFeederLabel,
				TestFeederLatitude,
				TestFeederLongitude,
				TestFeederMux,
			)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(resp))

		case fmt.Sprintf("/api/v1/feeders.json"):

			// check request
			assert.Equal(t, TestAuthToken, r.Header.Get("Authorization"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, http.MethodGet, r.Method)

			// mock response
			resp := fmt.Sprintf(
				`{"Feeders":[{"ApiKey":"%s","Label":"%s","Latitude":"%f","Longitude":"%f","Mux":"%s"}]}`,
				TestFeederAPIKeyWorking,
				TestFeederLabel,
				TestFeederLatitude,
				TestFeederLongitude,
				TestFeederMux,
			)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(resp))

		default:
			t.Log("invalid request URL:", r.URL.Path)
			t.FailNow()
		}

	}))

	return server
}

func TestAuthenticate_Working(t *testing.T) {
	// test proper functionality

	server := prepMockATCServer(t)
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

func TestAuthenticate_NoToken(t *testing.T) {
	// test no auth token returned

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// mock response
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url: (*u),
	}

	// test
	_, err = authenticate(&s)
	assert.Error(t, err)
}

func TestAuthenticate_BadResponse(t *testing.T) {
	// test bad server response

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// mock response
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url: (*u),
	}

	// test
	_, err = authenticate(&s)
	assert.Error(t, err)
}

func TestAuthenticate_NoResponse(t *testing.T) {
	// test server not responding

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	}))

	// close test server to deliberately cause connection refused
	server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url: (*u),
	}

	// test
	_, err = authenticate(&s)
	assert.Error(t, err)
}

func TestGetFeederInfo_Working(t *testing.T) {

	server := prepMockATCServer(t)
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

	refLat, refLon, mux, label, err := GetFeederInfo(&s, uuid.MustParse(TestFeederAPIKeyWorking))
	assert.NoError(t, err)
	assert.Equal(t, TestFeederLatitude, refLat)
	assert.Equal(t, TestFeederLongitude, refLon)
	assert.Equal(t, TestFeederMux, mux)
	assert.Equal(t, TestFeederLabel, label)

}

func TestGetFeederInfo_BadResponse(t *testing.T) {

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// mock response
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url: (*u),
	}

	_, _, _, _, err = GetFeederInfo(&s, uuid.MustParse(TestFeederAPIKeyWorking))
	assert.Error(t, err)

}

func TestGetFeederInfo_NoResponse(t *testing.T) {

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	}))

	// close test server to deliberately cause connection refused
	server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url: (*u),
	}

	_, _, _, _, err = GetFeederInfo(&s, uuid.MustParse(TestFeederAPIKeyWorking))
	assert.Error(t, err)

}

func TestGetFeeders_Working(t *testing.T) {

	server := prepMockATCServer(t)
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
		[]Feeder{Feeder{
			ApiKey:    uuid.MustParse(TestFeederAPIKeyWorking),
			Label:     TestFeederLabel,
			Latitude:  TestFeederLatitude,
			Longitude: TestFeederLongitude,
			Mux:       TestFeederMux,
		}},
	}

	assert.Equal(t, expectedFeeders, feeders)
}

func TestGetFeeders_BadResponse(t *testing.T) {

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// mock response
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url: (*u),
	}

	_, err = GetFeeders(&s)
	assert.Error(t, err)

}

func TestGetFeeders_NoResponse(t *testing.T) {

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	}))
	server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url: (*u),
	}

	_, err = GetFeeders(&s)
	assert.Error(t, err)

}
