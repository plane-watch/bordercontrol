package nats_io

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

const (
	natsSubjPing = "pw_bordercontrol.ping"
)

var (
	nc         *nats.Conn
	natsConfig NatsConfig

	initialised   bool
	initialisedMu sync.RWMutex

	// custom errors
	ErrStatsNotInitialised = errors.New("stats not initialised")
)

type NatsConfig struct {

	// NATS config
	Url      string // nats url
	Instance string // unique instance name for this instance of bordercontrol

	// App info
	Version   string    // app version
	StartTime time.Time // time when app was started (for working out uptime)
}

func isInitialised() bool {
	// returns true if Init() has been called, else false
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}

func (conf *NatsConfig) Init() {

	natsConfig = *conf

	var err error

	// prep nats instance name
	if natsConfig.Instance == "" {
		natsConfig.Instance, err = os.Hostname()
		if err != nil {
			log.Fatal().Err(err).Msg("could not determine hostname")
		}
	}

	// nats connection
	if conf.Url != "" {
		nc, err = nats.Connect(conf.Url)
		if err != nil {
			log.Fatal().Err(err).Msg("error connecting to NATS")
		}
	}

	// ping
	err = Sub(natsSubjPing, PingHandler)
	if err != nil {
		log.Err(err).Str("subj", natsSubjPing).Msg("error subscribing")
	}

	// set initialised
	initialisedMu.Lock()
	initialised = true
	defer initialisedMu.Unlock()

	log.Info().
		Str("instance", natsConfig.Instance).
		Str("url", nc.ConnectedAddr()).
		Msg("connected to nats server")
}

func GetInstance() (instance string, err error) {
	if !isInitialised() {
		err = ErrStatsNotInitialised
	}
	return natsConfig.Instance, err
}

func IsConnected() bool {
	if !isInitialised() {
		return false
	}
	return nc.IsConnected()
}

func Sub(subj string, handler func(msg *nats.Msg)) error {
	if !isInitialised() {
		return ErrStatsNotInitialised
	}

	_, err := nc.Subscribe(subj, handler)
	if err != nil {
		log.Err(err).Str("subj", subj).Msg("could not subscribe")
	} else {
		log.Debug().Str("subj", subj).Msg("subscribed")
	}

	return nil
}

func SignalSendOnSubj(subj string, sig os.Signal, ch chan os.Signal) error {
	// when subj is received, signal sig is sent to channel ch
	log := log.With().Str("subj", subj).Logger()
	return Sub(subj, func(msg *nats.Msg) {
		if string(msg.Data) == "*" || string(msg.Data) == natsConfig.Instance {
			ch <- sig
			msg.Ack()
		} else {
			log.Debug().Msg("ignoring, not for this instance")
		}
	})
}

func PingHandler(msg *nats.Msg) {
	log := log.With().Str("subj", msg.Subject).Logger()

	inst, err := GetInstance()
	if err != nil {
		log.Err(err).Msg("could not get NATS instance")
	}
	log = log.With().Str("instance", inst).Logger()

	reply := nats.NewMsg()
	reply.Header.Add("instance", inst)
	reply.Header.Add("version", natsConfig.Version)
	reply.Header.Add("uptime", time.Since(natsConfig.StartTime).String())

	err = msg.RespondMsg(reply)
	if err != nil {
		log.Err(err).Msg("could not reply to nats req")
	}
}
