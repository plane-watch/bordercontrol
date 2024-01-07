package stats

import (
	"encoding/json"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/nats_io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

const (
	natsSubjFeederConnectedBEAST = "pw_bordercontrol.feeder.connected.beast"
	natsSubjFeederConnectedMLAT  = "pw_bordercontrol.feeder.connected.mlat"

	natsSubjFeedersMetrics = "pw_bordercontrol.feeders.metrics"

	natsSubjFeederMetricsAllProtocols = "pw_bordercontrol.feeder.metrics"
	natsSubjFeederMetricsBEAST        = "pw_bordercontrol.feeder.metrics.beast"
	natsSubjFeederMetricsMLAT         = "pw_bordercontrol.feeder.metrics.mlat"
)

var (
	natsInstance string

	// functions to override for testing
	natsGetInstance = func() (instance string, err error) {
		return nats_io.GetInstance()
	}
	natsSub = func(subj string, handler func(msg *nats.Msg)) error {
		return nats_io.Sub(subj, handler)
	}
	natsRespondMsg = func(original *nats.Msg, reply *nats.Msg) error {
		return original.RespondMsg(reply)
	}
)

type perFeederPerProtocolMetrics struct {
	FeederCode     string    `json:"feeder_code"`
	Label          string    `json:"label"`
	BytesIn        uint64    `json:"bytes_in"`
	BytesOut       uint64    `json:"bytes_out"`
	ConnectionTime time.Time `json:"connection_time"`
	send           bool
}

type perFeederAllProtocolMetrics struct {
	FeederCode          string    `json:"feeder_code"`
	Label               string    `json:"label"`
	BeastConnected      bool      `json:"beast_connected"`
	BeastBytesIn        uint64    `json:"beast_bytes_in"`
	BeastBytesOut       uint64    `json:"beast_bytes_out"`
	BeastConnectionTime time.Time `json:"beast_connection_time"`
	MlatConnected       bool      `json:"mlat_connected"`
	MlatBytesIn         uint64    `json:"mlat_bytes_in"`
	MlatBytesOut        uint64    `json:"mlat_bytes_out"`
	MlatConnectionTime  time.Time `json:"mlat_connection_time"`
	send                bool
}

func initNats() error {

	var err error
	natsInstance, err = natsGetInstance()
	if err != nil {
		log.Err(err).Msg("cannot get instance")
		return err
	}

	// subscriptions
	err = natsSub(natsSubjFeedersMetrics, natsSubjFeedersMetricsHandler)
	if err != nil {
		return err
	}
	err = natsSub(natsSubjFeederMetricsAllProtocols, natsSubjFeederMetricsAllProtocolsHandler)
	if err != nil {
		return err
	}
	err = natsSub(natsSubjFeederMetricsBEAST, natsSubjFeederHandler)
	if err != nil {
		return err
	}
	err = natsSub(natsSubjFeederMetricsMLAT, natsSubjFeederHandler)
	if err != nil {
		return err
	}
	err = natsSub(natsSubjFeederConnectedBEAST, natsSubjFeederHandler)
	if err != nil {
		return err
	}
	err = natsSub(natsSubjFeederConnectedMLAT, natsSubjFeederHandler)
	if err != nil {
		return err
	}
	return nil
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

func parseApiKeyFromMsgData(msg *nats.Msg) (uuid.UUID, error) {
	// parse API key
	return uuid.ParseBytes(msg.Data)
}

func natsSubjFeedersMetricsHandler(msg *nats.Msg) {

	// update log context
	log := log.With().
		Str("subject", msg.Subject).
		Logger()

	// find feeder
	stats.mu.RLock()
	defer stats.mu.RUnlock()

	afm := make(map[string]perFeederAllProtocolMetrics)

	for apiKey, feeder := range stats.Feeders {

		// prep reply struct
		fm := perFeederAllProtocolMetrics{}
		fm.FeederCode = feeder.Code
		fm.Label = feeder.Label

		// beast connection
		conns, ok := feeder.Connections[feedprotocol.ProtocolNameBEAST]
		if !ok {
			log.Debug().Msg("no beast connection")
		} else {

			for _, connDetail := range conns.ConnectionDetails {
				if fm.BeastConnectionTime.Before(connDetail.TimeConnected) {
					fm.BeastConnectionTime = connDetail.TimeConnected
					fm.BeastBytesIn = connDetail.BytesIn
					fm.BeastBytesOut = connDetail.BytesOut
					fm.BeastConnected = true
					fm.send = true
				}
			}
		}

		// mlat connection
		conns, ok = feeder.Connections[feedprotocol.ProtocolNameMLAT]
		if !ok {
			log.Debug().Msg("no mlat connection")
		} else {

			for _, connDetail := range conns.ConnectionDetails {
				if fm.MlatConnectionTime.Before(connDetail.TimeConnected) {
					fm.MlatConnectionTime = connDetail.TimeConnected
					fm.MlatBytesIn = connDetail.BytesIn
					fm.MlatBytesOut = connDetail.BytesOut
					fm.MlatConnected = true
					fm.send = true
				}
			}
		}

		if fm.send {
			afm[apiKey.String()] = fm
		}
	}

	// prep reply
	reply := nats.NewMsg(msg.Subject)
	reply.Header.Add("instance", natsInstance)

	// marshall metrics struct into json
	jb, err := json.Marshal(afm)
	if err != nil {
		log.Err(err).Msg("could not marshall feeder metrics into JSON")
		return
	}
	reply.Data = jb

	// send reply
	err = natsRespondMsg(msg, reply)
	if err != nil {
		log.Err(err).Msg("could not respond to nats msg")
	}

	return
}

func natsSubjFeederMetricsAllProtocolsHandler(msg *nats.Msg) {

	// verify api key
	apiKey, err := parseApiKeyFromMsgData(msg)
	if err != nil {
		log.Err(err).Msg("could not parse API Key")
		return
	}

	// update log context
	log := log.With().
		Str("subject", msg.Subject).
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

	// prep reply struct
	fm := perFeederAllProtocolMetrics{}
	fm.FeederCode = feeder.Code
	fm.Label = feeder.Label

	// beast connection
	conns, ok := feeder.Connections[feedprotocol.ProtocolNameBEAST]
	if !ok {
		log.Debug().Msg("no beast connection")
	} else {

		for _, connDetail := range conns.ConnectionDetails {
			if fm.BeastConnectionTime.Before(connDetail.TimeConnected) {
				fm.BeastConnectionTime = connDetail.TimeConnected
				fm.BeastBytesIn = connDetail.BytesIn
				fm.BeastBytesOut = connDetail.BytesOut
				fm.BeastConnected = true
				fm.send = true
			}
		}
	}

	// mlat connection
	conns, ok = feeder.Connections[feedprotocol.ProtocolNameMLAT]
	if !ok {
		log.Debug().Msg("no mlat connection")
	} else {

		for _, connDetail := range conns.ConnectionDetails {
			if fm.MlatConnectionTime.Before(connDetail.TimeConnected) {
				fm.MlatConnectionTime = connDetail.TimeConnected
				fm.MlatBytesIn = connDetail.BytesIn
				fm.MlatBytesOut = connDetail.BytesOut
				fm.MlatConnected = true
				fm.send = true
			}
		}
	}

	if fm.send {
		// prep reply
		reply := nats.NewMsg(msg.Subject)
		reply.Header.Add("instance", natsInstance)

		// marshall metrics struct into json
		jb, err := json.Marshal(fm)
		if err != nil {
			log.Err(err).Msg("could not marshall feeder metrics into JSON")
			return
		}
		reply.Data = jb

		// send reply
		err = natsRespondMsg(msg, reply)
		if err != nil {
			log.Err(err).Msg("could not respond to nats msg")
		}
	}
	return
}

func natsSubjFeederHandler(msg *nats.Msg) {

	// verify protocol
	proto, err := getProtocolFromLastToken(msg.Subject)
	if err != nil {
		log.Err(err).Msg("could not determine protocol from subject")
		return
	}

	// verify api key
	apiKey, err := parseApiKeyFromMsgData(msg)
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
	fm := perFeederPerProtocolMetrics{}
	for _, connDetail := range conns.ConnectionDetails {
		if fm.ConnectionTime.Before(connDetail.TimeConnected) {
			fm.ConnectionTime = connDetail.TimeConnected
			fm.BytesIn = connDetail.BytesIn
			fm.BytesOut = connDetail.BytesOut
			fm.FeederCode = feeder.Code
			fm.Label = feeder.Label
			fm.send = true
		}
	}

	if fm.send {
		// prep reply
		reply := nats.NewMsg(msg.Subject)
		reply.Header.Add("instance", natsInstance)

		// marshall metrics struct into json
		jb, err := json.Marshal(fm)
		if err != nil {
			log.Err(err).Msg("could not marshall feeder metrics into JSON")
			return
		}
		reply.Data = jb

		// send reply
		err = natsRespondMsg(msg, reply)
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
		reply.Header.Add("instance", natsInstance)

		// send reply
		err := natsRespondMsg(msg, reply)
		if err != nil {
			log.Err(err).Msg("could not respond to nats msg")
		}
	}
	return
}
