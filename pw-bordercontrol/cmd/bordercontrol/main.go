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
	"syscall"
	"time"

	"pw_bordercontrol/lib/containers"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/logging"
	"pw_bordercontrol/lib/stats"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
)

var (
	feedInImage            string
	feedInContainerPrefix  string
	tlsConfig              tls.Config
	kpr                    *keypairReloader
	commithash, committime string
	incomingConnTracker    incomingConnectionTracker

	chanSIGHUP  chan os.Signal
	chanSIGUSR1 chan os.Signal

	// statsManagerAddr string
	// statsManagerMu   sync.RWMutex
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

	// set up cli context
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
				Value:   "0.0.0.0:12345", // insert Spaceballs joke here
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
				Name:    "feedincontainernetwork",
				Usage:   "feed-in container network",
				Value:   "bordercontrol_feeder",
				EnvVars: []string{"FEED_IN_CONTAINER_NETWORK"},
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

	// set up logging
	logging.IncludeVerbosityFlags(app)
	logging.ConfigureForCli()

	// get commit hash and commit time from git info
	commithash, committime = getRepoInfo()

	// runs before runServer() is called
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
	// returns commit hash and commit time

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

func createSignalChannels() {
	// prepares global channels to catch specific signals

	// SIGHUP = reload TLS/SSL cert/key
	chanSIGHUP = make(chan os.Signal, 1)
	signal.Notify(chanSIGHUP, syscall.SIGHUP)

}

func runServer(ctx *cli.Context) error {

	// Set logging level
	logging.SetLoggingLevel(ctx)

	// show banner
	log.Info().Msg(banner)
	log.Info().Str("commithash", commithash).Str("committime", committime).Msg("bordercontrol starting")

	log.Debug().Str("log-level", zerolog.GlobalLevel().String()).Msg("log level set")

	// prep signal channels
	createSignalChannels()

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
	go stats.Init(":8080")

	// start goroutine to regularly pull feeders from atc
	go func() {
		conf := updateFeederDBConfig{
			updateFreq: time.Second * 60,
			atcUrl:     ctx.String("atcurl"),
			atcUser:    ctx.String("atcuser"),
			atcPass:    ctx.String("atcpass"),
		}
		updateFeederDB(&conf)
	}()

	// initialise container manager
	ContainerManager := containers.ContainerManager{
		FeedInImageName:                    ctx.String("feedinimage"),
		FeedInContainerPrefix:              ctx.String("feedincontainerprefix"),
		FeedInContainerNetwork:             ctx.String("feedincontainernetwork"),
		SignalSkipContainerRecreationDelay: syscall.SIGUSR1,
		PWIngestSink:                       ctx.String("pwingestpublish"),
		Logger:                             log.Logger,
	}
	ContainerManager.Init()

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

		// prep config
		ip := net.ParseIP(strings.Split(ctx.String("listenbeast"), ":")[0])
		port, err := strconv.Atoi(strings.Split(ctx.String("listenbeast"), ":")[1])
		if err != nil {
			log.Err(err).Str("addr", ctx.String("listenbeast")).Msg("invalid listen port")
		}
		conf := listenConfig{
			listenProto: feedprotocol.BEAST,
			listenAddr: net.TCPAddr{
				IP:   ip,
				Port: port,
				Zone: "",
			},
			mgmt: &goRoutineManager{},
		}

		// listen forever
		for {
			listener(conf)
			time.Sleep(time.Second * 1) // if there's a problem, slow down restarting
		}
	}()

	// start listening for incoming MLAT connections
	go func() {

		// prep config
		ip := net.ParseIP(strings.Split(ctx.String("listenmlat"), ":")[0])
		port, err := strconv.Atoi(strings.Split(ctx.String("listenmlat"), ":")[1])
		if err != nil {
			log.Err(err).Str("addr", ctx.String("listenmlat")).Msg("invalid listen port")
		}
		conf := listenConfig{
			listenProto: feedprotocol.MLAT,
			listenAddr: net.TCPAddr{
				IP:   ip,
				Port: port,
				Zone: "",
			},
			mgmt: &goRoutineManager{},
		}

		// listen forever
		for {
			err := listener(conf)
			log.Err(err).Msg("error with listener")
			time.Sleep(time.Second * 1) // if there's a problem, slow down restarting
		}
	}()

	// serve forever
	for {
		time.Sleep(time.Second)
	}

	return nil
}

type listenConfig struct {
	listenProto feedprotocol.Protocol // Protocol handled by the listener
	listenAddr  net.TCPAddr           // TCP address to listen on for incoming stunnel'd BEAST connections
	mgmt        *goRoutineManager     // Goroutune manager. Provides the ability to tell the proxy to self-terminate.
}

func listener(conf listenConfig) error {
	// incoming connection listener

	protoName, err := feedprotocol.GetName(conf.listenProto)
	if err != nil {
		return err
	}

	log := log.With().
		Str("proto", protoName).
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

		// quit if directed
		if conf.mgmt.CheckForStop() {
			break
		}

		// accept incoming connection
		conn, err := tlsListener.Accept()
		if err != nil {
			log.Err(err).Msg("error with tlsListener.Accept")
			return err
		}

		// prep proxy config
		proxyConf := proxyConfig{
			connIn:    conn,
			connProto: conf.listenProto,
			connNum:   incomingConnTracker.getNum(),
			logger:    log,
		}

		// initiate proxying of the connection
		go proxyClientConnection(proxyConf)
	}

	return nil
}
