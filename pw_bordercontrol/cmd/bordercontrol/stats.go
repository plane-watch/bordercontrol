package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rs/zerolog/log"
)

const (
	statsTemplate = `
<html>
<head>
<style>
table, th, td {
  border: 1px solid black;
  border-collapse: collapse;
}
</style>
<meta http-equiv="Refresh" content="5"> 
</head>
<body>
<table style="width:100%">
	<tr>
		<th>Feeder</th>
		<th>Proto</th>
		<th colspan="2">Src</th>
		<th colspan="2">Dst</th>
		<th>Since</th>
	</tr>
{{range $index, $element := .}}
	<tr>
		<td rowspan="2">
			{{.Label}}</br>UUID: {{$index}}</br>{{.Lat}} {{.Lon}}
		</td>
		<td>BEAST</td>
	{{if .Connected_beast}}
		<td>{{.Src_beast}}</td>
		<td>Rx: {{.Bytes_rx_in_beast}}B</br>Tx: {{.Bytes_tx_in_beast}}B</br></td>
		<td>{{.Dst_feedin}}</td>
		<td>Rx: {{.Bytes_rx_out_beast}}B</br>Tx: {{.Bytes_tx_out_beast}}B</br></td>
		<td>{{.Time_connected_beast}}</td>
	{{else}}
		<td colspan="5">No connection</td>
	{{end}}
	</tr>
	<tr>
		<td>MLAT</td>
	{{if .Connected_mlat}}
		<td>{{.Src_mlat}}</td>
		<td>Rx: {{.Bytes_rx_in_mlat}}B</br>Tx: {{.Bytes_tx_in_mlat}}B</br></td>
		<td>{{.Dst_mux}}</td>
		<td>Rx: {{.Bytes_rx_out_mlat}}B</br>Tx: {{.Bytes_tx_out_mlat}}B</br></td>
		<td>{{.Time_connected_mlat}}</td>
	{{else}}
		<td colspan="5">No connection</td>
	{{end}}
	</tr>
{{end}}
`
)

// struct for per-feeder statistics
type FeederStats struct {
	// feeder details
	Label string  // feeder label
	Lat   float64 // feeder lat
	Lon   float64 // feeder lon
	// connection bools
	Connected_beast bool
	Connected_mlat  bool
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

// struct for http api responses
type APIResponse struct {
	Data  interface{}
	Error string
}

// variable for stats
var (
	stats Statistics // feeder statistics

	matchUrlSingleFeeder *regexp.Regexp // regex to match api request for single feeder stats
	matchUUID            *regexp.Regexp // regex to match UUID
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

func (stats *Statistics) setClientDisconnected(uuid uuid.UUID, proto string) {
	// updates the connected status of a feeder

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	y := stats.Feeders[uuid]

	// update stats entry
	switch strings.ToUpper(proto) {
	case "BEAST":
		y.Connected_beast = false
	case "MLAT":
		y.Connected_mlat = false
	default:
		panic("unsupported protocol")
	}

	// update time_last_updated
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
		y.Connected_beast = true
		y.Src_beast = src_addr
	case "MLAT":
		y.Time_connected_mlat = time.Now()
		y.Connected_mlat = true
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
	stats.mu.RLock()
	err = t.Execute(w, stats.Feeders)
	stats.mu.RUnlock()
	if err != nil {
		fmt.Println(err)
		log.Panic().AnErr("err", err).Msg("could not execute statsTemplate")
	}
}

func statsEvictor() {

	for {
		var toEvict []uuid.UUID

		stats.mu.Lock()

		// find stale data
		for u, _ := range stats.Feeders {
			if !stats.Feeders[u].Connected_beast {
				if !stats.Feeders[u].Connected_mlat {
					if time.Now().Sub(stats.Feeders[u].Time_last_updated) > (time.Second * 60) {
						// log.Debug().Str("uuid", u.String()).Msg("evicting stale stats data")
						toEvict = append(toEvict, u)
					}
				}
			}
		}

		// dump stale data
		for _, u := range toEvict {
			delete(stats.Feeders, u)
		}

		stats.mu.Unlock()

		// periodically log number of goroutines
		// todo: move this to the web ui
		log.Debug().Int("goroutines", runtime.NumGoroutine()).Msg("number of goroutines")

		time.Sleep(time.Minute * 1)
	}
}

func apiReturnAllFeeders(w http.ResponseWriter, r *http.Request) {

	// prepare response variable
	var resp APIResponse

	// get data
	stats.mu.RLock()
	resp.Data = stats.Feeders
	stats.mu.RUnlock()

	// prepare response
	output, err := json.Marshal(resp)
	if err != nil {
		log.Error().Any("resp", resp).Msg("could not marshall resp into json")
		w.WriteHeader(500)
		return
	}

	// write response
	w.Header().Add("Content-Type", "application/json")
	w.Write(output)
	return

}

func apiReturnSingleFeeder(w http.ResponseWriter, r *http.Request) {

	// prepare response variable
	var resp APIResponse

	// try to match the path for the api query for single feeder by uuid, eg:
	// /api/v1/feeder/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxxxxxx
	if matchUrlSingleFeeder.Match([]byte(strings.ToLower(r.URL.Path))) {

		// try to extract uuid from path
		clientApiKey, err := uuid.FromBytes(matchUUID.Find([]byte(strings.ToLower(r.URL.Path))))
		if err != nil {
			log.Error().Str("url", r.URL.Path).Msg("could not get uuid from url")
			w.WriteHeader(400)
			return
		}

		// look up feeder by uuid
		stats.mu.RLock()
		val, ok := stats.Feeders[clientApiKey]
		if !ok {
			resp.Error = "feeder not found"
		} else {
			resp.Data = val
		}
		stats.mu.RUnlock()

		// prepare response
		output, err := json.Marshal(resp)
		if err != nil {
			log.Error().Any("resp", resp).Msg("could not marshall resp into json")
			w.WriteHeader(500)
			return
		}

		// write response
		w.Header().Add("Content-Type", "application/json")
		w.Write(output)
		return

	} else {
		log.Error().Str("url", r.URL.Path).Msg("path did not match single feeder")
		w.WriteHeader(400)
		return
	}

}

func statsManager() {

	// init stats variable
	stats.Feeders = make(map[uuid.UUID]FeederStats)

	// init regexps
	matchUrlSingleFeeder = regexp.MustCompile(`^/api/v1/feeder/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/?$`)
	matchUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

	// start up stats evictor
	go statsEvictor()

	// stats http server routes
	http.HandleFunc("/", httpRenderStats)
	http.HandleFunc("/api/v1/feeder/", apiReturnSingleFeeder)
	http.HandleFunc("/api/v1/feeders/", apiReturnAllFeeders)

	// start stats http server
	log.Info().Str("ip", "0.0.0.0").Int("port", 8080).Msg("starting statistics listener")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Panic().AnErr("err", err).Msg("stats server stopped")
	}
}
