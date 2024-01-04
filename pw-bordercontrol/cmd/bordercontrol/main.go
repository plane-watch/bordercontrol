package main

import (
	"fmt"
	"net"
	"os"
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
	"pw_bordercontrol/lib/stats"
	"pw_bordercontrol/lib/stunnel"

	"github.com/nats-io/nats.go"
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
				Value:    "0.0.0.0:12345", // insert Spaceballs joke here
				EnvVars:  []string{"BC_LISTEN_BEAST"},
			},
			&cli.StringFlag{
				Category: "Network",
				Name:     "listenmlat",
				Usage:    "Address and TCP port to listen on for MLAT connections",
				Value:    "0.0.0.0:12346",
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

	// allow override of these functions to simplify testing
	stunnelNewListenerWrapper = func(network string, laddr string) (l net.Listener, err error) {
		return stunnel.NewListener(network, laddr)
	}
	proxyConnStartWrapper = func(f *feedproxy.ProxyConnection) error {
		return f.Start()
	}
	feedproxyGetConnectionNumberWrapper = func() (num uint, err error) {
		return feedproxy.GetConnectionNumber()
	}
)

func main() {

	// set action when run
	app.Action = runServer

	// set up logging
	logging.IncludeVerbosityFlags(&app)
	logging.ConfigureForCli()

	// run & final exit
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

func logNumGoroutines(freq time.Duration, stopChan chan bool) {
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
	// prep config
	ip := net.ParseIP(strings.Split(listenAddr, ":")[0])
	if ip == nil {
		log.Fatal().Str("proto", proto.Name()).Str("addr", listenAddr).Msg("invalid listen address")
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

func connectToNats(natsUrl string) (*nats.Conn, error) {
	// connect to NATS
	return nats.Connect(natsUrl)
}

func runServer(ctx *cli.Context) error {

	// Set logging level
	logging.SetLoggingLevel(ctx)

	// get commit hash and commit time from git info
	commithash, committime := getRepoInfo()

	// initial logging
	log.Info().Msg(banner) // show awesome banner
	log.Info().Str("version", ctx.App.Version).Str("commithash", commithash).Str("committime", committime).Msg("bordercontrol starting")
	log.Debug().Str("log-level", zerolog.GlobalLevel().String()).Msg("log level set")

	// if debug then show some extra goodies
	if zerolog.GlobalLevel() == zerolog.DebugLevel {
		// display number active goroutines
		logNumGoroutinesStopChan := make(chan bool)
		go logNumGoroutines(time.Minute*5, logNumGoroutinesStopChan)
	}

	// initialise ssl/tls subsystem
	stunnel.Init(syscall.SIGHUP)

	// load SSL cert/key
	err := stunnel.LoadCertAndKeyFromFile(ctx.String("cert"), ctx.String("key"))
	if err != nil {
		log.Fatal().Err(err).Msg("error loading TLS cert and/or key")
	}

	// connect to nats for control/stats/etc
	var nc *nats.Conn
	if ctx.String("natsurl") != "" {
		nc, err = connectToNats(ctx.String("natsurl"))
		if err != nil {
			log.Fatal().Err(err).Msg("error connecting to NATS")
		}
	} else {
		log.Debug().Msg("skipping NATS connection")
	}

	// prep nats instance name
	natsInstance := ctx.String("natsinstance")
	if natsInstance == "" {
		natsInstance, err = os.Hostname()
		if err != nil {
			log.Fatal().Err(err).Msg("could not determine hostname")
		}
	}

	// start statistics manager
	err = stats.Init(ctx.String("listenapi"), nc, natsInstance)
	if err != nil {
		log.Err(err).Msg("could not start statistics manager")
		return err
	}

	// initialise feedproxy
	feedProxyConf := feedproxy.FeedProxyConfig{
		UpdateFrequency: time.Minute * time.Duration(ctx.Int("atcupdatefreq")),
		ATCUrl:          ctx.String("atcurl"),
		ATCUser:         ctx.String("atcuser"),
		ATCPass:         ctx.String("atcpass"),
	}
	feedproxy.Init(&feedProxyConf)

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

	wg := sync.WaitGroup{}

	// start listening for incoming BEAST connections
	wg.Add(1)
	go func() {
		// listen forever
		for {
			err := listener(prepListenerConfig(ctx.String("listenbeast"), feedprotocol.BEAST, ctx.String("feedincontainerprefix")))
			log.Err(err).Str("proto", feedprotocol.ProtocolNameBEAST).Msg("error with listener")
			time.Sleep(time.Second) // if there's a problem, slow down restarting
		}
		wg.Done()
	}()

	// start listening for incoming MLAT connections
	wg.Add(1)
	go func() {
		// listen forever
		for {
			err := listener(prepListenerConfig(ctx.String("listenmlat"), feedprotocol.MLAT, ctx.String("feedincontainerprefix")))
			log.Err(err).Str("proto", feedprotocol.ProtocolNameMLAT).Msg("error with listener")
			time.Sleep(time.Second) // if there's a problem, slow down restarting
		}
		wg.Done()
	}()

	// serve forever
	wg.Wait()

	return nil
}

type listenConfig struct {
	listenProto           feedprotocol.Protocol // Protocol handled by the listener
	listenAddr            net.TCPAddr           // TCP address to listen on for incoming stunnel'd BEAST connections
	feedInContainerPrefix string

	stop   bool
	stopMu sync.RWMutex
}

func listener(conf *listenConfig) error {
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
	stunnelListener, err := stunnelNewListenerWrapper(
		"tcp",
		fmt.Sprintf("%s:%d", conf.listenAddr.IP.String(), conf.listenAddr.Port),
	)
	if err != nil {
		log.Err(err).Msg("error staring listener")
		return err
	}
	defer stunnelListener.Close()

	// handle incoming connections
	for {

		// quit if directed
		conf.stopMu.RLock()
		if conf.stop {
			conf.stopMu.RUnlock()
			return nil
		}
		conf.stopMu.RUnlock()

		// accept incoming connection
		conn, err := stunnelListener.Accept()
		if err != nil {
			log.Err(err).Msg("error accepting connection")
			return err
		}

		// prep proxy config
		connNum, err := feedproxyGetConnectionNumberWrapper()
		if err != nil {
			log.Err(err).Msg("could not get connection number")
			return err
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
		go proxyConnStartWrapper(&proxyConn)
	}

	return nil
}
