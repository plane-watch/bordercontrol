package stats

import (
	"pw_bordercontrol/lib/feedprotocol"
	"strings"

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

func initNats(natsUrl string) {

	log := log.With().
		Str("func", "initNats").
		Str("natsurl", natsUrl).
		Logger()

	// make chans & start chan handlers
	natsSubjFeederConnected = make(chan *nats.Msg)
	go natsSubjFeederConnectedHandler(natsSubjFeederConnected)

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

func natsSubjFeederConnectedHandler(c chan *nats.Msg) {
	for {

		// receive a message
		msg := <-c

		// handle message
		func(msg *nats.Msg) {

			err := msg.InProgress() // workin' on it!
			if err != nil {
				log.Err(err).Msg("could not send msg.InProgress")
				return
			}

			defer msg.Ack() // ack message when done

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

			err = msg.InProgress() // tell server we're working on it
			if err != nil {
				log.Err(err).Msg("could not respond to nats msg")
			}

			// report status
			if conn.Status {
				err := msg.Respond([]byte("true"))
				if err != nil {
					log.Err(err).Msg("could not respond to nats msg")
				}
			} else {
				err := msg.Respond([]byte("false"))
				if err != nil {
					log.Err(err).Msg("could not respond to nats msg")
				}
			}
			return

		}(msg)
	}
}
