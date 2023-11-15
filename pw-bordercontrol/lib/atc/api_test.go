package atc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAuthenticate_Working(t *testing.T) {
	// test proper functionality

	// prep test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// check request
		assert.Equal(t, "/api/user/sign_in", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, http.MethodPost, r.Method)
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, `{"user":{"email":"testuser","password":"testpass"}}`, string(body))

		// mock response
		w.Header().Add("Authorization", "testauthtoken")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// prep url
	u, err := url.Parse(server.URL)
	assert.NoError(t, err)

	// function argument
	s := Server{
		Url:      (*u),
		Username: "testuser",
		Password: "testpass",
	}

	// test
	token, err := authenticate(&s)
	assert.NoError(t, err)
	assert.Equal(t, "testauthtoken", token)

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
