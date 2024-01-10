package stats

import (
	"errors"
	"pw_bordercontrol/lib/feedprotocol"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	PromNamespace = "pw"
	PromSubsystem = "bordercontrol"
)

var (
	promCollectors   []prometheus.Collector // slice of all registered collectors (for unregistration during .Close())
	promCollectorsMu sync.RWMutex           // mutex for promCollectors

	// per-feeder prom metrics
	promFeederDataInBytesTotal  *prometheus.CounterVec
	promFeederDataOutBytesTotal *prometheus.CounterVec

	// custom errors
	ErrPromCounterDidNotUnregister = errors.New("prometheus metric did not unregister")
	ErrPromCouldNotFindCounter     = errors.New("could not find counter in promCollectors")
)

func registerGlobalCollectors() error {
	var (
		counters []prometheus.Collector
	)

	// define collectors

	counters = append(counters, prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   PromNamespace,
		Subsystem:   PromSubsystem,
		Name:        "connections",
		Help:        "The total number of active connections being handled by this instance of bordercontrol.",
		ConstLabels: prometheus.Labels{"protocol": "beast"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u := range stats.Feeders {
				n += float64(stats.Feeders[u].Connections[feedprotocol.ProtocolNameBEAST].ConnectionCount)
			}
			return n
		}))

	counters = append(counters, prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   PromNamespace,
		Subsystem:   PromSubsystem,
		Name:        "connections",
		Help:        "The total number of active connections being handled by this instance of bordercontrol.",
		ConstLabels: prometheus.Labels{"protocol": "mlat"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u := range stats.Feeders {
				n += float64(stats.Feeders[u].Connections[feedprotocol.ProtocolNameMLAT].ConnectionCount)
			}
			return n
		}))

	counters = append(counters, prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   PromNamespace,
		Subsystem:   PromSubsystem,
		Name:        "feeders_active",
		Help:        "The total number of feeders with an active connection to this instance of bordercontrol.",
		ConstLabels: prometheus.Labels{"protocol": "beast"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u := range stats.Feeders {
				if stats.Feeders[u].Connections[feedprotocol.ProtocolNameBEAST].Status == true {
					n++
				}
			}
			return n
		}))

	counters = append(counters, prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   PromNamespace,
		Subsystem:   PromSubsystem,
		Name:        "feeders_active",
		Help:        "The total number of feeders with an active connection to this instance of bordercontrol.",
		ConstLabels: prometheus.Labels{"protocol": "mlat"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u := range stats.Feeders {
				if stats.Feeders[u].Connections[feedprotocol.ProtocolNameMLAT].Status == true {
					n++
				}
			}
			return n
		}))

	counters = append(counters, prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace:   PromNamespace,
		Subsystem:   PromSubsystem,
		Name:        "data_in_bytes_total",
		Help:        "Bytes received (in) via protocol connection.",
		ConstLabels: prometheus.Labels{"protocol": "beast"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			return float64(stats.BytesInBEAST)
		}))

	counters = append(counters, prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace:   PromNamespace,
		Subsystem:   PromSubsystem,
		Name:        "data_out_bytes_total",
		Help:        "Bytes sent (out) via protocol connection.",
		ConstLabels: prometheus.Labels{"protocol": "beast"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			return float64(stats.BytesOutBEAST)
		}))

	counters = append(counters, prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace:   PromNamespace,
		Subsystem:   PromSubsystem,
		Name:        "data_in_bytes_total",
		Help:        "Bytes received (in) via protocol connection.",
		ConstLabels: prometheus.Labels{"protocol": "mlat"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			return float64(stats.BytesInMLAT)
		}))

	counters = append(counters, prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace:   PromNamespace,
		Subsystem:   PromSubsystem,
		Name:        "data_out_bytes_total",
		Help:        "Bytes sent (out) via protocol connection.",
		ConstLabels: prometheus.Labels{"protocol": "mlat"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			return float64(stats.BytesOutMLAT)
		}))

	// register collectors
	for _, c := range counters {
		err := registerCollector(c)
		if err != nil {
			return err
		}
	}

	return nil
}

func registerCollector(c prometheus.Collector) error {
	// registers a prometheus collector

	// register the collector
	err := prometheus.Register(c)
	if err != nil {
		return err
	}

	// add collector to slice
	promCollectorsMu.Lock()
	defer promCollectorsMu.Unlock()
	promCollectors = append(promCollectors, c)

	return nil
}

func unregisterCollector(c prometheus.Collector) error {
	// unregisters a prometheus collector

	b := prometheus.Unregister(c)
	if b != true {
		return ErrPromCounterDidNotUnregister
	}

	// remove collector from slice
	promCollectorsMu.Lock()
	defer promCollectorsMu.Unlock()
	for i := range promCollectors {
		if promCollectors[i] == c {
			promCollectors[i] = promCollectors[len(promCollectors)-1]
			promCollectors = promCollectors[:len(promCollectors)-1]
			return nil
		}
	}

	return ErrPromCouldNotFindCounter
}

func registerPerFeederCounterVecs() error {
	// define per-connection prometheus vectors

	var err error

	promFeederDataInBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: PromNamespace,
		Subsystem: PromSubsystem,
		Name:      "feeder_data_in_bytes_total",
		Help:      "Per-feeder bytes received (in)",
	}, []string{"protocol", "uuid", "connnum", "feeder_code"})
	err = registerCollector(promFeederDataInBytesTotal)
	if err != nil {
		return err
	}

	promFeederDataOutBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: PromNamespace,
		Subsystem: PromSubsystem,
		Name:      "feeder_data_out_bytes_total",
		Help:      "Per-feeder bytes sent (out)",
	}, []string{"protocol", "uuid", "connnum", "feeder_code"})
	err = registerCollector(promFeederDataOutBytesTotal)
	if err != nil {
		return err
	}

	return nil
}
