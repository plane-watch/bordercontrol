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

	// Connection details
	Connections map[uint]Connection // key = connection number

	TimeUpdated time.Time // time these stats were updated
}

type Connection struct {
	Proto         string    // protocol "BEAST" or "MLAT"
	Src           net.Addr  // source ip:port of incoming connection
	Dst           net.Addr  // destination ip:port of outgoing connection
	TimeConnected time.Time // time connection was established
	BytesIn       uint64    // bytes received from feeder client
	BytesOut      uint64    // bytes sent to feeder client
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

func (stats *Statistics) incrementByteCounters(uuid uuid.UUID, connNum uint, bytesIn, bytesOut uint64) {
	// increment byte counters of a feeder
	//   - sets time_last_updated to now

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	defer stats.mu.Unlock()

	// update counters
	y := stats.Feeders[uuid]
	c := y.Connections[connNum]
	c.BytesIn += bytesIn
	c.BytesOut += bytesOut
	y.Connections[connNum] = c

	// update time_last_updated
	y.TimeUpdated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y

}

func (stats *Statistics) initFeederStats(uuid uuid.UUID) {
	// does stats var have an entry for uuid?
	// if not, create it

	stats.mu.Lock()
	defer stats.mu.Unlock()

	_, ok := stats.Feeders[uuid]
	if !ok {
		stats.Feeders[uuid] = FeederStats{
			Connections: make(map[uint]Connection),
		}
	}
}

func (stats *Statistics) setFeederDetails(uuid uuid.UUID, label string, lat, lon float64) {
	// updates the details of a feeder

	stats.initFeederStats(uuid)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// copy stats entry
	y := stats.Feeders[uuid]

	// update time_last_updated
	y.Label = label
	y.Lat = lat
	y.Lon = lon
	y.TimeUpdated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y
}

func (stats *Statistics) delConnection(uuid uuid.UUID, connNum uint) {
	// updates the connected status of a feeder

	stats.initFeederStats(uuid)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// copy stats entry
	y := stats.Feeders[uuid]

	// delete connection
	delete(y.Connections, connNum)

	y.TimeUpdated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y
}

func (stats *Statistics) addConnection(uuid uuid.UUID, src net.Addr, dst net.Addr, proto string, connNum uint) {
	// updates the connected status of a feeder

	stats.initFeederStats(uuid)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// copy stats entry
	y := stats.Feeders[uuid]

	// add connection
	c := Connection{
		Proto:         strings.ToUpper(proto),
		Src:           src,
		Dst:           dst,
		TimeConnected: time.Now(),
		BytesIn:       0,
		BytesOut:      0,
	}
	y.Connections[connNum] = c
	y.TimeUpdated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y
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
			if len(stats.Feeders[u].Connections) == 0 {
				if time.Now().Sub(stats.Feeders[u].TimeUpdated) > (time.Second * 60) {
					toEvict = append(toEvict, u)
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
		w.WriteHeader(http.StatusInternalServerError)
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
	// /api/v1/feeder/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	if matchUrlSingleFeeder.Match([]byte(strings.ToLower(r.URL.Path))) {

		// try to extract uuid from path
		clientApiKey, err := uuid.Parse((string(matchUUID.Find([]byte(strings.ToLower(r.URL.Path))))))
		if err != nil {
			log.Err(err).Str("url", r.URL.Path).Msg("could not get uuid from url")
			w.WriteHeader(http.StatusBadRequest)
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
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// write response
		if resp.Error != "" {
			w.WriteHeader(http.StatusBadRequest)
		}
		w.Header().Add("Content-Type", "application/json")
		w.Write(output)
		return

	} else {
		log.Error().Str("url", r.URL.Path).Msg("path did not match single feeder")
		w.WriteHeader(http.StatusBadRequest)
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
