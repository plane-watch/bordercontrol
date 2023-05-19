package main

import (
	"fmt"
	"net"
	"strings"
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
	bytes_rx_in_beast  uint64 // bytes received from client (in) for BEAST protocol
	bytes_tx_in_beast  uint64 // bytes send to client (in) for BEAST protocol
	bytes_rx_out_beast uint64 // bytes received from mux (out) for BEAST protocol
	bytes_tx_out_beast uint64 // bytes send to mux (out) for BEAST protocol
	bytes_rx_in_mlat   uint64 // bytes received from client (in) for MLAT protocol
	bytes_tx_in_mlat   uint64 // bytes send to client (in) for MLAT protocol
	bytes_rx_out_mlat  uint64 // bytes received from mux (out) for MLAT protocol
	bytes_tx_out_mlat  uint64 // bytes send to mux (out) for MLAT protocol
	// output details
	dst_feedin net.Addr // connected feed-in container
	dst_mux    net.Addr // connected multiplexer
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

// variable for stats
var (
	stats statistics // feeder statistics
)

func (stats *statistics) incrementByteCounters(uuid uuid.UUID, rxin, txin, rxout, txout uint64, proto string) {
	// increment byte counters of a feeder
	//   - sets time_last_updated to now

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	y := stats.feeders[uuid]

	// update stats entry
	switch strings.ToUpper(proto) {
	case "BEAST":
		y.bytes_rx_in_beast += rxin
		y.bytes_tx_in_beast += txin
		y.bytes_rx_out_beast += rxout
		y.bytes_tx_out_beast += txout
	case "MLAT":
		y.bytes_rx_in_mlat += rxin
		y.bytes_tx_in_mlat += txin
		y.bytes_rx_out_mlat += rxout
		y.bytes_tx_out_mlat += txout
	default:
		panic("unsupported protocol")
	}

	// update time_last_updated
	y.time_last_updated = time.Now()

	// write stats entry
	stats.feeders[uuid] = y
	stats.mu.Unlock()

}

func (stats *statistics) initFeederStats(uuid uuid.UUID) {
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
}

func (stats *statistics) setOutputConnected(uuid uuid.UUID, outputType string, outputAddr net.Addr) {
	// updates the connected status of a feeder

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	y := stats.feeders[uuid]

	// update stats entry
	switch strings.ToUpper(outputType) {
	case "FEEDIN":
		y.dst_feedin = outputAddr
	case "MUX":
		y.dst_mux = outputAddr
	default:
		panic("unsupported output type")
	}

	// update time_last_updated
	y.time_last_updated = time.Now()

	// write stats entry
	stats.feeders[uuid] = y
	stats.mu.Unlock()

}

func (stats *statistics) setClientConnected(uuid uuid.UUID, src_addr net.Addr, proto string) {
	// updates the connected status of a feeder
	//   - sets src_beast/src_mlat
	//   - sets time_connected_beast/time_connected_mlat to now
	//   - sets time_last_updated to now

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	y := stats.feeders[uuid]

	// update stats entry
	switch strings.ToUpper(proto) {
	case "BEAST":
		y.time_connected_beast = time.Now()
		y.src_beast = src_addr
	case "MLAT":
		y.time_connected_mlat = time.Now()
		y.src_mlat = src_addr
	default:
		panic("unsupported protocol")
	}

	// update time_last_updated
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
		fmt.Printf("%+v\n", &stats)
		stats.mu.RUnlock()
	}
}
