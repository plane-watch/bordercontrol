package main

import (
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rs/zerolog/log"
)

const (
	statsTemplate = `
	{{range $index, $element := .}}
		{{$index}}
		{{range $element}}
			{{.Label}}
		{{end}}
	{{end}}
	`
)

// struct for per-feeder statistics
type FeederStats struct {
	// feeder details
	Label string  // feeder label
	Lat   float64 // feeder lat
	Lon   float64 // feeder lon
	// source connection info
	Src_beast net.Addr // source ip:port of client for BEAST connection
	Src_mlat  net.Addr // source ip:port of client for MLAT connection
	// connection time
	Time_connected_beast time.Time // connection time for BEAST connection
	Time_connected_mlat  time.Time // connection time for MLAT connection
	Time_last_updated    time.Time // time stats were last updated
	// byte counters
	Bytes_rx_in_beast  uint64 // bytes received from client (in) for BEAST protocol
	Bytes_tx_in_beast  uint64 // bytes send to client (in) for BEAST protocol
	Bytes_rx_out_beast uint64 // bytes received from mux (out) for BEAST protocol
	Bytes_tx_out_beast uint64 // bytes send to mux (out) for BEAST protocol
	Bytes_rx_in_mlat   uint64 // bytes received from client (in) for MLAT protocol
	Bytes_tx_in_mlat   uint64 // bytes send to client (in) for MLAT protocol
	Bytes_rx_out_mlat  uint64 // bytes received from mux (out) for MLAT protocol
	Bytes_tx_out_mlat  uint64 // bytes send to mux (out) for MLAT protocol
	// output details
	Dst_feedin net.Addr // connected feed-in container
	Dst_mux    net.Addr // connected multiplexer
}

type feederStatusUpdate struct {
	uuid   uuid.UUID
	update FeederStats
}

// struct for list of feeder stats (+ mutex for sync)
type Statistics struct {
	mu      sync.RWMutex
	Feeders map[uuid.UUID]FeederStats
}

// variable for stats
var (
	stats Statistics // feeder statistics
)

func (stats *Statistics) incrementByteCounters(uuid uuid.UUID, rxin, txin, rxout, txout uint64, proto string) {
	// increment byte counters of a feeder
	//   - sets time_last_updated to now

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	y := stats.Feeders[uuid]

	// update stats entry
	switch strings.ToUpper(proto) {
	case "BEAST":
		y.Bytes_rx_in_beast += rxin
		y.Bytes_tx_in_beast += txin
		y.Bytes_rx_out_beast += rxout
		y.Bytes_tx_out_beast += txout
	case "MLAT":
		y.Bytes_rx_in_mlat += rxin
		y.Bytes_tx_in_mlat += txin
		y.Bytes_rx_out_mlat += rxout
		y.Bytes_tx_out_mlat += txout
	default:
		panic("unsupported protocol")
	}

	// update time_last_updated
	y.Time_last_updated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y
	stats.mu.Unlock()

}

func (stats *Statistics) initFeederStats(uuid uuid.UUID) {
	// does stats var have an entry for uuid?
	stats.mu.RLock()
	_, ok := stats.Feeders[uuid]
	stats.mu.RUnlock()

	// if not, create it
	if !ok {
		stats.mu.Lock()
		stats.Feeders[uuid] = FeederStats{}
		stats.mu.Unlock()
	}
}

func (stats *Statistics) setOutputConnected(uuid uuid.UUID, outputType string, outputAddr net.Addr) {
	// updates the connected status of a feeder

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	y := stats.Feeders[uuid]

	// update stats entry
	switch strings.ToUpper(outputType) {
	case "FEEDIN":
		y.Dst_feedin = outputAddr
	case "MUX":
		y.Dst_mux = outputAddr
	default:
		panic("unsupported output type")
	}

	// update time_last_updated
	y.Time_last_updated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y
	stats.mu.Unlock()

}

func (stats *Statistics) setFeederDetails(uuid uuid.UUID, label string, lat, lon float64) {
	// updates the details of a feeder

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	y := stats.Feeders[uuid]

	// update time_last_updated
	y.Label = label
	y.Lat = lat
	y.Lon = lon
	y.Time_last_updated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y
	stats.mu.Unlock()
}

func (stats *Statistics) setClientConnected(uuid uuid.UUID, src_addr net.Addr, proto string) {
	// updates the connected status of a feeder
	//   - sets src_beast/src_mlat
	//   - sets time_connected_beast/time_connected_mlat to now
	//   - sets time_last_updated to now

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	y := stats.Feeders[uuid]

	// update stats entry
	switch strings.ToUpper(proto) {
	case "BEAST":
		y.Time_connected_beast = time.Now()
		y.Src_beast = src_addr
	case "MLAT":
		y.Time_connected_mlat = time.Now()
		y.Src_mlat = src_addr
	default:
		panic("unsupported protocol")
	}

	// update time_last_updated
	y.Time_last_updated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y
	stats.mu.Unlock()
}

func httpRenderStats(w http.ResponseWriter, r *http.Request) {

	// Make and parse the HTML template
	t, err := template.New("stats").Parse(statsTemplate)
	if err != nil {
		log.Panic().AnErr("err", err).Msg("could not render statsTemplate")
	}

	// Render the data
	err = t.Execute(w, stats.Feeders)
	if err != nil {
		fmt.Println(err)
		log.Panic().AnErr("err", err).Msg("could not execute statsTemplate")
	}

}

func statsManager() {

	// init stats variable
	stats.Feeders = make(map[uuid.UUID]FeederStats)

	http.HandleFunc("/", httpRenderStats)

	log.Info().Str("ip", "0.0.0.0").Int("port", 8080).Msg("statistics server started")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Panic().AnErr("err", err).Msg("stats server stopped")
	}
}
