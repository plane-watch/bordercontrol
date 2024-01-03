package stats

import (
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

var natsStatsFeeder chan *nats.Msg

func initNats(natsUrl string) {

	// make chans & start chan handlers
	natsStatsFeeder = make(chan *nats.Msg)
	go natsStatsFeederHandler(natsStatsFeeder)

	// connect to NATS
	nc, err := nats.Connect(natsUrl)
	if err != nil {
		log.Err(err).Msg("could not connect")
	}
	defer nc.Close()

	// subscribe to pw_bordercontrol.stats.feeder
	_, err = nc.ChanSubscribe("pw_bordercontrol.stats.feeder", natsStatsFeeder)
	if err != nil {
		log.Err(err).Msg("could not connect")
	}

	for {
	}
}

func natsStatsFeederHandler(c chan *nats.Msg) {
	for {
		msg := <-c
		fmt.Println("msg.Subject", msg.Subject)
		fmt.Println("msg.Header", msg.Header)
		fmt.Println("msg.Data", msg.Data)
		msg.Ack()
	}
}
