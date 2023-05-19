package main

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
)

// struct for per-feeder statistics
type feederStats struct {
	// source connection info
	src_beast net.Addr // source ip:port of client for BEAST connection
	src_mlat  net.Addr // source ip:port of client for MLAT connection
	// connection time
	time_connected_beast time.Time // connection time for BEAST connection
	time_connected_mlat  time.Time // connection time for MLAT connection
	time_last_updated    time.Time // time stats were last updated
	// byte counters
	bytes_rx_in_beast  int64 // bytes received from client (in) for BEAST protocol
	bytes_tx_in_beast  int64 // bytes send to client (in) for BEAST protocol
	bytes_rx_out_beast int64 // bytes received from mux (out) for BEAST protocol
	bytes_tx_out_beast int64 // bytes send to mux (out) for BEAST protocol
	// which multiplexer
	out_mux  string // connected multiplexer
	out_sink string // output sink
}

// struct for list of feeder stats (+ mutex for sync)
type statistics struct {
	mu      sync.RWMutex
	feeders map[uuid.UUID]feederStats
}

type declareConnectedBEAST struct {
	uuid      uuid.UUID
	src_beast net.Addr
}

var (
	stats               statistics                 // feeder statistics
	statsConnectedBEAST chan declareConnectedBEAST // add declareConnectedBEAST into channel on client BEAST connection
)

func statsUpdaterConnected() {
	for {
		// wait for data incoming from the channel
		x := <-statsConnectedBEAST

		// write lock on stats var
		stats.mu.Lock()

		// does stats var have an entry for uuid?
		_, ok := stats.feeders[x.uuid]
		if !ok {
			// if not, create it
			stats.feeders[x.uuid] = feederStats{}
		}

		// update stats entry
		y := stats.feeders[x.uuid]
		y.time_last_updated = time.Now()
		y.src_beast = x.src_beast
		stats.feeders[x.uuid] = y

		// unlock stats var
		stats.mu.Unlock()
	}
}

func statsManager() {

	// init stats variable
	stats.feeders = make(map[uuid.UUID]feederStats)

	// init channels
	statsConnectedBEAST = make(chan declareConnectedBEAST, 10)

	// start updater goroutines
	go statsUpdaterConnected()

	for {
		time.Sleep(10 * time.Second)
		fmt.Println(&stats)
	}
}
