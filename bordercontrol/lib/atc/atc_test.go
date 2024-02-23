package atc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
)

var (
	// mock ATC server credentials
	TestUser      = "testuser"
	TestPassword  = "testpass"
	TestAuthToken = "testauthtoken"

	// mock ATC response data
	TestFeederAPIKeyWorking = uuid.New()
	TestFeederLabel         = "Test Feeder 123"
	TestFeederLatitude      = 123.456789
	TestFeederLongitude     = 98.765432
	TestFeederMux           = "test-mux"
	TestFeederCode          = "ABCD-1234"
)

const (
	// mock ATC server testing scenarios
	MockServerTestScenarioWorking = iota
	MockServerTestScenarioNoAuthToken
	MockServerTestScenarioSignInBadResponseCode
	MockServerTestScenarioFeedersBadResponseCode
	MockServerTestScenarioFeedersUnauthorized
	MockServerTestScenarioFeeders5SecondResponse
	MockServerTestScenarioNoResponse
	MockServerTestScenarioBadCredentials
	MockServerTestScenarioInvalidJSON
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})
}

func StartMockATCServer(t *testing.T, testScenario int) *httptest.Server {

	// Thanks to: https://medium.com/zus-health/mocking-outbound-http-requests-in-go-youre-probably-doing-it-wrong-60373a38d2aa

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		t.Logf("mock atc server RequestURI: %s", r.RequestURI)

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
			case MockServerTestScenarioSignInBadResponseCode:
				w.WriteHeader(http.StatusBadRequest)
			case MockServerTestScenarioBadCredentials:
				w.WriteHeader(http.StatusUnauthorized)
			default:
				w.WriteHeader(http.StatusOK)
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
			case MockServerTestScenarioFeedersUnauthorized:
				w.WriteHeader(http.StatusUnauthorized)
			case MockServerTestScenarioFeedersBadResponseCode:
				w.WriteHeader(http.StatusBadRequest)
			default:
				w.WriteHeader(http.StatusOK)
			}

			// response body
			switch testScenario {
			case MockServerTestScenarioInvalidJSON:
				w.Write([]byte(resp)[2:])
			case MockServerTestScenarioFeeders5SecondResponse:
				time.Sleep(time.Second * 5)
				// noop
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

func TestErrUnsupportedResponse(t *testing.T) {
	e := ErrUnsupportedResponse(12345)
	require.Error(t, e)
	assert.Contains(t, e.Error(), "Unsupported response code:")
}

func TestClient(t *testing.T) {

	t.Run("authenticate/request_error", func(t *testing.T) {

		nltmp, err := nettest.NewLocalListener("tcp")
		require.NoError(t, err)
		nltmp.Close()

		brokenUrl := fmt.Sprintf("http://%s/broken", nltmp.Addr().String())

		c, err := NewClient(brokenUrl, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")

	})

	t.Run("NewClient/working", func(t *testing.T) {
		c, err := NewClient("http://some.atc.url:3000/", TestUser, TestPassword)
		require.NoError(t, err)

		expectedCredsJson := fmt.Sprintf(`{"user":{"email":"%s","password":"%s"}}`, TestUser, TestPassword)
		assert.Equal(t, "http://some.atc.url:3000/", c.atcURL.String())
		assert.JSONEq(t, expectedCredsJson, string(c.credsJson))
	})

	t.Run("authenticate/working/no_auth_token", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioNoAuthToken)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(false)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoAuthToken)

	})

	t.Run("authenticate/working/unsupported_response", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioSignInBadResponseCode)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "Unsupported response code:")

	})

	t.Run("authenticate/working/not_forced", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioWorking)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(false)

		require.NoError(t, err)
		assert.Equal(t, TestAuthToken, c.authorization)

	})

	t.Run("authenticate/working/forced", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioWorking)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(true)

		require.NoError(t, err)
		assert.Equal(t, TestAuthToken, c.authorization)

	})

	t.Run("authenticate/working/already_have_token", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioWorking)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(false)
		require.NoError(t, err)
		assert.Equal(t, TestAuthToken, c.authorization)

		err = c.authenticate(false)
		require.NoError(t, err)
		assert.Equal(t, TestAuthToken, c.authorization)

	})

	t.Run("GetFeeders/working", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioWorking)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		feeders, err := c.GetFeeders()
		require.NoError(t, err)

		expectedFeeders := &Feeders{
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
	})

	t.Run("GetFeeders/auth_problems", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioFeedersUnauthorized)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		_, err = c.GetFeeders()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrRequestFailed)

	})

	t.Run("GetFeeders/nil_context", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioWorking)
		defer server.Close()

		ctx := context.Background()

		c, err := NewClientWithContext(ctx, server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(false)
		require.NoError(t, err)

		// induce error
		c.ctx = nil

		_, err = c.GetFeeders()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil Context")

	})

	t.Run("GetFeeders/context_cancelled", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioFeedersUnauthorized)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())

		c, err := NewClientWithContext(ctx, server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(false)
		require.NoError(t, err)

		// induce error
		cancel()

		_, err = c.GetFeeders()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")

	})

	t.Run("GetFeeders/context_timeout", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioFeeders5SecondResponse)
		defer server.Close()

		ctx, _ := context.WithTimeout(context.Background(), time.Second)

		c, err := NewClientWithContext(ctx, server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		_, err = c.GetFeeders()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context deadline exceeded")

	})

	t.Run("GetFeeders/invalid_json", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioInvalidJSON)
		defer server.Close()

		ctx, _ := context.WithTimeout(context.Background(), time.Second)

		c, err := NewClientWithContext(ctx, server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		_, err = c.GetFeeders()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid")

	})

	t.Run("GetFeeders/bad_response_code", func(t *testing.T) {

		server := StartMockATCServer(t, MockServerTestScenarioFeedersBadResponseCode)
		defer server.Close()

		ctx, _ := context.WithTimeout(context.Background(), time.Second)

		c, err := NewClientWithContext(ctx, server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		_, err = c.GetFeeders()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Unsupported response code")

	})
}
