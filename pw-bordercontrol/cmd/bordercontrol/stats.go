package main

import (
	_ "embed"
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

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rs/zerolog/log"
)

//go:embed stats.tmpl
var statsTemplate string

// struct for per-feeder statistics
type FeederStats struct {
	// feeder details
	Label string  // feeder label
	Lat   float64 // feeder lat
	Lon   float64 // feeder lon

	// Connection details
	// string key = protocol (BEAST/MLAT, and in future ACARS/VDLM2 etc)
	Connections map[string]ProtocolDetail

	TimeUpdated time.Time // time these stats were updated
}

type ProtocolDetail struct {
	Status               bool      // is protocol connected
	ConnectionCount      int       // number of connections for this protocol
	MostRecentConnection time.Time // time of most recent connection

	// uint key = connection number within bordercontrol
	ConnectionDetails map[uint]ConnectionDetail
}

type ConnectionDetail struct {
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
	Data interface{}
}

var (
	stats Statistics // feeder statistics

	matchUrlSingleFeeder *regexp.Regexp // regex to match api request for single feeder stats
	matchUUID            *regexp.Regexp // regex to match UUID
)

func (stats *Statistics) getNumConnections(uuid uuid.UUID, proto string) int {
	proto = strings.ToUpper(proto)
	stats.mu.RLock()
	defer stats.mu.RUnlock()
	return stats.Feeders[uuid].Connections[proto].ConnectionCount
}

func (stats *Statistics) incrementByteCounters(uuid uuid.UUID, connNum uint, bytesIn, bytesOut uint64) {
	// increment byte counters of a feeder
	//   - sets time_last_updated to now

	stats.initFeederStats(uuid)

	// copy stats entry
	stats.mu.Lock()
	defer stats.mu.Unlock()

	// update counters
	y := stats.Feeders[uuid]

	// find connection to update
	for proto, p := range y.Connections {
		for cn, c := range p.ConnectionDetails {
			if cn == connNum {

				// increment counters
				c.BytesIn += bytesIn
				c.BytesOut += bytesOut
				y.Connections[proto].ConnectionDetails[connNum] = c

				// update time last updated
				y.TimeUpdated = time.Now()

				// write stats entry
				stats.Feeders[uuid] = y

				return
			}
		}
	}
}

func (stats *Statistics) initFeederStats(uuid uuid.UUID) {
	// does stats var have an entry for uuid?
	// if not, create it

	stats.mu.Lock()
	defer stats.mu.Unlock()

	_, ok := stats.Feeders[uuid]
	if !ok {
		stats.Feeders[uuid] = FeederStats{
			Connections: make(map[string]ProtocolDetail),
		}
		stats.Feeders[uuid].Connections[protoBeast] = ProtocolDetail{
			ConnectionDetails: make(map[uint]ConnectionDetail),
		}
		stats.Feeders[uuid].Connections[protoMLAT] = ProtocolDetail{
			ConnectionDetails: make(map[uint]ConnectionDetail),
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

	// update label, lat, lon and time last updated
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

	// find connection to update
	for proto, p := range y.Connections {
		for cn, _ := range p.ConnectionDetails {
			if cn == connNum {

				// delete the connection
				delete(y.Connections[proto].ConnectionDetails, connNum)

				// update connection state
				pd := y.Connections[proto]
				pd.ConnectionCount = len(y.Connections[proto].ConnectionDetails)
				if len(y.Connections[proto].ConnectionDetails) > 0 {
					pd.Status = true
				} else {
					pd.Status = false
				}

				y.Connections[proto] = pd

				// update time last updated
				y.TimeUpdated = time.Now()

				// write stats entry
				stats.Feeders[uuid] = y

			}
		}
	}
}

func (stats *Statistics) addConnection(uuid uuid.UUID, src net.Addr, dst net.Addr, proto string, connNum uint) {
	// updates the connected status of a feeder

	stats.initFeederStats(uuid)

	// make protocol uppercase
	proto = strings.ToUpper(proto)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// copy stats entry
	y := stats.Feeders[uuid]

	// add connection
	c := ConnectionDetail{
		Src:           src,
		Dst:           dst,
		TimeConnected: time.Now(),
		BytesIn:       0,
		BytesOut:      0,
	}
	y.Connections[proto].ConnectionDetails[connNum] = c

	// update connection state
	if len(y.Connections[proto].ConnectionDetails) > 0 {
		pd := y.Connections[proto]
		pd.Status = true
		pd.MostRecentConnection = c.TimeConnected
		pd.ConnectionCount = len(y.Connections[proto].ConnectionDetails)
		y.Connections[proto] = pd
	}

	y.TimeUpdated = time.Now()

	// write stats entry
	stats.Feeders[uuid] = y
}

func httpRenderStats(w http.ResponseWriter, r *http.Request) {

	// Template helper functions
	funcMap := template.FuncMap{
		// human readable data units
		"humanReadableDataUnits": func(n uint64) string {
			prefix := ""

			if n > 1024 {
				prefix = "K"
			}
			if n > 1048576 {
				prefix = "M"
			}
			if n > 1073741824 {
				prefix = "G"
			}
			if n > 1099511627776 {
				prefix = "T"
			}
			if n > 1125899906842624 {
				prefix = "P"
			}

			switch prefix {
			case "":
			case "K":
				n = n / 1024
			case "M":
				n = n / 1024 / 1024
			case "G":
				n = n / 1024 / 1024 / 1024
			case "T":
				n = n / 1024 / 1024 / 1024 / 1024
			case "P":
				n = n / 1024 / 1024 / 1024 / 1024 / 1024
			}
			return fmt.Sprintf("%d%s", n, prefix)
		},
	}

	// Make and parse the HTML template
	t, err := template.New("stats").Funcs(funcMap).Parse(statsTemplate)
	if err != nil {
		log.Panic().AnErr("err", err).Str("api", "httpRenderStats").Str("reqURI", r.RequestURI).Msg("could not render statsTemplate")
	}

	// Render the data
	stats.mu.RLock()
	defer stats.mu.RUnlock()
	err = t.Execute(w, stats.Feeders)
	if err != nil {
		fmt.Println(err)
		log.Panic().AnErr("err", err).Str("api", "httpRenderStats").Str("reqURI", r.RequestURI).Msg("could not execute statsTemplate")
	}
}

func statsEvictor() {

	for {
		var toEvict []uuid.UUID
		var activeBeast, activeMLAT uint

		stats.mu.Lock()

		// find stale data
		for u, _ := range stats.Feeders {
			if len(stats.Feeders[u].Connections) == 0 {
				if time.Now().Sub(stats.Feeders[u].TimeUpdated) > (time.Second * 60) {
					toEvict = append(toEvict, u)
				}
			} else {
				for p, _ := range stats.Feeders[u].Connections {
					switch p {
					case protoBeast:
						activeBeast++
					case protoMLAT:
						activeMLAT++
					}
				}
			}
		}

		// dump stale data
		for _, u := range toEvict {
			delete(stats.Feeders, u)
		}

		stats.mu.Unlock()

		// log number of connections & goroutines
		log.Info().Uint("beast", activeBeast).Uint("mlat", activeMLAT).Int("goroutines", runtime.NumGoroutine()).Msg("active connections")

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
		log.Error().Any("resp", resp).Str("api", "apiReturnAllFeeders").Str("reqURI", r.RequestURI).Msg("could not marshall resp into json")
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
			log.Err(err).Str("url", r.URL.Path).Str("api", "apiReturnSingleFeeder").Str("reqURI", r.RequestURI).Msg("could not get uuid from url")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// look up feeder by uuid
		stats.mu.RLock()
		defer stats.mu.RUnlock()
		val, ok := stats.Feeders[clientApiKey]
		if !ok {
			log.Error().Any("resp", resp).Str("api", "apiReturnSingleFeeder").Str("reqURI", r.RequestURI).Msg("feeder not found")
			w.WriteHeader(http.StatusBadRequest)
			return
		} else {
			resp.Data = val
		}

		// prepare response
		output, err := json.Marshal(resp)
		if err != nil {
			log.Error().Any("resp", resp).Str("api", "apiReturnSingleFeeder").Str("reqURI", r.RequestURI).Msg("could not marshall resp into json")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Add("Content-Type", "application/json")
		w.Write(output)
		return

	} else {
		log.Error().Str("url", r.URL.Path).Str("api", "apiReturnSingleFeeder").Str("reqURI", r.RequestURI).Msg("path did not match single feeder")
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

	// prometheus endpoint
	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/metrics/", promhttp.Handler())

	// start stats http server
	log.Info().Str("ip", "0.0.0.0").Int("port", 8080).Msg("starting statistics listener")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Panic().AnErr("err", err).Msg("stats server stopped")
	}
}
