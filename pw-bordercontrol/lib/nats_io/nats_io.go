package nats_io

import (
	"context"
	"errors"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

const (
	natsSubjPing = "pw_bordercontrol.ping"
)

var (
	// module wide variable for nats connection
	nc *nats.Conn

	// module wide variable (+mutex) to track whether nats subsystem has been initialised
	initialised   bool
	initialisedMu sync.RWMutex

	// module wide variable for nats subsystem context
	ctx       context.Context
	ctxCancel context.CancelFunc

	// module wide variable to hold nats config
	natsConfig *NatsConfig

	// custom errors
	ErrNotInitialised = errors.New("nats not initialised")
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

func (conf *NatsConfig) Close() error {

	// return error is not initialised
	if !isInitialised() {
		return ErrNotInitialised
	}

	// close nats connection
	nc.Close()

	// cancel the context
	ctxCancel()

	// clear config
	natsConfig = &NatsConfig{}

	// set initialised to false
	initialisedMu.Lock()
	initialised = false
	initialisedMu.Unlock()

	return nil
}

func (conf *NatsConfig) Init() error {

	// set up context
	ctx = context.Background()
	ctx, ctxCancel = context.WithCancel(ctx)

	var err error

	natsConfig = conf

	// prep nats instance name
	if natsConfig.Instance == "" {
		natsConfig.Instance, err = os.Hostname()
		if err != nil {
			log.Err(err).Msg("could not determine hostname")
			return err
		}
	}

	// nats connection
	if conf.Url != "" {
		nc, err = nats.Connect(conf.Url)
		if err != nil {
			log.Err(err).Msg("error connecting to NATS")
			return err
		}
	} else {
		return nil
	}

	// set initialised
	func() {
		initialisedMu.Lock()
		initialised = true
		defer initialisedMu.Unlock()
	}()

	log.Info().
		Str("instance", natsConfig.Instance).
		Str("url", nc.ConnectedAddr()).
		Msg("connected to nats server")

	// subscriptions
	err = Sub(natsSubjPing, PingHandler)
	if err != nil {
		log.Err(err).Str("subj", natsSubjPing).Msg("error subscribing")
		return err
	}

	return nil
}

func GetInstance() (instance string, err error) {
	// returns this bordercontrol's instance name
	if !isInitialised() {
		return instance, ErrNotInitialised
	}
	return natsConfig.Instance, err
}

func ThisInstance(sentToInstance string) (meantForThisInstance bool, thisInstanceName string, err error) {
	// returns meantForThisInstance = true if sentToInstance matches this instance
	// returns thisInstanceName = the name of this instance
	// sentToInstance supports regex

	if !isInitialised() {
		return meantForThisInstance, thisInstanceName, ErrNotInitialised
	}

	thisInstanceName = natsConfig.Instance
	if sentToInstance == "*" {
		return true, thisInstanceName, err
	} else if sentToInstance == thisInstanceName {
		return true, thisInstanceName, err
	} else {
		meantForThisInstance, err = regexp.MatchString(sentToInstance, natsConfig.Instance)
		return meantForThisInstance, thisInstanceName, err
	}
}

func IsConnected() bool {
	// returns true if connected to nats server
	if !isInitialised() {
		return false
	}
	return nc.IsConnected()
}

func Sub(subj string, handler func(msg *nats.Msg)) error {
	// subscribes to a subject "subj", and calls function "handler" with msg as argument

	// error if not initialised
	if !isInitialised() {
		return ErrNotInitialised
	}

	// update log context
	log := log.With().
		Str("subj", subj).
		Str("instance", natsConfig.Instance).
		Str("url", natsConfig.Url).
		Logger()

	// subscribe
	_, err := nc.Subscribe(subj, handler)
	if err != nil {
		log.Err(err).Msg("could not subscribe")
	} else {
		log.Debug().Msg("subscribed")
	}

	return nil
}

func SignalSendOnSubj(subj string, sig os.Signal, ch chan os.Signal) error {
	// when "subj" is received, signal "sig" is sent to channel "ch"
	log := log.With().Str("subj", subj).Logger()
	return Sub(subj, func(msg *nats.Msg) {
		meantForThisInstance, _, err := ThisInstance(string(msg.Data))
		if err != nil {
			log.Err(err).Msg("error subscribing")
			return
		}
		if meantForThisInstance {
			ch <- sig
			msg.Ack()
		} else {
			log.Debug().Msg("ignoring, not for this instance")
		}
	})
}

func PingHandler(msg *nats.Msg) {
	// handles ping requests
	log := log.With().Str("subj", msg.Subject).Logger()

	// get instance
	inst, err := GetInstance()
	if err != nil {
		log.Err(err).Msg("could not get NATS instance")
		return
	}
	log = log.With().Str("instance", inst).Logger()

	// prep reply
	reply := nats.NewMsg(msg.Subject)
	reply.Header.Add("instance", inst)
	reply.Header.Add("version", natsConfig.Version)
	reply.Header.Add("uptime", time.Since(natsConfig.StartTime).String())
	reply.Data = []byte("pong")

	// send reply
	err = msg.RespondMsg(reply)
	if err != nil {
		log.Err(err).Msg("could not reply to nats req")
		return
	}
}
