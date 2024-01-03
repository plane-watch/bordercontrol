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
	// allow override of these functions to simplify testing
	stunnelNewListenerWrapper = func(network string, laddr string) (l net.Listener, err error) {
		return stunnel.NewListener(network, laddr)
	}
	proxyConnStartWrapper = func(f *feedproxy.ProxyConnection) error {
		return f.Start()
	}
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

	// runs before runServer() is called
	app.Before = func(ctx *cli.Context) error {
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

func runServer(ctx *cli.Context) error {

	// Set logging level
	logging.SetLoggingLevel(ctx)

	// get commit hash and commit time from git info
	commithash, committime := getRepoInfo()

	// initial logging
	log.Info().Msg(banner) // show awesome banner
	log.Info().Str("commithash", commithash).Str("committime", committime).Msg("bordercontrol starting")
	log.Debug().Str("log-level", zerolog.GlobalLevel().String()).Msg("log level set")

	// initialise ssl/tls subsystem
	stunnel.Init(syscall.SIGHUP)
	// load SSL cert/key
	err := stunnel.LoadCertAndKeyFromFile(ctx.String("cert"), ctx.String("key"))
	if err != nil {
		log.Fatal().Err(err).Msg("error loading TLS cert and/or key")
	}

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
	err = stats.Init(":8080")
	if err != nil {
		log.Err(err).Msg("could not start statistics manager")
		return err
	}

	// initialise feedproxy
	feedProxyConf := feedproxy.FeedProxyConfig{
		UpdateFreqency: time.Second * 60,
		ATCUrl:         ctx.String("atcurl"),
		ATCUser:        ctx.String("atcuser"),
		ATCPass:        ctx.String("atcpass"),
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
		}

		// listen forever
		for {
			err := listener(&conf)
			log.Err(err).Str("proto", feedprotocol.ProtocolNameBEAST).Msg("error with listener")
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
		}

		// listen forever
		for {
			err := listener(&conf)
			log.Err(err).Str("proto", feedprotocol.ProtocolNameBEAST).Msg("error with listener")
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
		connNum, err := feedproxy.GetConnectionNumber()
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
