package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// FYI - Prometheus HTTP handler started via statsManager() in stats.go

// prometheus metrics
var (
	totalConnectionsBEAST = promauto.NewCounterFunc(prometheus.CounterOpts{
		Name: "total_connections_beast",
		Help: "The total number of active BEAST protocol connections being handled by this instance of bordercontrol.",
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			numConns := float64(0)
			for u, _ := range stats.Feeders {
				numConns += float64(stats.Feeders[u].Connections["BEAST"].ConnectionCount)
			}
			return numConns
		})

	totalConnectionsMLAT = promauto.NewCounterFunc(prometheus.CounterOpts{
		Name: "total_connections_mlat",
		Help: "The total number of active MLAT protocol connections being handled by this instance of bordercontrol.",
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			numConns := float64(0)
			for u, _ := range stats.Feeders {
				numConns += float64(stats.Feeders[u].Connections["MLAT"].ConnectionCount)
			}
			return numConns
		})

	totalFeeders = promauto.NewCounterFunc(prometheus.CounterOpts{
		Name: "total_feeders",
		Help: "The total number of feeders configured in ATC (active and inactive).",
	},
		func() float64 {
			validFeeders.mu.RLock()
			defer validFeeders.mu.RUnlock()
			return float64(len(validFeeders.Feeders))
		})
)
