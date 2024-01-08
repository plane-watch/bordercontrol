package main

import (
	"context"
	"errors"
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

	"pw_bordercontrol/lib/containers"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/feedproxy"
	"pw_bordercontrol/lib/logging"
	"pw_bordercontrol/lib/nats_io"
	"pw_bordercontrol/lib/stats"
	"pw_bordercontrol/lib/stunnel"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/urfave/cli/v2"
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
	// main app settings
	app = cli.App{
		Version: "0.0.1",
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
				Value:    "/opt/pw-feed-in/",
				EnvVars:  []string{"FEED_IN_BUILD_CONTEXT"},
			},
			&cli.StringFlag{
				Category: "Docker Environment",
				Name:     "feedinimagedockerfile",
				Usage:    "feed-in-image build Dockerfile (relative to context)",
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
		},
	}

	startTime time.Time // When app was started. To calculate uptime.

	// allow override of these functions to simplify testing
	stunnelNewListenerWrapper = func(network string, laddr string) (l net.Listener, err error) {
		return stunnel.NewListener(network, laddr)
	}
	proxyConnStartWrapper = func(f *feedproxy.ProxyConnection, ctx context.Context) error {
		return f.Start(ctx)
	}
	feedproxyGetConnectionNumberWrapper = func() (num uint, err error) {
		return feedproxy.GetConnectionNumber()
	}
)

func main() {

	// record start time
	startTime = time.Now()

	// add extra stuff to version
	commitHash, commitTime := getRepoInfo()
	app.Version = fmt.Sprintf("%s (%s), %s", app.Version, commitHash, commitTime)

	// set action when run
	app.Action = runServer

	// set up logging
	logging.IncludeVerbosityFlags(&app)
	logging.ConfigureForCli()

	// run & final exit
	err := app.Run(os.Args)
	if err != nil {
		log.Err(err).Msg("finished with error")
		os.Exit(1)
	} else {
		log.Info().Msg("finished without error")
		os.Exit(0)
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

func logNumGoroutines(freq time.Duration, stopChan chan bool) {
	// logs number of goroutines to debug level
	last := runtime.NumGoroutine()
	for {
		select {
		case <-time.After(freq):
			time.Sleep(freq)
			now := runtime.NumGoroutine()
			log.Debug().Int("goroutines", now).Int("delta", now-last).Msg("number of goroutines")
			last = now
		case <-stopChan:
			return
		}
	}
}

func prepListenerConfig(listenAddr string, proto feedprotocol.Protocol, feedInContainerPrefix string) *listenConfig {
	// prep listener config
	ip := net.ParseIP(strings.Split(listenAddr, ":")[0])
	if ip == nil {
		ip = net.IPv4(0, 0, 0, 0) // 0.0.0.0
	}
	port, err := strconv.Atoi(strings.Split(listenAddr, ":")[1])
	if err != nil {
		log.Fatal().Err(err).Str("proto", proto.Name()).Str("addr", listenAddr).Msg("invalid listen port")
	}
	return &listenConfig{
		listenProto: proto,
		listenAddr: net.TCPAddr{
			IP:   ip,
			Port: port,
			Zone: "",
		},
		feedInContainerPrefix: feedInContainerPrefix,
	}
}

func runServer(cliContext *cli.Context) error {

	// Set logging level
	logging.SetLoggingLevel(cliContext)

	// initial logging
	log.Info().Msg(banner) // show awesome banner
	log.Info().Str("version", cliContext.App.Version).Msg("bordercontrol starting")
	log.Debug().Str("log-level", zerolog.GlobalLevel().String()).Msg("log level set")

	// if debug then show some extra goodies
	if zerolog.GlobalLevel() == zerolog.DebugLevel {
		// display number active goroutines
		logNumGoroutinesStopChan := make(chan bool)
		go logNumGoroutines(time.Minute*5, logNumGoroutinesStopChan)
	}

	// connect to nats for control/stats/etc
	natsConf := nats_io.NatsConfig{
		Url:       cliContext.String("natsurl"),
		Instance:  cliContext.String("natsinstance"),
		Version:   cliContext.App.Version,
		StartTime: startTime,
	}
	err := natsConf.Init()
	if err != nil {
		log.Fatal().Err(err).Msg("could not init nats")
	}

	// initialise ssl/tls subsystem
	stunnel.Init(syscall.SIGHUP)

	// load SSL cert/key
	err = stunnel.LoadCertAndKeyFromFile(cliContext.String("cert"), cliContext.String("key"))
	if err != nil {
		log.Fatal().Err(err).Msg("error loading TLS cert and/or key")
	}

	// start statistics manager
	err = stats.Init(cliContext.String("listenapi"))
	if err != nil {
		log.Err(err).Msg("could not start statistics manager")
		return err
	}

	// initialise feedproxy
	feedProxyConf := feedproxy.FeedProxyConfig{
		UpdateFrequency: time.Minute * time.Duration(cliContext.Int("atcupdatefreq")),
		ATCUrl:          cliContext.String("atcurl"),
		ATCUser:         cliContext.String("atcuser"),
		ATCPass:         cliContext.String("atcpass"),
	}
	feedproxy.Init(&feedProxyConf)

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
	ContainerManager.Init()

	wg := sync.WaitGroup{}

	// set up context for clean exit
	ctx := context.Background()
	ctx, cleanExit := context.WithCancel(ctx)

	// set up channel to catch SIGTERM
	sigTermChan := make(chan os.Signal)
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
		for {
			err := listener(ctx, prepListenerConfig(cliContext.String("listenbeast"), feedprotocol.BEAST, cliContext.String("feedincontainerprefix")))
			if err != nil {
				log.Err(err).Str("proto", feedprotocol.ProtocolNameBEAST).Msg("error with listener")
			}
			select {
			case <-ctx.Done(): // exit on context closure
				return
			case <-time.After(time.Second): // if there's a problem, slow down restarting
			}
		}
	}()

	// start listening for incoming MLAT connections
	wg.Add(1)
	go func() {
		defer wg.Done()
		// listen until context close
		for {
			err := listener(ctx, prepListenerConfig(cliContext.String("listenmlat"), feedprotocol.MLAT, cliContext.String("feedincontainerprefix")))
			if err != nil {
				log.Err(err).Str("proto", feedprotocol.ProtocolNameMLAT).Msg("error with listener")
			}
			select {
			case <-ctx.Done(): // exit on context closure
				return
			case <-time.After(time.Second): // if there's a problem, slow down restarting
			}
		}
	}()

	// serve until context closure
	wg.Wait()
	return nil
}

type listenConfig struct {
	listenProto           feedprotocol.Protocol // Protocol handled by the listener
	listenAddr            net.TCPAddr           // TCP address to listen on for incoming stunnel'd BEAST connections
	feedInContainerPrefix string
}

func listener(ctx context.Context, conf *listenConfig) error {
	// incoming connection listener

	wg := sync.WaitGroup{}

	// get protocol name
	protoName, err := feedprotocol.GetName(conf.listenProto)
	if err != nil {
		return err
	}

	// update log context
	log := log.With().
		Str("proto", protoName).
		Int("port", conf.listenAddr.Port).
		Logger()

	// start TLS server
	log.Info().Msg("starting listener")
	stunnelListener, err := stunnelNewListenerWrapper(
		"tcp",
		fmt.Sprintf("%s:%d", conf.listenAddr.IP.String(), conf.listenAddr.Port),
	)
	if err != nil {
		log.Err(err).Msg("error staring listener")
		return err
	}
	defer stunnelListener.Close()

	// handle context closure
	go func() {
		// wait for context closure
		_ = <-ctx.Done()
		// let user know what's happenning
		log.Info().Msg("shutting down listener")
		// close stunnelListener, which will cause any .Accept() to throw net.ErrClosed
		err := stunnelListener.Close()
		if err != nil {
			log.Err(err).Msg("error closing listener")
		}
	}()

	// handle incoming connections
	for {

		// accept incoming connection
		conn, err := stunnelListener.Accept()
		if errors.Is(err, net.ErrClosed) {
			// if network connection has been closed, then ctx has likely been cancelled, meaning we should quit
			return nil
		} else if err != nil {
			log.Warn().AnErr("err", err).Msg("error accepting connection")
			continue
		}

		// prep proxy config
		connNum, err := feedproxyGetConnectionNumberWrapper()
		if err != nil {
			log.Warn().AnErr("err", err).Msg("could not get connection number")
			continue
		}
		proxyConn := feedproxy.ProxyConnection{
			Connection:                  conn,
			ConnectionProtocol:          conf.listenProto,
			ConnectionNumber:            connNum,
			FeedInContainerPrefix:       conf.feedInContainerPrefix,
			Logger:                      log,
			FeederValidityCheckInterval: time.Second * 60,
		}

		// initiate proxying of the connection
		wg.Add(1)
		go func() {
			wg.Done()
			proxyConnStartWrapper(&proxyConn, ctx)
		}()

	}
	wg.Wait()
	return nil
}
