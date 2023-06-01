package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

func authenticateFeeder(ctx *cli.Context, connIn net.Conn, log zerolog.Logger) (clientApiKey uuid.UUID, refLat, refLon float64, mux, label string, err error) {
	// authenticates a feeder

	// check TLS handshake
	tlscon := connIn.(*tls.Conn)
	if tlscon.ConnectionState().HandshakeComplete {

		// check valid uuid was returned as ServerName (sni)
		clientApiKey, err = uuid.Parse(tlscon.ConnectionState().ServerName)
		if err != nil {
			err := errors.New("client sent invalid SNI")
			return clientApiKey, refLat, refLon, mux, label, err
		}

		// check valid api key
		if isValidApiKey(clientApiKey) {

			// update log context with client uuid
			log = log.With().Str("uuid", clientApiKey.String()).Logger()
			log.Info().Msg("client connected")

			// update stats
			stats.setClientConnected(clientApiKey, connIn.RemoteAddr(), "BEAST")
			defer stats.setClientDisconnected(clientApiKey, "BEAST")

			// get feeder info (lat/lon/mux/label) from atc cache
			refLat, refLon, mux, label, err = getFeederInfo(clientApiKey)
			if err != nil {
				return clientApiKey, refLat, refLon, mux, label, err
			}

			// update stats
			stats.setFeederDetails(clientApiKey, label, refLat, refLon)

		} else {
			// if API is not valid, then kill the connection
			err := errors.New("client sent invalid api key")
			return clientApiKey, refLat, refLon, mux, label, err
		}

	} else {
		// if TLS handshake is not complete, then kill the connection
		err := errors.New("data received before tls handshake")
		return clientApiKey, refLat, refLon, mux, label, err
	}
	return clientApiKey, refLat, refLon, mux, label, err
}

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
			clientApiKey, _, _, mux, _, err = authenticateFeeder(ctx, clientConn, cLog)
			if err != nil {
				cLog.Err(err)
				break
			}
			clientAuthenticated = true
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

func clientBEASTConnection(ctx *cli.Context, connIn net.Conn, containersToStart chan startContainerRequest) {
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
		refLat, refLon                 float64
		mux, label                     string
	)

	defer connIn.Close()

	// update log context with client IP
	remoteIP := net.ParseIP(strings.Split(connIn.RemoteAddr().String(), ":")[0])
	cLog = cLog.With().IPAddr("src", remoteIP).Logger()

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
			clientApiKey, refLat, refLon, mux, label, err = authenticateFeeder(ctx, connIn, cLog)
			if err != nil {
				cLog.Err(err)
				break
			}
			clientAuthenticated = true

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

			// wait for container start
			time.Sleep(5 * time.Second)
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
}
