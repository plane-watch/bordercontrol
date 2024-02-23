package atc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

var (
	ErrNoAuthToken         = errors.New("No authorization token returned")
	ErrUnsupportedResponse = func(StatusCode int) error {
		return errors.New(fmt.Sprintf("Unsupported response code: %d", StatusCode))
	}
	ErrRequestFailed = errors.New("Request failed")
)

type Client struct {

	// ATC URL
	atcURL *url.URL

	// Pre-marshalled credentials
	credsJson []byte

	// Authorization Bearer Token
	authorization string

	// Context
	ctx context.Context
}

func NewClient(atcURL, username, password string) (*Client, error) {
	return NewClientWithContext(context.Background(), atcURL, username, password)
}

func NewClientWithContext(ctx context.Context, atcURL, username, password string) (*Client, error) {
	var errParse, errJMarshal error
	c := &Client{
		ctx: ctx,
	}
	c.atcURL, errParse = url.Parse(atcURL)
	c.credsJson, errJMarshal = json.Marshal(atcUser{
		User: atcCredentials{
			Email:    username,
			Password: password,
		},
	})
	return c, errors.Join(errParse, errJMarshal)
}

// authenticate to ATC if required (no auth token or force set to true)
func (c *Client) authenticate(force bool) error {
	var (
		err error
		req *http.Request
		res *http.Response
	)

	if c.authorization == "" || force {

		req, _ = http.NewRequestWithContext(c.ctx, http.MethodPost, c.atcURL.JoinPath("api/user/sign_in").String(), bytes.NewBuffer(c.credsJson))
		// No need to check for error on line above, as there's no context passed and method is using built-in type.

		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{}
		res, err = client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		// if status is 200 (OK)...
		if res.StatusCode == http.StatusOK {

			// get auth token from response header
			c.authorization = res.Header.Get("Authorization")
			if c.authorization == "" {
				return ErrNoAuthToken
			}

			// if status is not OK, log error
		} else {
			return ErrUnsupportedResponse(res.StatusCode)
		}
	}

	return nil
}

func (c *Client) getFeeders() (*Feeders, error) {
	// return all feeder data from ATC HTTP API

	var (
		forceAuth bool
		f         *Feeders
		i         int
	)

	for {

		// allow first iteration to fail due to 401/unauthorized
		// forceAuth on second try
		// if second try fails, bail out
		if i > 1 {
			return &Feeders{}, ErrRequestFailed
		}

		// authenticate if required
		c.authenticate(forceAuth)

		// prep url
		atcUrl := c.atcURL.JoinPath("/api/v1/feeders.json")

		// perform api request
		req, err := http.NewRequestWithContext(c.ctx, "GET", atcUrl.String(), nil)
		if err != nil {
			return &Feeders{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", c.authorization)
		client := &http.Client{}
		response, err := client.Do(req)
		if err != nil {
			return &Feeders{}, err
		}
		defer response.Body.Close()

		// get response
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return &Feeders{}, err
		}

		// unmarshal json if response ok
		switch response.StatusCode {
		case http.StatusOK:
			err := json.Unmarshal(body, &f)
			if err != nil {
				return &Feeders{}, err
			} else {
				return f, nil
			}
		case http.StatusUnauthorized:
			forceAuth = true
		default:
			return &Feeders{}, ErrUnsupportedResponse(response.StatusCode)
		}
		i++
	}
}
