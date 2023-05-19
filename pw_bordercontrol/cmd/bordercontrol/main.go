package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"pw_bordercontrol/lib/logging"

	"github.com/google/uuid"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
)

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
					cLog.Warn().Str("sni", tlscon.ConnectionState().ServerName).Msg("client sent invalid SNI")
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
					// used a chan here so it blocks while waiting for the request to be popped off the chan
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
					cLog.Warn().Str("sni", clientApiKey.String()).Msg("client sent invalid api key")
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
		clientApiKey          uuid.UUID
		mux                   string
		refLat, refLon        float64
		label                 string
	)

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
				e := connIn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close connIn")
				}
				break
			} else if err.Error() == "EOF" {
				if clientAuthenticated {
					cLog.Info().Msg("client disconnected")
				}
				e := connIn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close connIn")
				}
				break
			} else {
				cLog.Err(err).Msg("client read error")
				e := connIn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close connIn")
				}
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
					cLog.Warn().Str("sni", tlscon.ConnectionState().ServerName).Msg("client sent invalid SNI")
					e := connIn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("could not close connIn")
					}
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
					cLog.Warn().Str("sni", clientApiKey.String()).Msg("client sent invalid api key")
					e := connIn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("could not close connIn")
					}
					break
				}

			} else {
				// if TLS handshake is not complete, then kill the connection
				cLog.Warn().Msg("data received before tls handshake")
				e := connIn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close connIn")
				}
				break
			}
		}

		// If the client has been authenticated, then we can do stuff with the data
		if clientAuthenticated {

			// If we aren't yet connected to a mux
			if !muxContainerConnected {

				// attempt to connect to the mux container
				dialAddress := fmt.Sprintf("%s:12346", mux)

				var dstIP net.IP
				dstIPs, err := net.LookupIP(mux)
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

					e := connIn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("could not close connIn")
					}
					break

				} else {

					// connected OK...

					err := connOut.SetKeepAlive(true)
					if err != nil {
						cLog.Err(err).Msg("could not set keep alive")
						e := connIn.Close()
						if e != nil {
							log.Err(e).Caller().Msg("could not close connIn")
						}
						break
					}
					err = connOut.SetKeepAlivePeriod(1 * time.Second)
					if err != nil {
						cLog.Err(err).Msg("could not set keep alive period")
						e := connIn.Close()
						if e != nil {
							log.Err(e).Caller().Msg("could not close connIn")
						}
						break
					}

					muxContainerConnected = true

					// update stats
					stats.setClientConnected(clientApiKey, connIn.RemoteAddr(), "MLAT")
					defer stats.setClientDisconnected(clientApiKey, "MLAT")

					cLog = cLog.With().Str("dst", dialAddress).Logger()
					cLog.Info().Msg("connected to mux")

					// update stats
					stats.setOutputConnected(clientApiKey, "MUX", connOut.RemoteAddr())

				}
			}
		}
		// if we are ready to output data to the feed-in container...
		if clientAuthenticated {
			if muxContainerConnected {
				break
			}
		}
	}

	// if we are ready to output data to the feed-in container...
	if clientAuthenticated {
		if muxContainerConnected {

			wg := sync.WaitGroup{}

			// start responder
			wg.Add(1)
			go clientMLATResponderNTC2NC(clientApiKey, connOut, connIn, sendRecvBufferSize, cLog, &wg)
			wg.Add(1)
			go clientMLATResponderNC2NTC(clientApiKey, connIn, connOut, sendRecvBufferSize, cLog, &wg)
			wg.Wait()

			defer connOut.Close()
			defer connIn.Close()

		}
	}
	// cLog.Debug().Msg("clientMLATConnection goroutine finishing")
}

func clientMLATResponderNTC2NC(clientApiKey uuid.UUID, connOut *net.TCPConn, connIn net.Conn, sendRecvBufferSize int, cLog zerolog.Logger, wg *sync.WaitGroup) {
	// MLAT traffic is two-way. This func reads from mlat-server and sends back to client.
	// Designed to be run as goroutine

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

	wg.Done()

	// e := connOut.Close()
	// if e != nil {
	// 	log.Err(e).Caller().Msg("could not close connOut")
	// }
	// e = connIn.Close()
	// if e != nil {
	// 	log.Err(e).Caller().Msg("could not close connIn")
	// }

	// cLog.Debug().Msg("clientMLATResponder finished")
}

func clientMLATResponderNC2NTC(clientApiKey uuid.UUID, connOut net.Conn, connIn *net.TCPConn, sendRecvBufferSize int, cLog zerolog.Logger, wg *sync.WaitGroup) {
	// MLAT traffic is two-way. This func reads from mlat-server and sends back to client.
	// Designed to be run as goroutine

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

	wg.Done()

	// e := connOut.Close()
	// if e != nil {
	// 	log.Err(e).Caller().Msg("could not close connOut")
	// }
	// e = connIn.Close()
	// if e != nil {
	// 	log.Err(e).Caller().Msg("could not close connIn")
	// }

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
	log.Info().Str("ip", strings.Split(ctx.String("listenbeast"), ":")[0]).Str("port", strings.Split(ctx.String("listenbeast"), ":")[1]).Msg("starting BEAST listener")
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
	log.Info().Str("ip", strings.Split(ctx.String("listenmlat"), ":")[0]).Str("port", strings.Split(ctx.String("listenmlat"), ":")[1]).Msg("starting MLAT listener")
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
