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

func clientMLATConnection(ctx *cli.Context, clientConn net.Conn, tlsConfig *tls.Config) {
	// handles incoming MLAT connections
	// TODO: need a way to kill a client connection if the UUID is no longer valid (ie: feeder banned)

	cLog := log.With().Str("listener", "MLAT").Logger()

	var (
		sendRecvBufferSize    = 256 * 1024 // 256kB
		clientAuthenticated   = false
		muxContainerConnected = false
		muxConn               *net.TCPConn
		muxConnErr            error
		clientApiKey          uuid.UUID
		mux                   string
		refLat, refLon        float64
		label                 string
		bytesRead             int
		err                   error
	)

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(clientConn.RemoteAddr().String(), ":")[0])
	cLog = cLog.With().IPAddr("src", remoteIP).Logger()

	// cLog.Debug().Msgf("connection established")
	// defer cLog.Debug().Msgf("connection closed")

	inBuf := make([]byte, sendRecvBufferSize)

	for {

		// read data from client
		bytesRead, err = clientConn.Read(inBuf)
		if err != nil {
			if err.Error() == "tls: first record does not look like a TLS handshake" {
				cLog.Warn().Msg(err.Error())
				e := clientConn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close clientConn")
				}
				break
			} else if err.Error() == "EOF" {
				if clientAuthenticated {
					cLog.Info().Msg("client disconnected")
				}
				e := clientConn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close clientConn")
				}
				break
			} else {
				cLog.Err(err).Msg("client read error")
				e := clientConn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close clientConn")
				}
				break
			}
		}

		// When the first data is sent, the TLS handshake should take place.
		// Accordingly, we need to track the state...
		if !clientAuthenticated {

			// check TLS handshake
			tlscon := clientConn.(*tls.Conn)
			if tlscon.ConnectionState().HandshakeComplete {

				// check valid uuid was returned as ServerName (sni)
				clientApiKey, err = uuid.Parse(tlscon.ConnectionState().ServerName)
				if err != nil {
					cLog.Warn().Str("sni", tlscon.ConnectionState().ServerName).Msg("client sent invalid SNI")
					e := clientConn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("could not close clientConn")
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
					e := clientConn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("could not close clientConn")
					}
					break
				}

			} else {
				// if TLS handshake is not complete, then kill the connection
				cLog.Warn().Msg("data received before tls handshake")
				e := clientConn.Close()
				if e != nil {
					log.Err(e).Caller().Msg("could not close clientConn")
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
				muxConn, muxConnErr = net.DialTCP("tcp", nil, &dstTCPAddr)

				if muxConnErr != nil {

					// handle connection errors to feed-in container

					cLog.Warn().AnErr("error", muxConnErr).Str("dst", dialAddress).Msg("could not connect to mux container")
					time.Sleep(1 * time.Second)

					e := clientConn.Close()
					if e != nil {
						log.Err(e).Caller().Msg("could not close clientConn")
					}
					break

				} else {

					// connected OK...

					err := muxConn.SetKeepAlive(true)
					if err != nil {
						cLog.Err(err).Msg("could not set keep alive")
						e := clientConn.Close()
						if e != nil {
							log.Err(e).Caller().Msg("could not close clientConn")
						}
						break
					}
					err = muxConn.SetKeepAlivePeriod(1 * time.Second)
					if err != nil {
						cLog.Err(err).Msg("could not set keep alive period")
						e := clientConn.Close()
						if e != nil {
							log.Err(e).Caller().Msg("could not close clientConn")
						}
						break
					}

					muxContainerConnected = true

					// update stats
					stats.setClientConnected(clientApiKey, clientConn.RemoteAddr(), "MLAT")
					defer stats.setClientDisconnected(clientApiKey, "MLAT")

					cLog = cLog.With().Str("dst", dialAddress).Logger()
					cLog.Info().Msg("connected to mux")

					// update stats
					stats.setOutputConnected(clientApiKey, "MUX", muxConn.RemoteAddr())

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

			// write outstanding data
			_, err := muxConn.Write(inBuf[:bytesRead])
			if err != nil {
				cLog.Err(err).Msg("error writing to client")
			}

			// start responder
			wg.Add(1)
			go mlatTcpForwarderM2C(clientApiKey, muxConn, clientConn, sendRecvBufferSize, cLog, &wg)
			wg.Add(1)
			go mlatTcpForwarderC2M(clientApiKey, clientConn, muxConn, sendRecvBufferSize, cLog, &wg)
			wg.Wait()

			defer muxConn.Close()
			defer clientConn.Close()

		}
	}
	// cLog.Debug().Msg("clientMLATConnection goroutine finishing")
}

func mlatTcpForwarderM2C(clientApiKey uuid.UUID, muxConn *net.TCPConn, clientConn net.Conn, sendRecvBufferSize int, cLog zerolog.Logger, wg *sync.WaitGroup) {
	// MLAT traffic is two-way. This func reads from mlat-server and sends back to client.
	// Designed to be run as goroutine

	// cLog.Debug().Msg("clientMLATResponder started")

	outBuf := make([]byte, sendRecvBufferSize)

	for {

		// read data from server
		bytesRead, err := muxConn.Read(outBuf)
		if err != nil {
			if err.Error() == "EOF" {
				cLog.Info().Caller().Msg("mux disconnected from client")
				break
			} else if err, ok := err.(net.Error); ok && err.Timeout() {
				// cLog.Debug().AnErr("err", err).Msg("no data to read")
			} else {
				cLog.Err(err).Msg("mux read error")
				break
			}
		}

		// attempt to write data in buf (that was read from mux connection earlier)
		bytesWritten, err := clientConn.Write(outBuf[:bytesRead])
		if err != nil {
			cLog.Err(err).Msg("error writing to client")
			break
		}

		// update stats
		stats.incrementByteCounters(clientApiKey, 0, uint64(bytesWritten), uint64(bytesRead), 0, "MLAT")
	}

	wg.Done()

}

func mlatTcpForwarderC2M(clientApiKey uuid.UUID, clientConn net.Conn, muxConn *net.TCPConn, sendRecvBufferSize int, cLog zerolog.Logger, wg *sync.WaitGroup) {
	// MLAT traffic is two-way. This func reads from mlat-server and sends back to client.
	// Designed to be run as goroutine

	// cLog.Debug().Msg("clientMLATResponder started")

	outBuf := make([]byte, sendRecvBufferSize)

	for {

		// read data from server
		bytesRead, err := clientConn.Read(outBuf)
		if err != nil {
			if err.Error() == "EOF" {
				cLog.Info().Caller().Msg("client disconnected from mux")
				break
			} else if err, ok := err.(net.Error); ok && err.Timeout() {
				// cLog.Debug().AnErr("err", err).Msg("no data to read")
			} else {
				cLog.Err(err).Msg("mux read error")
				break
			}
		}

		// attempt to write data in buf (that was read from mux connection earlier)
		bytesWritten, err := muxConn.Write(outBuf[:bytesRead])
		if err != nil {
			cLog.Err(err).Msg("error writing to client")
			break
		}

		// update stats
		stats.incrementByteCounters(clientApiKey, uint64(bytesRead), 0, 0, uint64(bytesWritten), "MLAT")
	}

	wg.Done()

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
