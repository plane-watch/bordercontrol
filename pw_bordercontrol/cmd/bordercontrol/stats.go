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

type feederStatusUpdate struct {
	uuid   uuid.UUID
	update feederStats
}

// struct for list of feeder stats (+ mutex for sync)
type statistics struct {
	mu      sync.RWMutex
	feeders map[uuid.UUID]feederStats
}

var (
	stats statistics // feeder statistics
)

func (stats *statistics) setConnectedBEAST(uuid uuid.UUID, src_beast net.Addr) {
	// updates the connected status of a feeder
	//   - sets src_beast
	//   - sets connected_time to now
	//   - sets time_last_updated to now

	// does stats var have an entry for uuid?
	stats.mu.RLock()
	_, ok := stats.feeders[uuid]
	stats.mu.RUnlock()

	// if not, create it
	if !ok {
		stats.mu.Lock()
		stats.feeders[uuid] = feederStats{}
		stats.mu.Unlock()
	}

	// copy stats entry
	stats.mu.Lock()
	y := stats.feeders[uuid]

	// update stats entry
	y.time_connected_beast = time.Now()
	y.src_beast = src_beast
	y.time_last_updated = time.Now()

	// write stats entry
	stats.feeders[uuid] = y
	stats.mu.Unlock()
}

func statsManager() {

	// init stats variable
	stats.feeders = make(map[uuid.UUID]feederStats)

	for {
		time.Sleep(10 * time.Second)

		stats.mu.RLock()
		fmt.Println(&stats)
		stats.mu.RUnlock()
	}
}
