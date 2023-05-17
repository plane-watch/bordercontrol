package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"pw_bordercontrol/lib/atc"
	"pw_bordercontrol/lib/logging"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// struct for requesting that the startFeederContainers goroutine start a container
type startContainerRequest struct {
	uuid   uuid.UUID // feeder uuid
	refLat float64   // feeder lat
	refLon float64   // feeder lon
	mux    string    // the multiplexer to upstream the data to
	label  string    // the label of the feeder
	srcIP  net.IP    // client IP address
}

// struct for a list of valid feeder uuids
type atcFeeders struct {
	mu      sync.Mutex
	feeders []uuid.UUID
}

var (
	validFeeders atcFeeders
)

func isValidApiKey(clientApiKey uuid.UUID) bool {
	// return true of api key clientApiKey is a valid feeder in atc
	validFeeders.mu.Lock()
	defer validFeeders.mu.Unlock()
	for _, v := range validFeeders.feeders {
		if v == clientApiKey {
			return true
		}
	}
	return false
}

func updateFeederDB(ctx *cli.Context, updateFreq time.Duration) {
	// updates validFeeders with data from atc

	firstRun := true

	for {

		// sleep for updateFreq
		if !firstRun {
			time.Sleep(updateFreq)
		} else {
			firstRun = false
		}

		// log.Debug().Msg("started updating api key cache from atc")

		// get data from atc
		atcUrl, err := url.Parse(ctx.String("atcurl"))
		if err != nil {
			log.Error().Msg("--atcurl is invalid")
			continue
		}
		s := atc.Server{
			Url:      *atcUrl,
			Username: ctx.String("atcuser"),
			Password: ctx.String("atcpass"),
		}
		f, err := atc.GetFeeders(&s)
		var newValidFeeders []uuid.UUID
		count := 0
		for _, v := range f.Feeders {
			newValidFeeders = append(newValidFeeders, v.ApiKey)
			// log.Debug().Str("ApiKey", v.ApiKey.String()).Msg("added feeder")
			count += 1
		}

		// update validFeeders
		validFeeders.mu.Lock()
		validFeeders.feeders = newValidFeeders
		validFeeders.mu.Unlock()

		log.Info().Int("feeders", count).Msg("updated feeder uuid cache from atc")
	}
}

func startFeederContainers(ctx *cli.Context, containersToStart chan startContainerRequest) {
	// reads startContainerRequests from channel containersToStart and starts container
	sfcLog := log.With().Logger()

	// set up docker client
	dockerCtx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		sfcLog.Err(err).Msg("Could not create docker client")
		panic(err)
	}
	defer cli.Close()

	for {
		// read from channel (this blocks until a request comes in)
		// sfcLog.Debug().Str("func", "startFeederContainers").Msg("waiting for data to arrive on channel")
		containerToStart := <-containersToStart
		// sfcLog.Debug().Str("func", "startFeederContainers").Msg("received data from channel")

		cLog := log.With().Float64("lat", containerToStart.refLat).Float64("lon", containerToStart.refLon).Str("mux", containerToStart.mux).Str("label", containerToStart.label).Str("uuid", containerToStart.uuid.String()).IPAddr("src", containerToStart.srcIP).Logger()

		// determine if container is already running
		containers, err := cli.ContainerList(dockerCtx, types.ContainerListOptions{})
		if err != nil {
			sfcLog.Err(err).Msg("Could not list docker containers")
		}
		foundContainer := false
		feederContainerName := fmt.Sprintf("feed-in-%s", containerToStart.uuid.String())
		for _, container := range containers {
			for _, cn := range container.Names {
				log.Info().Str("looking", fmt.Sprintf("/%s", feederContainerName)).Str("cn", cn)
				if cn == fmt.Sprintf("/%s", feederContainerName) {
					foundContainer = true
					break
				}
			}
		}
		if foundContainer {
			cLog.Info().Msg("feed-in container already running")
		} else {
			cLog.Debug().Msg("starting feed-in container")

			// prepare environment variables for container
			envVars := [...]string{
				fmt.Sprintf("READSB_LAT=%f", containerToStart.refLat),
				fmt.Sprintf("READSB_LON=%f", containerToStart.refLon),
				"READSB_STATS_EVERY=300",
				"READSB_NET_ENABLE=true",
				"READSB_NET_BEAST_REDUCE_INTERVAL=1",
				"READSB_NET_BEAST_INPUT_PORT=12345",
				"READSB_NET_BEAST_OUTPUT_PORT=30005",
				"READSB_NET_ONLY=true",
			}

			// prepare labels
			containerLabels := make(map[string]string)
			containerLabels["plane.watch.label"] = containerToStart.label
			containerLabels["plane.watch.mux"] = containerToStart.mux

			// prepare container config
			containerConfig := container.Config{
				Image:  ctx.String("feedinimage"),
				Env:    envVars[:],
				Labels: containerLabels,
			}

			// prepare container host config
			containerHostConfig := container.HostConfig{
				AutoRemove: true,
			}

			// prepare container network config
			endpointsConfig := make(map[string]*network.EndpointSettings)
			endpointsConfig["bordercontrol_feeder"] = &network.EndpointSettings{}
			networkingConfig := network.NetworkingConfig{
				EndpointsConfig: endpointsConfig,
			}

			// create feed-in container
			resp, err := cli.ContainerCreate(dockerCtx, &containerConfig, &containerHostConfig, &networkingConfig, nil, feederContainerName)
			if err != nil {
				panic(err)
			}

			// start container
			if err := cli.ContainerStart(dockerCtx, resp.ID, types.ContainerStartOptions{}); err != nil {
				panic(err)
			}

			cLog.Info().Str("container_id", resp.ID).Msg("started feed-in container")

		}

		// sfcLog.Debug().Str("func", "startFeederContainers").Msg("finished loop")
	}
}

func clientConnection(ctx *cli.Context, conn net.Conn, tlsConfig *tls.Config, containersToStart chan startContainerRequest) {
	// handles incoming connections
	// TODO: need a way to kill a client connection if the UUID is no longer valid (ie: feeder banned)

	cLog := log.With().Logger()

	var (
		sendRecvBufferSize             = 256 * 1024 // 256kB
		clientAuthenticated            = false
		clientFeedInContainerConnected = false
		feedInConn                     net.Conn
		feedInErr                      error
		clientApiKey                   uuid.UUID
		err                            error
	)

	defer conn.Close()

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(conn.RemoteAddr().String(), ":")[0])
	cLog = cLog.With().IPAddr("src", remoteIP).Logger()

	cLog.Debug().Msgf("connection established")
	defer cLog.Debug().Msgf("connection closed")

	buf := make([]byte, sendRecvBufferSize)
	err = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if err != nil {
		panic(err)
	}
	for {

		// read data
		bytesRead, err := conn.Read(buf)
		if err != nil {
			if err.Error() == "tls: first record does not look like a TLS handshake" {
				cLog.Warn().Msg(err.Error())
				break
			} else if err.Error() == "EOF" {
				if clientAuthenticated {
					cLog.Info().Msg("client disconnected")
				}
				break
			} else if e, ok := err.(net.Error); ok && e.Timeout() {
				cLog.Debug().AnErr("error", err).Msg("conn.Read")
				if bytesRead == 0 {
					break
				}
			} else {
				cLog.Err(err).Msg("conn.Read")
				break
			}
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if !clientAuthenticated {

			// check TLS handshake
			tlscon := conn.(*tls.Conn)
			if tlscon.ConnectionState().HandshakeComplete {

				// check valid uuid was returned as ServerName (sni)
				clientApiKey, err = uuid.Parse(tlscon.ConnectionState().ServerName)
				if err != nil {
					cLog.Warn().Msg("client sent invalid uuid")
					break
				}

				// check valid api key
				if isValidApiKey(clientApiKey) {

					// update log context with client uuid
					cLog = cLog.With().Str("uuid", clientApiKey.String()).Logger()
					cLog.Info().Msg("client connected")

					// if API is valid, then set clientAuthenticated to TRUE
					clientAuthenticated = true

					// get feeder lat/long
					atcUrl, err := url.Parse(ctx.String("atcurl"))
					if err != nil {
						log.Error().Msg("--atcurl is invalid")
						continue
					}
					s := atc.Server{
						Url:      *atcUrl,
						Username: ctx.String("atcuser"),
						Password: ctx.String("atcpass"),
					}
					refLat, refLon, mux, label, err := atc.GetFeederInfo(&s, clientApiKey)
					if err != nil {
						log.Err(err).Msg("atc.GetFeederLatLon")
						continue
					}

					// start the container
					containersToStart <- startContainerRequest{
						uuid:   clientApiKey,
						refLat: refLat,
						refLon: refLon,
						mux:    mux,
						label:  label,
						srcIP:  remoteIP,
					}

				} else {
					// if API is not valid, then kill the connection
					cLog.Warn().Msg("client sent invalid api key")
					break
				}

			} else {
				// if TLS handshake is not complete, then kill the connection
				cLog.Warn().Msg("data received before tls handshake")
				break
			}
		}

		// If the client has been authenticated, then we can do stuff with the data
		if clientAuthenticated {

			// If the client's feed-in container is not yet connected
			if !clientFeedInContainerConnected {

				// attempt to connect to the container
				dialAddress := fmt.Sprintf("feed-in-%s:12345", clientApiKey)
				cLog.Debug().Str("dst", dialAddress).Msg("attempting to connect")
				feedInConn, feedInErr = net.DialTimeout("tcp", dialAddress, 1*time.Second)
				if feedInErr != nil {
					cLog.Warn().AnErr("error", feedInErr).Str("dst", dialAddress).Msg("could not connect to feed-in container")
				} else {
					clientFeedInContainerConnected = true
					cLog.Debug().Str("dst", dialAddress).Msg("connected ok")
				}

			}
		}

		if clientAuthenticated {

			// If the client's feed-in container is not yet connected
			if clientFeedInContainerConnected {
				if bytesRead > 0 {

					// attempt to write data in buf (that was read from client connection earlier)
					bytesWritten, err := feedInConn.Write(buf[:bytesRead])
					if err != nil {
						cLog.Err(err).Msg("error writing to feed-in container")
					} else {
						cLog.Debug().Int("bytes", bytesWritten).Msg("wrote to feed-in container")
					}
				}
			}
		}
	}
	cLog.Debug().Msg("goroutine finishing")
}

func main() {

	app := &cli.App{
		Name:  "Plane Watch Feeder Endpoint",
		Usage: "Server for multiple stunnel-based endpoints",
		Description: `This program acts as a server for multiple stunnel-based endpoints, ` +
			`authenticates the feeder based on UUID check against atc.plane.watch, ` +
			`routes data to feed-in containers.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "listen",
				Usage:   "Address and TCP port server will listen on",
				Value:   "0.0.0.0:12345",
				EnvVars: []string{"BC_LISTEN"},
			},
			&cli.StringFlag{
				Name:     "cert",
				Usage:    "Server certificate PEM file name (x509)",
				Required: true,
				EnvVars:  []string{"BC_CERT_FILE"},
			},
			&cli.StringFlag{
				Name:     "key",
				Usage:    "Server certificate private key PEM file name (x509)",
				Required: true,
				EnvVars:  []string{"BC_KEY_FILE"},
			},
			&cli.StringFlag{
				Name:     "atcurl",
				Usage:    "URL to ATC API",
				Required: true,
				EnvVars:  []string{"ATC_URL"},
			},
			&cli.StringFlag{
				Name:     "atcuser",
				Usage:    "email username for ATC API",
				Required: true,
				EnvVars:  []string{"ATC_USER"},
			},
			&cli.StringFlag{
				Name:     "atcpass",
				Usage:    "password for ATC API",
				Required: true,
				EnvVars:  []string{"ATC_PASS"},
			},
			&cli.StringFlag{
				Name:  "feedinimage",
				Usage: "feed-in image name",
				Value: "feed-in",
			},
		},
		Action: runServer,
	}

	logging.IncludeVerbosityFlags(app)
	logging.ConfigureForCli()

	// Set logging level
	app.Before = func(c *cli.Context) error {
		logging.SetLoggingLevel(c)
		return nil
	}

	// Final exit
	if err := app.Run(os.Args); nil != err {
		log.Err(err).Msg("Finishing with an error")
		os.Exit(1)
	}

}

func runServer(ctx *cli.Context) error {

	// start goroutine to regularly pull feeders from atc
	log.Info().Msg("starting updateFeederDB")
	go updateFeederDB(ctx, 60*time.Second)

	// prepare channel for container start requests
	containersToStart := make(chan startContainerRequest)
	defer close(containersToStart)

	// start goroutine to start feeder containers
	log.Info().Msg("starting startFeederContainers")
	go startFeederContainers(ctx, containersToStart)

	// load server cert & key
	// TODO: reload certificate on sighup: https://stackoverflow.com/questions/37473201/is-there-a-way-to-update-the-tls-certificates-in-a-net-http-server-without-any-d
	log.Info().Str("file", ctx.String("cert")).Msg("loading certificate")
	log.Info().Str("file", ctx.String("key")).Msg("loading private key")
	cert, err := tls.LoadX509KeyPair(
		ctx.String("cert"),
		ctx.String("key"),
	)
	if err != nil {
		log.Err(err).Msg("tls.LoadX509KeyPair")
	}

	// tls configuration
	tlsConfig := tls.Config{Certificates: []tls.Certificate{cert}}
	// tlsConfig.ServerName = "bordercontrol.plane.watch"

	// start TLS server
	log.Info().Msgf("Starting %s on %s", ctx.App.Name, ctx.String("listen"))
	tlsListener, err := tls.Listen("tcp", ctx.String("listen"), &tlsConfig)
	if err != nil {
		log.Err(err).Msg("tls.Listen")
	}
	defer tlsListener.Close()

	// handle incoming connections
	for {
		conn, err := tlsListener.Accept()
		if err != nil {
			log.Err(err).Msg("tlsListener.Accept")
			continue
		}
		defer conn.Close()
		go clientConnection(ctx, conn, &tlsConfig, containersToStart)
	}
	return nil
}
