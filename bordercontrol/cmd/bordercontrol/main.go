/*
Bordercontrol provides authentication and proxying of incoming BEAST and MLAT connections.

When an incoming connection is accepted, Bordercontrol will:

  - Authenticate the incoming connection against ATC, based on the SNI information received as part of the TLS handshake.
  - If BEAST, Bordercontrol will create a feed-in container for the feeder, and proxy the incoming connection through to the feed-in container.
  - If MLAT, Bordercontrol will proxy the incoming connection through to the MLAT server identified by ATC.

Bordercontrol provides statistics via:

  - REST API
  - Prometheus endpoint
  - NATS requests

Bordercontrol can be controlled via:

  - Signals (SIGHUP, SIGUSR1, SIGTERM)
  - NATS requests
*/
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"pw_bordercontrol/lib/containers"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/feedproxy"
	"pw_bordercontrol/lib/listener"
	"pw_bordercontrol/lib/nats_io"
	"pw_bordercontrol/lib/stats"
	"pw_bordercontrol/lib/stunnel"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
	//
	// uncomment for profiling
	// _ "net/http/pprof"
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

var (
	// App config, command line & env var configuration
	app = cli.App{
		Version: "0.0.3",
		Name:    "plane.watch bordercontrol",
		Usage:   "Proxy for multiple stunnel-based BEAST & MLAT endpoints",
		Description: `This program acts as a server for multiple stunnel-based endpoints, ` +
			`authenticates the feeder based on API key (UUID) check against atc.plane.watch, ` +
			`routes data to feed-in containers.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Category: "Network",
				Name:     "listenbeast",
				Usage:    "Address and TCP port to listen on for BEAST connections",
				Value:    ":12345", // insert Spaceballs joke here
				EnvVars:  []string{"BC_LISTEN_BEAST"},
			},
			&cli.StringFlag{
				Category: "Network",
				Name:     "listenmlat",
				Usage:    "Address and TCP port to listen on for MLAT connections",
				Value:    ":12346",
				EnvVars:  []string{"BC_LISTEN_MLAT"},
			},
			&cli.StringFlag{
				Category: "Network",
				Name:     "listenapi",
				Usage:    "Address and TCP port server will listen on for API, stats & Prometheus metrics",
				Value:    ":8080",
				EnvVars:  []string{"BC_LISTEN_API"},
			},
			&cli.StringFlag{
				Category: "SSL/TLS",
				Name:     "cert",
				Usage:    "Server certificate PEM file name (x509)",
				Required: true,
				EnvVars:  []string{"BC_CERT_FILE"},
			},
			&cli.StringFlag{
				Category: "SSL/TLS",
				Name:     "key",
				Usage:    "Server certificate private key PEM file name (x509)",
				Required: true,
				EnvVars:  []string{"BC_KEY_FILE"},
			},
			&cli.StringFlag{
				Category: "ATC API",
				Name:     "atcurl",
				Usage:    "URL to ATC API",
				Required: true,
				EnvVars:  []string{"ATC_URL"},
			},
			&cli.StringFlag{
				Category: "ATC API",
				Name:     "atcuser",
				Usage:    "email username for ATC API",
				Required: true,
				EnvVars:  []string{"ATC_USER"},
			},
			&cli.StringFlag{
				Category: "ATC API",
				Name:     "atcpass",
				Usage:    "password for ATC API",
				Required: true,
				EnvVars:  []string{"ATC_PASS"},
			},
			&cli.IntFlag{
				Category: "ATC API",
				Name:     "atcupdatefreq",
				Usage:    "frequency (in minutes) for valid feeder updates from ATC",
				Value:    1,
				EnvVars:  []string{"ATC_UPDATE_FREQ"},
			},
			&cli.StringFlag{
				Category: "Docker Environmemt",
				Name:     "feedinimage",
				Usage:    "feed-in image name",
				Value:    "feed-in",
				EnvVars:  []string{"FEED_IN_IMAGE"},
			},
			&cli.StringFlag{
				Category: "Docker Environmemt",
				Name:     "feedincontainerprefix",
				Usage:    "feed-in container prefix",
				Value:    "feed-in-",
				EnvVars:  []string{"FEED_IN_CONTAINER_PREFIX"},
			},
			&cli.StringFlag{
				Category: "Docker Environmemt",
				Name:     "feedincontainernetwork",
				Usage:    "feed-in container network",
				Value:    "bordercontrol_feeder",
				EnvVars:  []string{"FEED_IN_CONTAINER_NETWORK"},
			},
			&cli.PathFlag{
				Category: "Docker Environment",
				Name:     "feedinimagecontext",
				Usage:    "feed-in-image build context",
				Value:    "/opt/feed-in/",
				EnvVars:  []string{"FEED_IN_BUILD_CONTEXT"},
			},
			&cli.StringFlag{
				Category: "Docker Environment",
				Name:     "feedinimagedockerfile",
				Usage:    "feed-in image build Dockerfile (relative to context)",
				Value:    "Dockerfile.feeder",
				EnvVars:  []string{"FEED_IN_BUILD_DOCKERFILE"},
			},
			&cli.StringFlag{
				Category: "NATS",
				Name:     "pwingestpublish",
				Usage:    "pw_ingest --sink setting in feed-in containers",
				Required: true,
				EnvVars:  []string{"PW_INGEST_SINK"},
			},
			&cli.StringFlag{
				Category: "NATS",
				Name:     "natsurl",
				Usage:    "NATS URL for stats/control",
				Value:    "",
				EnvVars:  []string{"NATS"},
			},
			&cli.StringFlag{
				Category: "NATS_INSTANCE",
				Name:     "natsinstance",
				Usage:    "NATS instance ID (will be put into header of responses). Default: hostname",
				Value:    "",
				EnvVars:  []string{"NATS_INSTANCE"},
			},
			&cli.BoolFlag{
				Category: "Logging",
				Name:     "verbose",
				Usage:    "Change default log level from Info to Debug",
				Value:    false,
				EnvVars:  []string{"BC_VERBOSE"},
			},
		},
	}

	// channel for SIGTERM
	sigTermChan = make(chan os.Signal)

	// finish is a wrapper for os.Exit that can be overridden for testing
	Finish = func(code int) {
		os.Exit(code)
	}

	// When app was started. To calculate uptime.
	startTime time.Time
)

// uncomment for profiling
//
// func init() {
// 	go func() {
// 		http.ListenAndServe(":1234", nil)
// 	}()
// }

// main initialises the application, before RunServer is called.
func main() {

	// record start time
	startTime = time.Now()

	// add extra stuff to version
	commitHash, commitTime := getRepoInfo()
	app.Version = fmt.Sprintf("%s (%s), %s", app.Version, commitHash, commitTime)

	// set action when run
	app.Action = runServer

	// set up logging
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})

	// run & final exit
	err := app.Run(os.Args)
	if err != nil {
		log.Err(err).Msg("finished with error")
		Finish(1)
	} else {
		log.Info().Msg("finished without error")
		Finish(0)
	}
}

// getRepoInfo returns the git commit hash and commit time of the bordercontrol repo as strings.
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

// logNumGoroutines will log the number of goroutines at debug level.
// The number of goroutines will be logged every freq.
// The context ctx can be used to end this function.
func logNumGoroutines(ctx context.Context, freq time.Duration) {
	last := runtime.NumGoroutine()
	for {
		select {
		case <-time.After(freq):
			time.Sleep(freq)
			now := runtime.NumGoroutine()
			log.Debug().Int("goroutines", now).Int("delta", now-last).Msg("number of goroutines")
			last = now
		case <-ctx.Done():
			log.Debug().Msg("shutting down logNumGoroutines")
			return
		}
	}
}

// listenWithContext runs listener.NewListener until context ctx is cancelled.
// listenAddr is the local ip:port to listen on for incomming connections.
// proto is the protocol, defined in lib/feedprotocol.
// feedInContainerPrefix is the prefix used for feed-in containers (for BEAST connections).
// innerConnectionPort is the destination port for the feed-in container or MLAT sever.
func listenWithContext(ctx context.Context, listenAddr string, proto feedprotocol.Protocol, feedInContainerPrefix string, innerConnectionPort int) {
	for {
		l, err := listener.NewListener(listenAddr, proto, feedInContainerPrefix, innerConnectionPort)
		if err != nil {
			log.Err(err).Str("proto", proto.Name()).Str("addr", listenAddr).Msg("error creating listener")
		}
		err = l.Run(ctx)
		if err != nil {
			log.Err(err).Str("proto", proto.Name()).Msg("error with listener")
		}
		select {
		case <-ctx.Done(): // exit on context closure
			return
		case <-time.After(time.Second): // if there's a problem, slow down restarting
		}
	}
}

// runServer runs the server (duh).
// It is run via urfave/cli/v2 app.Run in main().
func runServer(cliContext *cli.Context) error {

	// prepare waitgroup for goroutines started by this func.
	wg := sync.WaitGroup{}

	// set up context for clean exit
	ctx := context.Background()
	ctx, cleanExit := context.WithCancel(ctx)
	defer cleanExit()

	// Set logging level
	if cliContext.Bool("verbose") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// initial logging
	log.Info().Msg(banner) // show awesome banner
	log.Info().Str("version", cliContext.App.Version).Msg("bordercontrol starting")
	log.Debug().Str("log-level", zerolog.GlobalLevel().String()).Msg("log level set")

	// if debug then show some extra goodies
	if zerolog.GlobalLevel() == zerolog.DebugLevel {
		// display number active goroutines
		go logNumGoroutines(ctx, time.Minute*5)
	}

	// initialise nats subsystem
	// connect to nats for control/stats/etc
	natsConf := nats_io.NatsConfig{
		Url:       cliContext.String("natsurl"),
		Instance:  cliContext.String("natsinstance"),
		Version:   cliContext.App.Version,
		StartTime: startTime,
	}
	err := natsConf.Init()
	if err != nil {
		log.Err(err).Msg("error initialising nats subsystem")
		return err
	}

	// initialise ssl/tls subsystem
	err = stunnel.Init(ctx, syscall.SIGHUP)
	if err != nil {
		log.Err(err).Msg("error starting stunnel subsystem")
		return err
	}

	// load SSL cert/key
	err = stunnel.LoadCertAndKeyFromFile(cliContext.String("cert"), cliContext.String("key"))
	if err != nil {
		log.Err(err).Msg("error loading TLS cert and/or key")
		return err
	}

	// initialise statistics manager
	err = stats.Init(ctx, cliContext.String("listenapi"))
	if err != nil {
		log.Err(err).Msg("error initialising statistics manager")
		return err
	}

	// initialise feedproxy
	feedProxyConf := feedproxy.ProxyConfig{
		UpdateFrequency: time.Minute * time.Duration(cliContext.Int("atcupdatefreq")),
		ATCUrl:          cliContext.String("atcurl"),
		ATCUser:         cliContext.String("atcuser"),
		ATCPass:         cliContext.String("atcpass"),
	}
	err = feedproxy.Init(ctx, &feedProxyConf)
	if err != nil {
		log.Err(err).Msg("error initialising proxy subsystem")
		return err
	}

	// initialise container manager
	ContainerManager := containers.ContainerManager{
		FeedInImageName:                    cliContext.String("feedinimage"),
		FeedInImageBuildContext:            cliContext.String("feedinimagecontext"),
		FeedInImageBuildContextDockerfile:  cliContext.String("feedinimagedockerfile"),
		FeedInContainerPrefix:              cliContext.String("feedincontainerprefix"),
		FeedInContainerNetwork:             cliContext.String("feedincontainernetwork"),
		SignalSkipContainerRecreationDelay: syscall.SIGUSR1,
		PWIngestSink:                       cliContext.String("pwingestpublish"),
		Logger:                             log.Logger,
	}
	err = ContainerManager.Run()
	if err != nil {
		log.Err(err).Msg("error initialising container manager")
		return err
	}

	// set up channel to catch SIGTERM
	// sigTermChan =
	signal.Notify(sigTermChan, syscall.SIGTERM)

	// handle SIGTERM
	wg.Add(1)
	go func() {
		defer wg.Done()
		// wait for SIGTERM
		_ = <-sigTermChan
		log.Info().Msg("received SIGTERM")
		// close main context
		cleanExit()
	}()

	// start listening for incoming BEAST connections
	wg.Add(1)
	go func() {
		defer wg.Done()
		// listen until context close
		listenWithContext(ctx, cliContext.String("listenbeast"), feedprotocol.BEAST, cliContext.String("feedincontainerprefix"), 12345)
	}()

	// start listening for incoming MLAT connections
	wg.Add(1)
	go func() {
		defer wg.Done()
		// listen until context close
		listenWithContext(ctx, cliContext.String("listenmlat"), feedprotocol.MLAT, cliContext.String("feedincontainerprefix"), 12346)
	}()

	// wait for listeners to finish (until context closure)
	wg.Wait()

	// stop container manager
	err = feedproxy.Close(&feedProxyConf)
	if err != nil {
		log.Err(err).Msg("error closing proxy subsystem")
	}

	// stop container manager
	err = ContainerManager.Stop()
	if err != nil {
		log.Err(err).Msg("error stopping container manager")
	}

	// stop stats subsystem
	err = stats.Close()
	if err != nil {
		log.Err(err).Msg("error stopping statistics subsystem")
	}

	// stop stunnel subsystem
	err = stunnel.Close()
	if err != nil {
		log.Err(err).Msg("error stopping stunnel subsystem")
	}

	// stop nats subsystem
	err = natsConf.Close()
	if err != nil {
		log.Err(err).Msg("error stopping nats subsystem")
	}

	return nil
}
