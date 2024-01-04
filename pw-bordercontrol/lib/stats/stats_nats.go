package stats

import (
	"pw_bordercontrol/lib/feedprotocol"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

const (
	natsSubjFeederConnectedAllProtocols = "pw_bordercontrol.feeder.connected.*"
	natsSubjFeederConnectedBEAST        = "pw_bordercontrol.feeder.connected.beast"
	natsSubjFeederConnectedMLAT         = "pw_bordercontrol.feeder.connected.mlat"
)

var natsSubjFeederConnected chan *nats.Msg

func initNats(natsUrl, natsInstance string) {

	// update log context
	log := log.With().
		Str("func", "initNats").
		Str("natsurl", natsUrl).
		Str("natsinstance", natsInstance).
		Logger()

	wg := sync.WaitGroup{}

	// make chans & start chan handlers
	natsSubjFeederConnected = make(chan *nats.Msg)
	wg.Add(1)
	go func() {
		natsSubjFeederConnectedHandler(natsSubjFeederConnected, natsInstance)
		wg.Done()
	}()

	// connect to NATS
	log.Debug().Msg("connecting to NATS")
	nc, err := nats.Connect(natsUrl)
	if err != nil {
		log.Err(err).Msg("could not connect")
	}
	defer nc.Close()

	// subscribe to pw_bordercontrol.stats.feeder
	log.Debug().Msgf("subscribe to: %s", natsSubjFeederConnectedAllProtocols)
	_, err = nc.ChanSubscribe(natsSubjFeederConnectedAllProtocols, natsSubjFeederConnected)
	if err != nil {
		log.Err(err).Msg("could not subscribe")
	}

	for {
	}
}

func natsSubjFeederConnectedHandler(c chan *nats.Msg, natsInstance string) {
	for {

		// receive a message
		msg := <-c

		// handle message
		func(msg *nats.Msg) {

			// verify protocol
			var proto feedprotocol.Protocol
			switch msg.Subject {
			case natsSubjFeederConnectedBEAST:
				proto = feedprotocol.BEAST
			case natsSubjFeederConnectedMLAT:
				proto = feedprotocol.MLAT
			default:
				unknown := strings.Split(msg.Subject, ":")[3:]
				log.Error().Str("subject", msg.Subject).Strs("unknowns", unknown).Msg("unknown subject")
			}

			// update log context
			log := log.With().
				Str("subject", msg.Subject).
				Str("proto", proto.Name()).
				Logger()

			// parse API key
			apiKey, err := uuid.ParseBytes(msg.Data)
			if err != nil {
				log.Err(err).Msg("could not parse API Key")
				return
			}

			// update log context
			log = log.With().
				Str("apikey", apiKey.String()).
				Logger()

			// find feeder
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			feeder, ok := stats.Feeders[apiKey]
			if !ok {
				// silently ignore if the client is not connected to this instance
				log.Debug().Msg("unknown API Key")
				return
			}

			// find connection
			conn, ok := feeder.Connections[proto.Name()]
			if !ok {
				log.Debug().Msg("no connection")
				return
			}

			// report status
			if conn.Status {

				// prep reply
				reply := nats.NewMsg(msg.Subject)
				reply.Data = []byte("true")
				reply.Header.Add("host", natsInstance)

				// send reply
				err := msg.RespondMsg(reply)
				if err != nil {
					log.Err(err).Msg("could not respond to nats msg")
				}
			}
			return

		}(msg)
	}
}
