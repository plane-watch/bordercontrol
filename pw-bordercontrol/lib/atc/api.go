// Package atc provides functionality to query feeder data from plane-watch/pw-air_traffic_control.

package atc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/google/uuid"
)

type (

	// Server is a representation of an ATC instance
	Server struct {

		// Url of the ATC instance
		Url url.URL

		// Username for authenticating to ATC API
		Username string

		// Password for authenticating to ATC API
		Password string
	}

	// Feeders holds all feeder information from ATC.
	// Schema for /api/v1/feeders.json atc endpoint
	// To facilitate unmarshall of JSON to go struct.
	Feeders struct {
		Feeders []Feeder
	}

	// Feeder represents a feeder's information from ATC.
	// Part of schema for /api/v1/feeders.json atc endpoint.
	// To facilitate unmarshall of JSON to go struct.
	Feeder struct {
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

	// Schema to marshall atc credentials into JSON
	atcCredentials struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	// Schema to marshall atc credentials into JSON
	atcUser struct {
		User atcCredentials `json:"user"`
	}
)

// authenticate provides authentication to ATC's HTTP API.
// Credentials are obtained from server.
// authToken is returned, and should be used for requests to ATC.
func authenticate(server *Server) (authToken string, err error) {

	// prep url
	atcUrl := server.Url.JoinPath("api/user/sign_in")

	// prep creds & marshal into JSON
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

	// perform API request
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

	// if status is 200 (OK)...
	if response.StatusCode == 200 {

		// get auth token from response header
		authToken = response.Header.Get("Authorization")
		if authToken == "" {
			return authToken, errors.New("No authorization token returned")
		}

		// if status is not OK, log error
	} else {
		errStr := fmt.Sprintf("ATC API response status: %s", response.Status)
		return authToken, errors.New(errStr)
	}

	return authToken, nil
}

// GetFeeders facilitates getting feeder information either via NATS or failing back to ATC HTTP API.
// Credentials are obtained from server.
// Feeder details from ATC are returned in feeders.
func GetFeeders(server *Server) (feeders Feeders, err error) {
	// TODO: Once NATS supports feeder_code (https://github.com/plane-watch/pw-pipeline/pull/235),
	//       Update this function to attempt NATS first and fail back to ATC API if NATS fails.
	return getFeedersFromATC(server)
}

// getFeedersFromATC facilitates getting feeder information from ATC HTTP API.
// Credentials are obtained from server.
// Feeder details from ATC are returned in feeders.
func getFeedersFromATC(server *Server) (feeders Feeders, err error) {
	// return all feeder data from ATC HTTP API

	// authenticate to ATC
	authToken, err := authenticate(server)
	if err != nil {
		return feeders, err
	}

	// prep url
	atcUrl := server.Url.JoinPath("/api/v1/feeders.json")

	// perform api request
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

	// get response
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return feeders, err
	}

	// unmarshal json if response ok
	if response.StatusCode == 200 {
		err := json.Unmarshal(body, &feeders)
		if err != nil {
			return feeders, err
		}
	} else {
		errStr := fmt.Sprintf("ATC API response status: %s", response.Status)
		return feeders, errors.New(errStr)
	}

	// return results
	return feeders, nil
}
