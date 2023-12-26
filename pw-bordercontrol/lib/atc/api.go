package atc

import (
	"bytes"
	"encoding/json"
	"errors"
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

	// mock ATC server credentials
	TestUser      = "testuser"
	TestPassword  = "testpass"
	TestAuthToken = "testauthtoken"

	// mock feeder details
	TestFeederAPIKeyWorking = "6261B9C8-25C1-4B67-A5A2-51FC688E8A25" // not a real feeder api key, generated with uuidgen
	TestFeederLabel         = "Test Feeder 123"
	TestFeederLatitude      = 123.456789
	TestFeederLongitude     = 98.765432
	TestFeederMux           = "test-mux"
	TestFeederCode          = "ABCD-1234"

	// mock ATC server testing scenarios
	MockServerTestScenarioWorking = iota
	MockServerTestScenarioNoAuthToken
	MockServerTestScenarioBadResponseCodeSignIn
	MockServerTestScenarioBadResponseCodeFeeder
	MockServerTestScenarioBadResponseCodeFeeders
	MockServerTestScenarioNoResponse
	MockServerTestScenarioBadCredentials
)

type (
	Server struct {
		Url      url.URL
		Username string
		Password string
		//log      zerolog.Logger
	}

	Feeders struct { // schema for /api/v1/feeders.json atc endpoint
		Feeders []Feeder
	}

	Feeder struct { // part of schema for /api/v1/feeders.json atc endpoint
		Altitude      float64 `json:",string"`
		ApiKey        uuid.UUID
		FeederCode    string
		FeedDirection string
		FeedProtocol  string
		ID            int
		Label         string
		Latitude      float64 `json:",string"`
		Longitude     float64 `json:",string"`
		MlatEnabled   bool
		Mux           string
		User          string
	}

	atcCredentials struct { // schema for atc credentials
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	atcUser struct { // schema for atc authentication
		User atcCredentials `json:"user"`
	}
)

func authenticate(server *Server) (authToken string, err error) {

	atcUrl := server.Url.JoinPath("api/user/sign_in")

	creds := atcUser{
		User: atcCredentials{
			Email:    server.Username,
			Password: server.Password,
		},
	}

	credsJson, err := json.Marshal(creds)
	if err != nil {
		return authToken, err
	}

	req, err := http.NewRequest("POST", atcUrl.String(), bytes.NewBuffer(credsJson))
	if err != nil {
		return authToken, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	response, err := client.Do(req)
	if err != nil {
		return authToken, err
	}
	defer response.Body.Close()

	if response.StatusCode == 200 {

		authToken = response.Header.Get("Authorization")
		if authToken == "" {
			return authToken, errors.New("No authorization token returned")
		}

	} else {
		errStr := fmt.Sprintf("ATC API response status: %s", response.Status)
		return authToken, errors.New(errStr)
	}

	return authToken, nil
}

func GetFeeders(server *Server) (feeders Feeders, err error) {

	authToken, err := authenticate(server)
	if err != nil {
		return feeders, err
	}

	atcUrl := server.Url.JoinPath("/api/v1/feeders.json")

	req, err := http.NewRequest("GET", atcUrl.String(), nil)
	if err != nil {
		return feeders, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authToken)

	client := &http.Client{}
	response, err := client.Do(req)
	if err != nil {
		return feeders, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return feeders, err
	}

	// fmt.Println("response Body:", string(body))

	if response.StatusCode == 200 {

		err := json.Unmarshal(body, &feeders)
		if err != nil {
			return feeders, err
		}
	} else {
		errStr := fmt.Sprintf("ATC API response status: %s", response.Status)
		return feeders, errors.New(errStr)
	}

	return feeders, nil
}

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

		case fmt.Sprintf("/api/v1/feeders/%s.json", strings.ToLower(TestFeederAPIKeyWorking)):

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
			case MockServerTestScenarioBadResponseCodeSignIn:
			case MockServerTestScenarioBadResponseCodeFeeder:
				w.WriteHeader(http.StatusBadRequest)
			default:
				w.WriteHeader(http.StatusOK)
			}

			// response body
			w.Write([]byte(resp))

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
			case MockServerTestScenarioBadResponseCodeSignIn:
			case MockServerTestScenarioBadResponseCodeFeeders:
				w.WriteHeader(http.StatusBadRequest)
			default:
				w.WriteHeader(http.StatusOK)
			}

			// response body
			w.Write([]byte(resp))

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
