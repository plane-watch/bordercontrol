package stats

import (
	"encoding/json"
	"pw_bordercontrol/lib/feedprotocol"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

const (
	natsSubjFeederConnectedAllProtocols = "pw_bordercontrol.feeder.connected.*"
	natsSubjFeederConnectedBEAST        = "pw_bordercontrol.feeder.connected.beast"
	natsSubjFeederConnectedMLAT         = "pw_bordercontrol.feeder.connected.mlat"

	natsSubjFeederMetricsAllProtocols = "pw_bordercontrol.feeder.metrics.*"
	natsSubjFeederMetricsBEAST        = "pw_bordercontrol.feeder.metrics.beast"
	natsSubjFeederMetricsMLAT         = "pw_bordercontrol.feeder.metrics.mlat"
)

var NatsInstance string

type feederMetrics struct {
	BytesIn        uint64    `json:"bytes_in"`
	BytesOut       uint64    `json:"bytes_out"`
	ConnectionTime time.Time `json:"connection_time"`
	send           bool
}

func initNats(natsUrl, natsInstance string) {

	// update log context
	log := log.With().
		Str("func", "initNats").
		Str("natsurl", natsUrl).
		Str("natsinstance", natsInstance).
		Logger()

	NatsInstance = natsInstance

	// connect to NATS
	log.Debug().Msg("connecting to NATS")
	nc, err := nats.Connect(natsUrl)
	if err != nil {
		log.Err(err).Msg("could not connect")
	}
	defer nc.Close()

	// subscriptions

	natsSubjFeederConnectedAllProtocolsSub, err := nc.Subscribe(natsSubjFeederConnectedAllProtocols, natsSubjFeederHandler)
	if err != nil {
		log.Err(err).Str("subj", natsSubjFeederConnectedAllProtocols).Msg("could not subscribe")
	} else {
		defer natsSubjFeederConnectedAllProtocolsSub.Unsubscribe()
		log.Debug().Str("subj", natsSubjFeederConnectedAllProtocols).Msg("subscribed")
	}

	natsSubjFeederMetricsAllProtocolsSub, err := nc.Subscribe(natsSubjFeederMetricsAllProtocols, natsSubjFeederHandler)
	if err != nil {
		log.Err(err).Str("subj", natsSubjFeederMetricsAllProtocols).Msg("could not subscribe")
	} else {
		defer natsSubjFeederMetricsAllProtocolsSub.Unsubscribe()
		log.Debug().Str("subj", natsSubjFeederMetricsAllProtocols).Msg("subscribed")
	}

	// ---

	for {
	}
}

func getProtocolFromLastToken(subject string) (feedprotocol.Protocol, error) {
	// returns the feeder protocol, where the protocol is the last token in the subject
	tokens := strings.Split(subject, ".")
	lastToken := strings.ToUpper(tokens[len(tokens)-1])
	switch {
	case lastToken == strings.ToUpper(feedprotocol.ProtocolNameBEAST):
		return feedprotocol.BEAST, nil
	case lastToken == strings.ToUpper(feedprotocol.ProtocolNameMLAT):
		return feedprotocol.MLAT, nil
	default:
		return feedprotocol.Protocol(0), feedprotocol.ErrUnknownProtocol
	}
}

func natsSubjFeederHandler(msg *nats.Msg) {

	// verify protocol
	proto, err := getProtocolFromLastToken(msg.Subject)
	if err != nil {
		log.Err(err).Msg("could not determine protocol from subject")
		return
	}

	// parse API key
	apiKey, err := uuid.ParseBytes(msg.Data)
	if err != nil {
		log.Err(err).Msg("could not parse API Key")
		return
	}

	// send message to relevant handler
	switch msg.Subject {
	case natsSubjFeederConnectedBEAST:
		natsSubjFeederConnectedHandler(msg, apiKey, proto)
	case natsSubjFeederConnectedMLAT:
		natsSubjFeederConnectedHandler(msg, apiKey, proto)
	case natsSubjFeederMetricsBEAST:
		natsSubjFeederMetricsHandler(msg, apiKey, proto)
	case natsSubjFeederMetricsMLAT:
		natsSubjFeederMetricsHandler(msg, apiKey, proto)
	default:
		log.Error().Msg("unsupported subject")
	}

}

func natsSubjFeederMetricsHandler(msg *nats.Msg, apiKey uuid.UUID, proto feedprotocol.Protocol) {

	// update log context
	log := log.With().
		Str("subject", msg.Subject).
		Str("proto", proto.Name()).
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
	conns, ok := feeder.Connections[proto.Name()]
	if !ok {
		log.Debug().Msg("no connection")
		return
	}

	// prep reply struct
	fm := feederMetrics{}
	for _, connDetail := range conns.ConnectionDetails {
		if fm.ConnectionTime.Before(connDetail.TimeConnected) {
			fm.ConnectionTime = connDetail.TimeConnected
			fm.BytesIn = connDetail.BytesIn
			fm.BytesOut = connDetail.BytesOut
			fm.send = true
		}
	}

	if fm.send {
		// prep reply
		reply := nats.NewMsg(msg.Subject)
		reply.Header.Add("instance", NatsInstance)

		// marshall metrics struct into json
		jb, err := json.Marshal(fm)
		if err != nil {
			log.Err(err).Msg("could not marshall feeder metrics into JSON")
			return
		}
		reply.Data = jb

		// send reply
		err = msg.RespondMsg(reply)
		if err != nil {
			log.Err(err).Msg("could not respond to nats msg")
		}
	}
	return
}

func natsSubjFeederConnectedHandler(msg *nats.Msg, apiKey uuid.UUID, proto feedprotocol.Protocol) {

	// update log context
	log := log.With().
		Str("subject", msg.Subject).
		Str("proto", proto.Name()).
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
		reply.Header.Add("instance", NatsInstance)

		// send reply
		err := msg.RespondMsg(reply)
		if err != nil {
			log.Err(err).Msg("could not respond to nats msg")
		}
	}
	return
}
