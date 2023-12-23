package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"pw_bordercontrol/lib/logging"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
)

type feedProtocol string

var (
	feedInImage            string
	feedInContainerPrefix  string
	tlsConfig              tls.Config
	kpr                    *keypairReloader
	commithash, committime string
	incomingConnTracker    incomingConnectionTracker

	chanSIGHUP  chan os.Signal
	chanSIGUSR1 chan os.Signal

	statsManagerAddr string
	statsManagerMu   sync.RWMutex

	// standardise the protocol name strings
	protoMLAT  feedProtocol = "MLAT"
	protoBEAST feedProtocol = "BEAST"
)

const (

	// banner to display when started
	banner = ` 
 _                   _                          _             __
| |__   ___  _ __ __| | ___ _ __ ___ ___  _ __ | |_ _ __ ___ /  |
| '_ \ / _ \| '__/ _' |/ _ \ '__/ __/ _ \| '_ \| __| '__/ _ \|  |
| |_) | (_) | | | (_| |  __/ | | (_| (_) | | | | |_| | | (_) |  |
|_.__/ \___/|_|  \__,_|\___|_|  \___\___/|_| |_|\__|_|  \___/|  |
                                                         |___|  |____/-| ~ ~ ~
                                                         *|__|  |____---- ~ ~
                                                         |   |  |    \-| ~ ~ ~
        b y :   p l a n e . w a t c h                        |  |
                                                             |  |
                                                             |  |
                                                             \__|
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
				Name:    "feedinimage",
				Usage:   "feed-in image name",
				Value:   "feed-in",
				EnvVars: []string{"FEED_IN_IMAGE"},
			},
			&cli.StringFlag{
				Name:    "feedincontainerprefix",
				Usage:   "feed-in container prefix",
				Value:   "feed-in-",
				EnvVars: []string{"FEED_IN_CONTAINER_PREFIX"},
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
	commithash, committime = getRepoInfo()

	app.Before = func(ctx *cli.Context) error {

		// set global var containing feed-in image name
		feedInImage = ctx.String("feedinimage")

		// set global var containing feed-in container prefix
		feedInContainerPrefix = ctx.String("feedincontainerprefix")

		return nil
	}

	// Final exit
	if err := app.Run(os.Args); nil != err {
		log.Err(err).Msg("Finishing with an error")
		os.Exit(1)
	}

}

func getRepoInfo() (commitHash, commitTime string) {

	commitHash = "unknown"
	commitTime = "unknown"

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				if len(setting.Value) >= 7 {
					commitHash = setting.Value[:7]
				}
			}
		}
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.time" {
				commitTime = setting.Value
			}
		}
	}

	return commitHash, commitTime

}

func prepSignalChannels() {
	// prepares global channels to catch specific signals

	// SIGHUP = reload TLS/SSL cert/key
	chanSIGHUP = make(chan os.Signal, 1)
	signal.Notify(chanSIGHUP, syscall.SIGHUP)

	// SIGUSR1 = skip delay for not current feed-in container recreation
	chanSIGUSR1 = make(chan os.Signal, 1)
	signal.Notify(chanSIGUSR1, syscall.SIGUSR1)
}

func runServer(ctx *cli.Context) error {

	// Set logging level
	logging.SetLoggingLevel(ctx)

	// show banner
	log.Info().Msg(banner)
	log.Info().Str("commithash", commithash).Str("committime", committime).Msg("bordercontrol starting")

	log.Debug().Str("log-level", zerolog.GlobalLevel().String()).Msg("Logging Set")

	// prep signal channels
	prepSignalChannels()

	// set up TLS
	// load SSL cert/key
	kpr, err := NewKeypairReloader(ctx.String("cert"), ctx.String("key"), chanSIGHUP)
	if err != nil {
		log.Fatal().Err(err).Msg("error loading TLS cert and/or key")
	}
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()

	// display number active goroutines
	go func() {
		last := runtime.NumGoroutine()
		for {
			time.Sleep(5 * time.Minute)
			now := runtime.NumGoroutine()
			log.Debug().Int("goroutines", now).Int("delta", now-last).Msg("number of goroutines")
			last = now
		}
	}()

	// start statistics manager
	go statsManager(":8080")

	// start goroutine to regularly pull feeders from atc
	go updateFeederDB(ctx, 60*time.Second)

	// prepare channel for container start requests
	containersToStartRequests := make(chan startContainerRequest)
	defer close(containersToStartRequests)

	// prepare channel for container start responses
	containersToStartResponses := make(chan startContainerResponse)
	defer close(containersToStartResponses)

	// start goroutine to start feeder containers

	go func() {
		for {
			conf := &startFeederContainersConfig{
				feedInImageName:            ctx.String("feedinimage"),
				feedInContainerPrefix:      ctx.String("feedincontainerprefix"),
				pwIngestPublish:            ctx.String("pwingestpublish"),
				containersToStartRequests:  containersToStartRequests,
				containersToStartResponses: containersToStartResponses,
			}
			_ = startFeederContainers(*conf)
			time.Sleep(1 * time.Minute)
		}
	}()

	// start goroutine to check feed-in containers
	go func() {
		for {
			conf := &checkFeederContainersConfig{
				feedInImageName:          ctx.String("feedinimage"),
				feedInContainerPrefix:    ctx.String("feedincontainerprefix"),
				checkFeederContainerSigs: chanSIGUSR1,
			}
			_ = checkFeederContainers(*conf)
			time.Sleep(1 * time.Minute)
		}
	}()

	// prepare incoming connection tracker (to allow dropping too-frequent connections)
	// start evictor for incoming connection tracker
	go func() {
		for {
			incomingConnTracker.evict()
			time.Sleep(time.Second * 1)
		}
	}()

	// start listening for incoming BEAST connections
	go func() {
		ip := net.ParseIP(strings.Split(ctx.String("listenbeast"), ":")[0])
		port, err := strconv.Atoi(strings.Split(ctx.String("listenbeast"), ":")[1])
		if err != nil {
			log.Err(err).Str("addr", ctx.String("listenbeast")).Msg("invalid listen port")
		}
		conf := &listenConfig{
			listenAddr: net.TCPAddr{
				IP:   ip,
				Port: port,
				Zone: "",
			},
		}
		for {
			listener(*conf)
			time.Sleep(time.Second * 1)
		}
	}()

	// start listening for incoming MLAT connections
	go func() {
		ip := net.ParseIP(strings.Split(ctx.String("listenmlat"), ":")[0])
		port, err := strconv.Atoi(strings.Split(ctx.String("listenmlat"), ":")[1])
		if err != nil {
			log.Err(err).Str("addr", ctx.String("listenmlat")).Msg("invalid listen port")
		}
		conf := &listenConfig{
			listenAddr: net.TCPAddr{
				IP:   ip,
				Port: port,
				Zone: "",
			},
		}
		for {
			listener(*conf)
			time.Sleep(time.Second * 1)
		}
	}()

	// serve forever
	for {
	}

	return nil
}

type listenConfig struct {
	listenProto                feedProtocol                // Protocol handled by the listener
	listenAddr                 net.TCPAddr                 // TCP address to listen on for incoming stunnel'd BEAST connections
	containersToStartRequests  chan startContainerRequest  // Channel to send container start requests to.
	containersToStartResponses chan startContainerResponse // Channel to receive container start responses from.
}

func listener(conf listenConfig) {
	// BEAST listener

	log := log.With().
		Str("proto", string(conf.listenProto)).
		Str("ip", conf.listenAddr.IP.String()).
		Int("port", conf.listenAddr.Port).
		Logger()

	// start TLS server
	log.Info().Msg("starting listener")
	tlsListener, err := tls.Listen(
		"tcp",
		fmt.Sprintf("%s:%d", conf.listenAddr.IP.String(), conf.listenAddr.Port),
		&tlsConfig,
	)
	if err != nil {
		log.Err(err).Msg("error with tls.Listen")
		os.Exit(1)
	}
	defer tlsListener.Close()

	// handle incoming connections
	for {
		conn, err := tlsListener.Accept()
		if err != nil {
			log.Err(err).Msg("error with tlsListener.Accept")
			continue
		}
		go proxyClientConnection(conn, string(conf.listenProto), incomingConnTracker.getNum(), conf.containersToStartRequests, conf.containersToStartResponses)
	}
}
