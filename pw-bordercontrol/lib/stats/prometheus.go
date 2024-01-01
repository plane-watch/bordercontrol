package stats

import (
	"pw_bordercontrol/lib/feedprotocol"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	PromNamespace = "pw"
	PromSubsystem = "bordercontrol"
)

// FYI
//  - Prometheus HTTP handler started via statsManager() in stats.go
//  - per-feeder metrics are registered in addConnection() in stats.go
//  - per-feeder metrics are unregistered in delConnection() in stats.go

var (
	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
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
				n += float64(stats.Feeders[u].Connections[feedprotocol.BEAST].ConnectionCount)
			}
			return n
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
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
				n += float64(stats.Feeders[u].Connections[feedprotocol.MLAT].ConnectionCount)
			}
			return n
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
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
				if stats.Feeders[u].Connections[feedprotocol.BEAST].Status == true {
					n++
				}
			}
			return n
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
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
				if stats.Feeders[u].Connections[feedprotocol.MLAT].Status == true {
					n++
				}
			}
			return n
		})

	_ = promauto.NewCounterFunc(prometheus.CounterOpts{
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
		})

	_ = promauto.NewCounterFunc(prometheus.CounterOpts{
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
		})

	_ = promauto.NewCounterFunc(prometheus.CounterOpts{
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
		})

	_ = promauto.NewCounterFunc(prometheus.CounterOpts{
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
		})
)
