package atc

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrUnsupportedResponse(t *testing.T) {
	e := ErrUnsupportedResponse(12345)
	require.Error(t, e)
	assert.Contains(t, e.Error(), "Unsupported response code:")
}

func TestClient(t *testing.T) {

	t.Run("NewClient/working", func(t *testing.T) {
		c, err := NewClient("http://some.atc.url:3000/", TestUser, TestPassword)
		require.NoError(t, err)

		expectedCredsJson := fmt.Sprintf(`{"user":{"email":"%s","password":"%s"}}`, TestUser, TestPassword)
		assert.Equal(t, "http://some.atc.url:3000/", c.atcURL.String())
		assert.JSONEq(t, expectedCredsJson, string(c.credsJson))
	})

	t.Run("authenticate/working/not_forced", func(t *testing.T) {

		server := PrepMockATCServer(t, MockServerTestScenarioWorking)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(false)

		require.NoError(t, err)
		assert.Equal(t, TestAuthToken, c.authorization)

	})

	t.Run("authenticate/working/forced", func(t *testing.T) {

		server := PrepMockATCServer(t, MockServerTestScenarioWorking)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		err = c.authenticate(true)

		require.NoError(t, err)
		assert.Equal(t, TestAuthToken, c.authorization)

	})

	t.Run("authenticate/working/already_have_token", func(t *testing.T) {

		server := PrepMockATCServer(t, MockServerTestScenarioWorking)
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

	t.Run("getFeeders/working", func(t *testing.T) {

		server := PrepMockATCServer(t, MockServerTestScenarioWorking)
		defer server.Close()

		c, err := NewClient(server.URL, TestUser, TestPassword)
		require.NoError(t, err)

		feeders, err := c.getFeeders()
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
}
