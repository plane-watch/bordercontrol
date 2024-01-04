package nats_io

import (
	"errors"
	"os"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

var (
	natsInstance string
	nc           *nats.Conn

	initialised   bool
	initialisedMu sync.RWMutex

	// custom errors
	ErrStatsNotInitialised = errors.New("stats not initialised")
)

func isInitialised() bool {
	// returns true if Init() has been called, else false
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}

type NatsConfig struct {
	Url      string // nats url
	Instance string // nats instance
}

func (conf *NatsConfig) Init() {

	var err error

	// prep nats instance name
	if conf.Instance == "" {
		natsInstance, err = os.Hostname()
		if err != nil {
			log.Fatal().Err(err).Msg("could not determine hostname")
		}
	} else {
		natsInstance = conf.Instance
	}

	// nats connection
	if conf.Url != "" {
		nc, err = nats.Connect(conf.Url)
		if err != nil {
			log.Fatal().Err(err).Msg("error connecting to NATS")
		}
	}

	initialisedMu.Lock()
	initialised = true
	defer initialisedMu.Unlock()
}

func GetInstance() (instance string, err error) {
	if !isInitialised() {
		err = ErrStatsNotInitialised
	}
	return natsInstance, err
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
	return Sub(subj, func(msg *nats.Msg) {
		if msg.Data == nil {
			ch <- sig
			msg.Ack()
		}
	})
}
