package main

import (
	"context"
	"crypto/tls"
	"errors"
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

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
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

// struct for a list of valid feeder uuids (+ mutex for sync)
type atcFeeders struct {
	mu      sync.RWMutex
	Feeders []atc.Feeder
}

var (
	validFeeders atcFeeders // list of valid feeders
)

func isValidApiKey(clientApiKey uuid.UUID) bool {
	// return true of api key clientApiKey is a valid feeder in atc
	validFeeders.mu.RLock()
	defer validFeeders.mu.RUnlock()
	for _, v := range validFeeders.Feeders {
		if v.ApiKey == clientApiKey {
			return true
		}
	}
	return false
}

func getFeederInfo(feederApiKey uuid.UUID) (refLat float64, refLon float64, mux string, label string, err error) {
	found := false
	validFeeders.mu.RLock()
	defer validFeeders.mu.RUnlock()
	for _, v := range validFeeders.Feeders {
		if v.ApiKey == feederApiKey {
			refLat = v.Latitude
			refLon = v.Longitude
			mux = v.Mux
			label = v.Label
			found = true
			break
		}
	}
	if !found {
		err = errors.New("could not find feeder")
	}
	return refLat, refLon, mux, label, err
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
		validFeeders.Feeders = f.Feeders
		validFeeders.mu.Unlock()

		log.Info().Int("feeders", count).Msg("updated feeder uuid cache from atc")
	}
}

func muxHostname(mux string) (muxHost string, err error) {
	// return a mux hostname based on mux returned from ATC
	switch strings.ToUpper(mux) {
	case "ASIA":
		muxHost = "mux-asia"
	case "US":
		muxHost = "mux-us"
	case "EU":
		muxHost = "mux-eu"
	case "NZ":
		muxHost = "mux-nz"
	case "WA":
		muxHost = "mux-wa"
	case "VIC":
		muxHost = "mux-vic"
	case "TAS":
		muxHost = "mux-tas"
	case "SA":
		muxHost = "mux-sa"
	case "QLD":
		muxHost = "mux-qld"
	case "NT":
		muxHost = "mux-nt"
	case "NSW":
		muxHost = "mux-nsw"
	case "ACT":
		muxHost = "mux-act"
	default:
		err = errors.New(fmt.Sprintf("no multiplexer for: %s", mux))
	}

	return muxHost, err
}

func checkFeederContainers(ctx *cli.Context) {
	// cycles through feed-in containers and recreates if needed
	cfcLog := log.With().Logger()
	// cfcLog.Debug().Msg("Running checkFeederContainers")

	// set up docker client
	dockerCtx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		cfcLog.Err(err).Msg("Could not create docker client")
		panic(err)
	}
	defer cli.Close()

	// prepare filter to find feed-in containers
	filters := filters.NewArgs()
	filters.Add("name", "feed-in-*")

	// find containers
	containers, err := cli.ContainerList(dockerCtx, types.ContainerListOptions{Filters: filters})
	if err != nil {
		panic(err)
	}

	// for each container...
	for _, container := range containers {

		// check containers are running latest feed-in image
		if container.Image != ctx.String("feedinimage") {
			cfcLog.Info().Str("container", container.Names[0][1:]).Msg("out of date container being killed for recreation")
			err := cli.ContainerRemove(dockerCtx, container.ID, types.ContainerRemoveOptions{Force: true})
			if err != nil {
				cfcLog.Err(err).Str("container", container.Names[0]).Msg("could not kill out of date container")
			}
		}

		// avoid killing lots of containers in a short duration
		time.Sleep(30 * time.Second)
	}

	// re-launch this goroutine in 5 mins
	time.Sleep(300 * time.Second)
	go checkFeederContainers(ctx)

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
		containerToStart := <-containersToStart

		// prepare logger
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

			// if container is not running, create it
			// cLog.Debug().Msg("starting feed-in container")

			// mux, err := muxHostname(containerToStart.mux)
			// if err != nil {
			// 	cLog.Err(err).Msg("could not assign mux")
			// 	time.Sleep(5 * time.Second)
			// 	break
			// }

			// prepare environment variables for container
			envVars := [...]string{
				fmt.Sprintf("FEEDER_LAT=%f", containerToStart.refLat),
				fmt.Sprintf("FEEDER_LON=%f", containerToStart.refLon),
				fmt.Sprintf("FEEDER_UUID=%s", containerToStart.uuid),
				"READSB_STATS_EVERY=300",
				"READSB_NET_ENABLE=true",
				"READSB_NET_BEAST_INPUT_PORT=12345",
				"READSB_NET_BEAST_OUTPUT_PORT=30005",
				"READSB_NET_ONLY=true",
				fmt.Sprintf("READSB_NET_CONNECTOR=%s,12345,beast_out", containerToStart.mux),
				"PW_INGEST_PUBLISH=location-updates",
				fmt.Sprintf("PW_INGEST_SINK=%s", ctx.String("pwingestpublish")),
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

			// prepare tmpfs config
			tmpFSConfig := make(map[string]string)
			tmpFSConfig["/run"] = "exec,size=64M"
			tmpFSConfig["/var/log"] = ""

			// prepare container host config
			containerHostConfig := container.HostConfig{
				AutoRemove: true,
				Tmpfs:      tmpFSConfig,
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
	}
}

func clientBEASTConnection(ctx *cli.Context, connIn net.Conn, tlsConfig *tls.Config, containersToStart chan startContainerRequest) {
	// handles incoming BEAST connections
	// TODO: need a way to kill a client connection if the UUID is no longer valid (ie: feeder banned)

	cLog := log.With().Str("listener", "BEAST").Logger()

	var (
		sendRecvBufferSize             = 256 * 1024 // 256kB
		clientAuthenticated            = false
		clientFeedInContainerConnected = false
		connOut                        *net.TCPConn
		connOutErr                     error
		connOutAttempts                = 0
		clientApiKey                   uuid.UUID
	)

	defer connIn.Close()

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(connIn.RemoteAddr().String(), ":")[0])
	cLog = cLog.With().IPAddr("src", remoteIP).Logger()

	// cLog.Debug().Msgf("connection established")
	// defer cLog.Debug().Msgf("connection closed")

	buf := make([]byte, sendRecvBufferSize)
	for {

		// read data
		bytesRead, err := connIn.Read(buf)
		if err != nil {
			if err.Error() == "tls: first record does not look like a TLS handshake" {
				cLog.Warn().Msg(err.Error())
				break
			} else if err.Error() == "EOF" {
				if clientAuthenticated {
					cLog.Info().Msg("client disconnected")
				}
				break
			} else {
				cLog.Err(err).Msg("conn.Read")
				break
			}
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if !clientAuthenticated {

			// check TLS handshake
			tlscon := connIn.(*tls.Conn)
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

					// update stats
					stats.setClientConnected(clientApiKey, connIn.RemoteAddr(), "BEAST")
					defer stats.setClientDisconnected(clientApiKey, "BEAST")

					// get feeder info (lat/lon/mux/label)
					refLat, refLon, mux, label, err := getFeederInfo(clientApiKey)
					if err != nil {
						log.Err(err).Msg("getFeederInfo")
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

					// update stats
					stats.setFeederDetails(clientApiKey, label, refLat, refLon)

					// wait for container start
					time.Sleep(5 * time.Second)

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

				// attempt to connect to the feed-in container
				dialAddress := fmt.Sprintf("feed-in-%s:12345", clientApiKey)

				var dstIP net.IP
				dstIPs, err := net.LookupIP(fmt.Sprintf("feed-in-%s", clientApiKey))
				if err != nil {
					cLog.Err(err).Msg("could not perform lookup")
				} else {
					if len(dstIPs) > 0 {
						dstIP = dstIPs[0]
					} else {
						continue
					}
				}

				dstTCPAddr := net.TCPAddr{
					IP:   dstIP,
					Port: 12345,
				}

				// cLog.Debug().Str("dst", dialAddress).Msg("attempting to connect")
				// connOut, connOutErr = net.DialTimeout("tcp", dialAddress, 1*time.Second)
				connOut, connOutErr = net.DialTCP("tcp", nil, &dstTCPAddr)

				if connOutErr != nil {

					// handle connection errors to feed-in container

					cLog.Warn().AnErr("error", connOutErr).Str("dst", dialAddress).Msg("could not connect to feed-in container")
					time.Sleep(1 * time.Second)

					// retry up to 5 times then bail
					connOutAttempts += 1
					if connOutAttempts > 5 {
						break
					}

				} else {

					// connected OK...

					err := connOut.SetKeepAlive(true)
					if err != nil {
						cLog.Err(err).Msg("could not set keep alive")
						break
					}
					err = connOut.SetKeepAlivePeriod(1 * time.Second)
					if err != nil {
						cLog.Err(err).Msg("could not set keep alive period")
						break
					}

					defer connOut.Close()
					clientFeedInContainerConnected = true
					connOutAttempts = 0
					cLog = cLog.With().Str("dst", dialAddress).Logger()
					cLog.Info().Msg("connected to feed-in")

					// update stats
					stats.setOutputConnected(clientApiKey, "FEEDIN", connOut.RemoteAddr())
				}
			}
		}

		// if we are ready to output data to the feed-in container...
		if clientAuthenticated {
			if clientFeedInContainerConnected {

				// if we have data to write...
				if bytesRead > 0 {

					// set deadline of 5 second
					wdErr := connOut.SetDeadline(time.Now().Add(5 * time.Second))
					if wdErr != nil {
						cLog.Err(wdErr).Msg("could not set deadline on connection")
						break
					}

					// attempt to write data in buf (that was read from client connection earlier)
					bytesWritten, err := connOut.Write(buf[:bytesRead])
					if err != nil {
						cLog.Err(err).Msg("error writing to feed-in container")
						break
					}

					// update stats
					stats.incrementByteCounters(clientApiKey, uint64(bytesRead), 0, 0, uint64(bytesWritten), "BEAST")
				}
			}
		}
	}
	// cLog.Debug().Msg("clientBEASTConnection goroutine finishing")
}

func clientMLATConnection(ctx *cli.Context, connIn net.Conn, tlsConfig *tls.Config) {
	// handles incoming MLAT connections
	// TODO: need a way to kill a client connection if the UUID is no longer valid (ie: feeder banned)

	cLog := log.With().Str("listener", "MLAT").Logger()

	var (
		sendRecvBufferSize    = 256 * 1024 // 256kB
		clientAuthenticated   = false
		muxContainerConnected = false
		connOut               *net.TCPConn
		connOutErr            error
		connOutAttempts       = 0
		clientApiKey          uuid.UUID
		mux                   string
		refLat, refLon        float64
		label                 string
	)

	defer connIn.Close()

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(connIn.RemoteAddr().String(), ":")[0])
	cLog = cLog.With().IPAddr("src", remoteIP).Logger()

	// cLog.Debug().Msgf("connection established")
	// defer cLog.Debug().Msgf("connection closed")

	inBuf := make([]byte, sendRecvBufferSize)

	for {

		// read data from client
		bytesRead, err := connIn.Read(inBuf)
		if err != nil {
			if err.Error() == "tls: first record does not look like a TLS handshake" {
				cLog.Warn().Msg(err.Error())
				break
			} else if err.Error() == "EOF" {
				if clientAuthenticated {
					cLog.Info().Msg("client disconnected")
				}
				break
			} else {
				cLog.Err(err).Msg("client read error")
				break
			}
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if !clientAuthenticated {

			// check TLS handshake
			tlscon := connIn.(*tls.Conn)
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

					// get feeder info (lat/lon/mux/label)
					refLat, refLon, mux, label, err = getFeederInfo(clientApiKey)
					if err != nil {
						log.Err(err).Msg("getFeederInfo")
						continue
					}

					// update stats
					stats.setFeederDetails(clientApiKey, label, refLat, refLon)

					// wait for container start
					time.Sleep(5 * time.Second)

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

			// If we aren't yet connected to a mux
			if !muxContainerConnected {

				muxHost, err := muxHostname(mux)
				if err != nil {
					cLog.Err(err).Msg("could not assign mux")
					time.Sleep(5 * time.Second)
					break
				}

				// attempt to connect to the mux container
				dialAddress := fmt.Sprintf("%s:12346", muxHost)

				var dstIP net.IP
				dstIPs, err := net.LookupIP(muxHost)
				if err != nil {
					cLog.Err(err).Msg("could not perform lookup")
				} else {
					if len(dstIPs) > 0 {
						dstIP = dstIPs[0]
					} else {
						continue
					}
				}

				dstTCPAddr := net.TCPAddr{
					IP:   dstIP,
					Port: 12346,
				}

				// cLog.Debug().Str("dst", dialAddress).Msg("attempting to connect")
				connOut, connOutErr = net.DialTCP("tcp", nil, &dstTCPAddr)

				if connOutErr != nil {

					// handle connection errors to feed-in container

					cLog.Warn().AnErr("error", connOutErr).Str("dst", dialAddress).Msg("could not connect to mux container")
					time.Sleep(1 * time.Second)

					// retry up to 5 times then bail
					connOutAttempts += 1
					if connOutAttempts > 5 {
						break
					}

				} else {

					// connected OK...

					err := connOut.SetKeepAlive(true)
					if err != nil {
						cLog.Err(err).Msg("could not set keep alive")
						break
					}
					err = connOut.SetKeepAlivePeriod(1 * time.Second)
					if err != nil {
						cLog.Err(err).Msg("could not set keep alive period")
						break
					}

					defer connOut.Close()
					muxContainerConnected = true

					// update stats
					stats.setClientConnected(clientApiKey, connIn.RemoteAddr(), "MLAT")
					defer stats.setClientDisconnected(clientApiKey, "MLAT")

					connOutAttempts = 0
					cLog = cLog.With().Str("dst", dialAddress).Logger()
					cLog.Info().Msg("connected to mux")

					// update stats
					stats.setOutputConnected(clientApiKey, "MUX", connOut.RemoteAddr())

					// start responder
					go clientMLATResponder(clientApiKey, connOut, connIn, sendRecvBufferSize, cLog)
				}
			}
		}

		// if we are ready to output data to the feed-in container...
		if clientAuthenticated {
			if muxContainerConnected {

				// if we have data to write...
				if bytesRead > 0 {

					// set deadline of 5 second
					wdErr := connOut.SetDeadline(time.Now().Add(5 * time.Second))
					if wdErr != nil {
						cLog.Err(wdErr).Msg("could not set deadline on connection")
						break
					}

					// attempt to write data in buf (that was read from client connection earlier)
					bytesWritten, err := connOut.Write(inBuf[:bytesRead])
					if err != nil {
						cLog.Err(err).Msg("error writing to mux container")
						break
					}

					// update stats
					stats.incrementByteCounters(clientApiKey, uint64(bytesRead), 0, 0, uint64(bytesWritten), "MLAT")
				}
			}
		}
	}
	// cLog.Debug().Msg("clientMLATConnection goroutine finishing")
}

func clientMLATResponder(clientApiKey uuid.UUID, connOut *net.TCPConn, connIn net.Conn, sendRecvBufferSize int, cLog zerolog.Logger) {
	// MLAT traffic is two-way. This func reads from mlat-server and sends back to client.
	// Designed to be run as gorouting

	// cLog.Debug().Msg("clientMLATResponder started")

	outBuf := make([]byte, sendRecvBufferSize)

	for {

		// read data from server
		bytesRead, err := connOut.Read(outBuf)
		if err != nil {
			if err.Error() == "EOF" {
				cLog.Info().Msg("mux disconnected")
				break
			} else if err, ok := err.(net.Error); ok && err.Timeout() {
				// cLog.Debug().AnErr("err", err).Msg("no data to read")
			} else {
				cLog.Err(err).Msg("mux read error")
				break
			}
		}

		// attempt to write data in buf (that was read from mux connection earlier)
		bytesWritten, err := connIn.Write(outBuf[:bytesRead])
		if err != nil {
			cLog.Err(err).Msg("error writing to client")
			break
		}

		// update stats
		stats.incrementByteCounters(clientApiKey, 0, uint64(bytesWritten), uint64(bytesRead), 0, "MLAT")
	}

	// cLog.Debug().Msg("clientMLATResponder finished")
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
				Name:    "listenbeast",
				Usage:   "Address and TCP port server will listen on for BEAST connections",
				Value:   "0.0.0.0:12345",
				EnvVars: []string{"BC_LISTEN_BEAST"},
			},
			&cli.StringFlag{
				Name:    "listenmlat",
				Usage:   "Address and TCP port server will listen on for MLAT connections",
				Value:   "0.0.0.0:12346",
				EnvVars: []string{"BC_LISTEN_MLAT"},
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
			&cli.StringFlag{
				Name:     "pwingestpublish",
				Usage:    "pw_ingest --sink setting in feed-in containers",
				Required: true,
				EnvVars:  []string{"PW_INGEST_SINK"},
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

	// start statistics manager
	log.Info().Msg("starting statsManager")
	go statsManager()

	// start goroutine to regularly pull feeders from atc
	log.Info().Msg("starting updateFeederDB")
	go updateFeederDB(ctx, 60*time.Second)

	// prepare channel for container start requests
	containersToStart := make(chan startContainerRequest)
	defer close(containersToStart)

	// start goroutine to start feeder containers
	log.Info().Msg("starting startFeederContainers")
	go startFeederContainers(ctx, containersToStart)

	// start goroutine to check feed-in containers
	go checkFeederContainers(ctx)

	var wg sync.WaitGroup

	// start listening for incoming BEAST connections
	wg.Add(1)
	go listenBEAST(ctx, &wg, containersToStart)

	// start listening for incoming MLAT connections
	wg.Add(1)
	go listenMLAT(ctx, &wg)

	// wait for all listeners to finish / serve forever
	wg.Wait()

	return nil
}

func listenBEAST(ctx *cli.Context, wg *sync.WaitGroup, containersToStart chan startContainerRequest) {
	// BEAST listener

	// load SSL cert/key
	kpr, err := NewKeypairReloader(ctx.String("cert"), ctx.String("key"), "BEAST")
	if err != nil {
		log.Fatal().Err(err).Msg("Error loading TLS cert and/or key")
	}
	tlsConfig := tls.Config{}
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()

	// start TLS server
	log.Info().Msgf("Starting BEAST listener on %s", ctx.String("listenbeast"))
	tlsListener, err := tls.Listen("tcp", ctx.String("listenbeast"), &tlsConfig)
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
		go clientBEASTConnection(ctx, conn, &tlsConfig, containersToStart)
	}

	wg.Done()
}

func listenMLAT(ctx *cli.Context, wg *sync.WaitGroup) {
	// MLAT listener

	// load SSL cert/key
	kpr, err := NewKeypairReloader(ctx.String("cert"), ctx.String("key"), "MLAT")
	if err != nil {
		log.Fatal().Err(err).Msg("Error loading TLS cert and/or key")
	}
	tlsConfig := tls.Config{}
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()

	// start TLS server
	log.Info().Msgf("Starting MLAT listener on %s", ctx.String("listenmlat"))
	tlsListener, err := tls.Listen("tcp", ctx.String("listenmlat"), &tlsConfig)
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
		go clientMLATConnection(ctx, conn, &tlsConfig)
	}

	wg.Done()
}
