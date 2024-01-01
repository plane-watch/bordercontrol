package stats

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"pw_bordercontrol/lib/feedprotocol"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rs/zerolog/log"
)

//go:embed stats.tmpl
var statsTemplate string

// struct for per-feeder statistics
type FeederStats struct {
	// feeder details
	Label string // feeder label
	Code  string // feeder_code

	// Connection details
	// string key = protocol (BEAST/MLAT, and in future ACARS/VDLM2 etc)
	Connections map[feedprotocol.Protocol]ProtocolDetail

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
	Src                net.Addr           // source ip:port of incoming connection
	Dst                net.Addr           // destination ip:port of outgoing connection
	TimeConnected      time.Time          // time connection was established
	BytesIn            uint64             // bytes received from feeder client
	BytesOut           uint64             // bytes sent to feeder client
	promMetricBytesIn  prometheus.Counter // holds the prometheus counter for bytes in
	promMetricBytesOut prometheus.Counter // holds the prometheus counter for bytes out
}

// struct for list of feeder stats (+ mutex for sync)
type Statistics struct {
	mu      sync.RWMutex
	Feeders map[uuid.UUID]FeederStats

	BytesInBEAST  uint64
	BytesOutBEAST uint64

	BytesInMLAT  uint64
	BytesOutMLAT uint64
}

// struct for http api responses
type APIResponse struct {
	Data interface{}
}

var (
	stats Statistics // feeder statistics

	matchUrlSingleFeeder *regexp.Regexp // regex to match api request for single feeder stats
	matchUUID            *regexp.Regexp // regex to match UUID

	initialised   bool
	initialisedMu sync.RWMutex
)

func statsInitialised() bool {
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}

func GetNumConnections(uuid uuid.UUID, proto feedprotocol.Protocol) int {
	// returns the number of connections for a given uuid and protocol
	stats.mu.RLock()
	defer stats.mu.RUnlock()
	return stats.Feeders[uuid].Connections[proto].ConnectionCount
}

func IncrementByteCounters(uuid uuid.UUID, connNum uint, bytesIn, bytesOut uint64) {
	// increment byte counters of a feeder
	//   - sets time_last_updated to now

	if !statsInitialised() {
		log.Err(errors.New("stats not initialised")).Msg("stats not initialised")
	}

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
				c.promMetricBytesIn.Add(float64(bytesIn))
				c.BytesOut += bytesOut
				c.promMetricBytesOut.Add(float64(bytesOut))

				y.Connections[proto].ConnectionDetails[connNum] = c

				switch proto {
				case feedprotocol.BEAST:
					stats.BytesInBEAST += bytesIn
					stats.BytesOutBEAST += bytesOut
				case feedprotocol.MLAT:
					stats.BytesInMLAT += bytesIn
					stats.BytesOutMLAT += bytesOut
				}

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

	// log := log.With().
	// 	Strs("func", []string{"stats.go", "initFeederStats"}).
	// 	Str("uuid", uuid.String()).
	// 	Logger()

	stats.mu.Lock()
	defer stats.mu.Unlock()

	_, ok := stats.Feeders[uuid]
	if !ok {
		stats.Feeders[uuid] = FeederStats{
			Connections: make(map[feedprotocol.Protocol]ProtocolDetail),
		}
		stats.Feeders[uuid].Connections[feedprotocol.BEAST] = ProtocolDetail{
			ConnectionDetails: make(map[uint]ConnectionDetail),
		}
		stats.Feeders[uuid].Connections[feedprotocol.MLAT] = ProtocolDetail{
			ConnectionDetails: make(map[uint]ConnectionDetail),
		}
	}
}

type FeederDetails struct {
	Label      string    // feeder label
	FeederCode string    // unique feeder code
	ApiKey     uuid.UUID // feeder api key
}

func RegisterFeeder(f FeederDetails) {
	// updates the details of a feeder

	if !statsInitialised() {
		log.Err(errors.New("stats not initialised")).Msg("stats not initialised")
	}

	stats.initFeederStats(f.ApiKey)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// copy stats entry
	y := stats.Feeders[f.ApiKey]

	// update label and time last updated
	y.Label = f.Label
	y.Code = f.FeederCode
	y.TimeUpdated = time.Now()

	// write stats entry
	stats.Feeders[f.ApiKey] = y
}

type Connection struct {
	ApiKey     uuid.UUID
	SrcAddr    net.Addr
	DstAddr    net.Addr
	Proto      feedprotocol.Protocol
	FeederCode string
	ConnNum    uint
}

func (conn *Connection) UnregisterConnection() {
	// updates the connected status of a feeder

	log := log.With().
		Strs("func", []string{"stats.go", "addConnection"}).
		Str("uuid", conn.ApiKey.String()).
		Str("src", conn.SrcAddr.String()).
		Str("dst", conn.DstAddr.String()).
		Str("code", conn.FeederCode).
		Uint("connNum", conn.ConnNum).
		Logger()

	if !statsInitialised() {
		log.Err(errors.New("stats not initialised")).Msg("stats not initialised")
	}

	// get protocol name
	protoName, err := feedprotocol.GetName(conn.Proto)
	if err != nil {
		log.Err(err).Msgf("unsupported protocol: %d", conn.Proto)
		return
	}

	log = log.With().Str("proto", protoName).Logger()

	stats.initFeederStats(conn.ApiKey)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// section below commented out
	// uuid should always exist in stats.Feeders due to the stats.initFeederStats above

	_, found := stats.Feeders[conn.ApiKey].Connections[conn.Proto]
	if !found {
		log.Error().Msg("proto not found in stats.Feeders[uuid].Connections")
		return
	}

	_, found = stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails[conn.ConnNum]
	if !found {
		log.Error().Msg("connNum not found in stats.Feeders[uuid].Connections[proto].ConnectionDetails")
		return
	}

	// unregister prom metrics
	_ = prometheus.Unregister(stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails[conn.ConnNum].promMetricBytesIn)
	_ = prometheus.Unregister(stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails[conn.ConnNum].promMetricBytesOut)

	// delete the connection
	delete(stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails, conn.ConnNum)

	// update connection status & count
	c, _ := stats.Feeders[conn.ApiKey].Connections[conn.Proto]
	c.ConnectionCount = len(stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails)
	if len(stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails) > 0 {
		c.Status = true
	} else {
		c.Status = false
	}
	stats.Feeders[conn.ApiKey].Connections[conn.Proto] = c

	// update time last updated
	feeder, _ := stats.Feeders[conn.ApiKey]
	feeder.TimeUpdated = time.Now()
	stats.Feeders[conn.ApiKey] = feeder

	// do we completely remove the entry?
	if stats.Feeders[conn.ApiKey].Connections[feedprotocol.BEAST].ConnectionCount == 0 {
		if stats.Feeders[conn.ApiKey].Connections[feedprotocol.MLAT].ConnectionCount == 0 {
			delete(stats.Feeders, conn.ApiKey)
		}
	}
}

func (conn *Connection) RegisterConnection() {
	// updates the connected status of a feeder

	log := log.With().
		Strs("func", []string{"stats.go", "addConnection"}).
		Str("uuid", conn.ApiKey.String()).
		Str("src", conn.SrcAddr.String()).
		Str("dst", conn.DstAddr.String()).
		Str("code", conn.FeederCode).
		Uint("connNum", conn.ConnNum).
		Logger()

	if !statsInitialised() {
		log.Err(errors.New("stats not initialised")).Msg("stats not initialised")
	}

	// get protocol name
	protoName, err := feedprotocol.GetName(conn.Proto)
	if err != nil {
		log.Err(err).Msgf("unsupported protocol: %d", conn.Proto)
		return
	}

	log = log.With().Str("proto", protoName).Logger()

	stats.initFeederStats(conn.ApiKey)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// add connection
	c := ConnectionDetail{
		Src:           conn.SrcAddr,
		Dst:           conn.DstAddr,
		TimeConnected: time.Now(),
		BytesIn:       0,
		BytesOut:      0,
	}

	// define per-connection prometheus metrics
	c.promMetricBytesIn = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: PromNamespace,
		Subsystem: PromSubsystem,
		Name:      "feeder_data_in_bytes_total",
		Help:      "Per-feeder bytes received (in)",
		ConstLabels: prometheus.Labels{
			"protocol":    strings.ToLower(protoName),
			"uuid":        conn.ApiKey.String(),
			"label":       stats.Feeders[conn.ApiKey].Label,
			"connnum":     fmt.Sprintf("%d", conn.ConnNum),
			"feeder_code": conn.FeederCode,
		}})
	c.promMetricBytesOut = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: PromNamespace,
		Subsystem: PromSubsystem,
		Name:      "feeder_data_out_bytes_total",
		Help:      "Per-feeder bytes sent (out)",
		ConstLabels: prometheus.Labels{
			"protocol":    strings.ToLower(protoName),
			"uuid":        conn.ApiKey.String(),
			"label":       stats.Feeders[conn.ApiKey].Label,
			"connnum":     fmt.Sprintf("%d", conn.ConnNum),
			"feeder_code": conn.FeederCode,
		}})
	err = prometheus.Register(c.promMetricBytesIn)
	if err != nil {
		log.Err(err).Msg("could not register per-feeder prometheus bytes in metric")
	}
	err = prometheus.Register(c.promMetricBytesOut)
	if err != nil {
		log.Err(err).Msg("could not register per-feeder prometheus bytes out metric")
	}

	// add connection to feeder connections map
	stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails[conn.ConnNum] = c

	// update connection state
	if len(stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails) > 0 {
		pd := stats.Feeders[conn.ApiKey].Connections[conn.Proto]
		pd.Status = true
		pd.MostRecentConnection = c.TimeConnected
		pd.ConnectionCount = len(stats.Feeders[conn.ApiKey].Connections[conn.Proto].ConnectionDetails)
		stats.Feeders[conn.ApiKey].Connections[conn.Proto] = pd
	}

	// update time updated
	s := stats.Feeders[conn.ApiKey]
	s.TimeUpdated = time.Now()

	// write stats entry
	stats.Feeders[conn.ApiKey] = s
}

func httpRenderStats(w http.ResponseWriter, r *http.Request) {

	log := log.With().
		Strs("func", []string{"stats.go", "httpRenderStats"}).
		Str("RemoteAddr", r.RemoteAddr).
		Str("url", r.URL.Path).
		Str("RequestURI", r.RequestURI).
		Logger()

	// Template helper functions
	funcMap := template.FuncMap{

		// human readable / pretty printable data units
		"humanReadableDataUnits": func(n uint64) string {

			var prefix byte = ' '
			var out string

			if n > 1024 {
				prefix = 'K'
			}
			if n > 1048576 {
				prefix = 'M'
			}
			if n > 1073741824 {
				prefix = 'G'
			}
			if n > 1099511627776 {
				prefix = 'T'
			}
			if n > 1125899906842624 {
				prefix = 'P'
			}

			switch prefix {
			case ' ':
				out = fmt.Sprintf("%dB", n)
			case 'K':
				out = fmt.Sprintf("%.1fK", float32(n)/1024.0)
			case 'M':
				out = fmt.Sprintf("%.2fM", float32(n)/1024.0/1024.0)
			case 'G':
				out = fmt.Sprintf("%.3fG", float32(n)/1024.0/1024.0/1024.0)
			case 'T':
				out = fmt.Sprintf("%.4fT", float32(n)/1024.0/1024.0/1024.0/1024.0)
			case 'P':
				out = fmt.Sprintf("%.5fP", float32(n)/1024.0/1024.0/1024.0/1024.0/1024.0)
			}

			return out
		},
	}

	// Make and parse the HTML template
	t, err := template.New("stats").Funcs(funcMap).Parse(statsTemplate)
	if err != nil {
		log.Panic().AnErr("err", err).Msg("could not render statsTemplate")
	}

	// Render the data
	stats.mu.RLock()
	defer stats.mu.RUnlock()
	err = t.Execute(w, stats.Feeders)
	if err != nil {
		fmt.Println(err)
		log.Panic().AnErr("err", err).Msg("could not execute statsTemplate")
	}
}

func statsEvictor() {

	// loop through stats data, evict any feeders that have been inactive for over 60 seconds
	for {
		var toEvict []uuid.UUID
		// var activeBeast, activeMLAT uint

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

		time.Sleep(time.Minute * 1)
	}
}

func apiReturnAllFeeders(w http.ResponseWriter, r *http.Request) {

	log := log.With().
		Strs("func", []string{"stats.go", "apiReturnAllFeeders"}).
		Str("RemoteAddr", r.RemoteAddr).
		Str("url", r.URL.Path).
		Str("RequestURI", r.RequestURI).
		Str("api", "apiReturnAllFeeders").
		Logger()

	// get a read lock on stats
	stats.mu.RLock()
	defer stats.mu.RUnlock()

	// prepare response variable
	var resp APIResponse

	// get data
	resp.Data = stats.Feeders

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

	log := log.With().
		Strs("func", []string{"stats.go", "apiReturnSingleFeeder"}).
		Str("RemoteAddr", r.RemoteAddr).
		Str("url", r.URL.Path).
		Str("RequestURI", r.RequestURI).
		Logger()

	// prepare response variable
	var resp APIResponse

	// try to match the path for the api query for single feeder by uuid, eg:
	// /api/v1/feeder/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	if matchUrlSingleFeeder.Match([]byte(strings.ToLower(r.URL.Path))) {

		// try to extract uuid from path
		clientApiKey, err := uuid.Parse((string(matchUUID.Find([]byte(strings.ToLower(r.URL.Path))))))
		if err != nil {
			log.Err(err).Msg("could not get uuid from url")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// look up feeder by uuid
		stats.mu.RLock()
		defer stats.mu.RUnlock()
		val, ok := stats.Feeders[clientApiKey]
		if !ok {
			log.Error().Any("resp", resp).Msg("feeder not found")
			w.WriteHeader(http.StatusBadRequest)
			return
		} else {
			resp.Data = val
		}

		// prepare response
		output, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			log.Error().Any("resp", resp).Msg("error marshalling response into json")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Add("Content-Type", "application/json")
		w.Write(output)
		return

	} else {
		log.Error().Msg("path did not match single feeder")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

}

func Init(addr string) {

	log := log.With().
		Strs("func", []string{"stats.go", "statsManager"}).
		Str("addr", addr).
		Logger()

	// init stats variable
	stats.Feeders = make(map[uuid.UUID]FeederStats)

	// init regexps
	matchUrlSingleFeeder = regexp.MustCompile(`^/api/v1/feeder/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/?$`)
	matchUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

	// start up stats evictor
	go statsEvictor()

	// set initialised
	initialisedMu.Lock()
	initialised = true
	initialisedMu.Unlock()

	// stats http server routes
	http.HandleFunc("/", httpRenderStats)
	http.HandleFunc("/api/v1/feeder/", apiReturnSingleFeeder)
	http.HandleFunc("/api/v1/feeders/", apiReturnAllFeeders)

	// prometheus endpoint
	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/metrics/", promhttp.Handler())

	// start stats http server
	go func() {
		log.Info().Str("addr", addr).Msg("starting statistics listener")
		err := http.ListenAndServe(addr, nil)
		if err != nil {
			log.Panic().AnErr("err", err).Msg("stats server stopped")
		}
	}()
}
