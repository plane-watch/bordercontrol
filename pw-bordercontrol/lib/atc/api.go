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
	// authenticate to ATC HTTP API

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

func GetFeeders(server *Server) (feeders Feeders, err error) {
	// TODO: Once NATS supports feeder_code (https://github.com/plane-watch/pw-pipeline/pull/235),
	//       Update this function to attempt NATS first and fail back to ATC API if NATS fails.
	return GetFeedersFromATC(server)

}

func GetFeedersFromATC(server *Server) (feeders Feeders, err error) {
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
