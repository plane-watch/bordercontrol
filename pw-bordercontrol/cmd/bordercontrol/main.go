package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"pw_bordercontrol/lib/logging"

	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
)

var (
	feedInImage            string
	tlsConfig              tls.Config
	kpr                    *keypairReloader
	commithash, committime string
	incomingConnTracker    incomingConnectionTracker
)

const (

	// standardise the protocol name strings
	protoMLAT  = "MLAT"
	protoBeast = "BEAST"

	// banner to display when started
	banner = ` 
 _                   _                          _             _
| |__   ___  _ __ __| | ___ _ __ ___ ___  _ __ | |_ _ __ ___ | |
| '_ \ / _ \| '__/ _' |/ _ \ '__/ __/ _ \| '_ \| __| '__/ _ \| |
| |_) | (_) | | | (_| |  __/ | | (_| (_) | | | | |_| | | (_) | |
|_.__/ \___/|_|  \__,_|\___|_|  \___\___/|_| |_|\__|_|  \___/| |
                                                            _| |_
                                                          _| | | | _
                                                         | | | | |' |
                                                         \          /
                                                          \________/
`
)

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

	// get commit hash and commit time from git info
	commithash = func() string {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					if len(setting.Value) >= 7 {
						return setting.Value[:7]
					} else {
						return "unknown"
					}
				}
			}
		}
		return ""
	}()
	committime = func() string {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.time" {
					return setting.Value
				}
			}
		}
		return ""
	}()
	if len(commithash) < 7 {
		app.Version = "unknown"
	} else {
		app.Version = fmt.Sprintf("%s (%s)", commithash, committime)
	}

	app.Before = func(c *cli.Context) error {
		// Set logging level
		logging.SetLoggingLevel(c)

		// set global var containing feed-in image name
		feedInImage = c.String("feedinimage")

		return nil
	}

	// Final exit
	if err := app.Run(os.Args); nil != err {
		log.Err(err).Msg("Finishing with an error")
		os.Exit(1)
	}

}

func runServer(ctx *cli.Context) error {

	// show banner
	log.Info().Msg(banner)
	log.Info().Str("commithash", commithash).Str("committime", committime).Msg("bordercontrol starting")

	// set up TLS
	// load SSL cert/key
	kpr, err := NewKeypairReloader(ctx.String("cert"), ctx.String("key"))
	if err != nil {
		log.Fatal().Err(err).Msg("Error loading TLS cert and/or key")
	}
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()

	// start statistics manager
	go statsManager()

	// start goroutine to regularly pull feeders from atc
	go updateFeederDB(ctx, 60*time.Second)

	// prepare channel for container start requests
	containersToStart := make(chan startContainerRequest)
	defer close(containersToStart)

	// start goroutine to start feeder containers
	go startFeederContainers(ctx, containersToStart)

	// start goroutine to check feed-in containers
	go checkFeederContainers(ctx)

	var wg sync.WaitGroup
	connNum := connectionNumber{}

	// prepare incoming connection tracker (to allow dropping too-frequent connections)
	// start evictor for incoming connection tracker
	go func() {
		for {
			incomingConnTracker.evict()
			time.Sleep(time.Second * 1)
		}
	}()

	// start listening for incoming BEAST connections
	wg.Add(1)
	go listenBEAST(ctx, &wg, containersToStart, &connNum)

	// start listening for incoming MLAT connections
	wg.Add(1)
	go listenMLAT(ctx, &wg, &connNum)

	// wait for all listeners to finish / serve forever
	wg.Wait()

	return nil
}

func listenBEAST(ctx *cli.Context, wg *sync.WaitGroup, containersToStart chan startContainerRequest, connNum *connectionNumber) {
	// BEAST listener

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
		go clientBEASTConnection(ctx, conn, containersToStart, connNum.GetNum())
	}

	wg.Done()
}

func listenMLAT(ctx *cli.Context, wg *sync.WaitGroup, connNum *connectionNumber) {
	// MLAT listener

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
		go clientMLATConnection(ctx, conn, &tlsConfig, connNum.GetNum())
	}

	wg.Done()
}
