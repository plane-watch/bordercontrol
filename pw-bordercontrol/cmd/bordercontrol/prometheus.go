package main

import (
	"fmt"
	"pw_bordercontrol/lib/containers"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	promNamespace = "pw"
	promSubsystem = "bordercontrol"
)

// FYI
//  - Prometheus HTTP handler started via statsManager() in stats.go
//  - per-feeder metrics are registered in addConnection() in stats.go
//  - per-feeder metrics are unregistered in delConnection() in stats.go

var (
	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   promNamespace,
		Subsystem:   promSubsystem,
		Name:        "connections",
		Help:        "The total number of active connections being handled by this instance of bordercontrol.",
		ConstLabels: prometheus.Labels{"protocol": "beast"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u := range stats.Feeders {
				n += float64(stats.Feeders[u].Connections[protoBEAST].ConnectionCount)
			}
			return n
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   promNamespace,
		Subsystem:   promSubsystem,
		Name:        "connections",
		Help:        "The total number of active connections being handled by this instance of bordercontrol.",
		ConstLabels: prometheus.Labels{"protocol": "mlat"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u := range stats.Feeders {
				n += float64(stats.Feeders[u].Connections[protoMLAT].ConnectionCount)
			}
			return n
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "feeders",
		Help:      "The total number of feeders configured in ATC (active and inactive).",
	},
		func() float64 {
			validFeeders.mu.RLock()
			defer validFeeders.mu.RUnlock()
			return float64(len(validFeeders.Feeders))
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   promNamespace,
		Subsystem:   promSubsystem,
		Name:        "feeders_active",
		Help:        "The total number of feeders with an active connection to this instance of bordercontrol.",
		ConstLabels: prometheus.Labels{"protocol": "beast"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u := range stats.Feeders {
				if stats.Feeders[u].Connections[protoBEAST].Status == true {
					n++
				}
			}
			return n
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace:   promNamespace,
		Subsystem:   promSubsystem,
		Name:        "feeders_active",
		Help:        "The total number of feeders with an active connection to this instance of bordercontrol.",
		ConstLabels: prometheus.Labels{"protocol": "mlat"},
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u := range stats.Feeders {
				if stats.Feeders[u].Connections[protoMLAT].Status == true {
					n++
				}
			}
			return n
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "feedercontainers_image_current",
		Help:      "The number of feed-in-* containers running on this host that are using the latest feed-in image.",
	},
		func() float64 {
			n := float64(0)

			// set up docker client
			dockerCtx, cli, err := containers.GetDockerClient()
			if err != nil {
				panic(err)
			}
			defer cli.Close()

			// prepare filter to find feed-in containers
			filters := filters.NewArgs()
			filters.Add("name", fmt.Sprintf("%s*", feedInContainerPrefix))

			// find containers
			containers, err := cli.ContainerList(*dockerCtx, types.ContainerListOptions{Filters: filters})
			if err != nil {
				panic(err)
			}

			// for each container...
			for _, container := range containers {

				// check containers are running latest feed-in image
				if container.Image == feedInImage {
					n++
				}

			}
			return n
		})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Subsystem: promSubsystem,
		Name:      "feedercontainers_image_not_current",
		Help:      "The number of feed-in-* containers running on this host that are using an out of date feed-in image and require upgrading.",
	},
		func() float64 {
			n := float64(0)

			// set up docker client
			dockerCtx, cli, err := containers.GetDockerClient()
			if err != nil {
				panic(err)
			}
			defer cli.Close()

			// prepare filter to find feed-in containers
			filters := filters.NewArgs()
			filters.Add("name", fmt.Sprintf("%s*", feedInContainerPrefix))

			// find containers
			containers, err := cli.ContainerList(*dockerCtx, types.ContainerListOptions{Filters: filters})
			if err != nil {
				panic(err)
			}

			// for each container...
			for _, container := range containers {

				// check containers are running latest feed-in image
				if container.Image != feedInImage {
					n++
				}

			}
			return n
		})

	_ = promauto.NewCounterFunc(prometheus.CounterOpts{
		Namespace:   promNamespace,
		Subsystem:   promSubsystem,
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
		Namespace:   promNamespace,
		Subsystem:   promSubsystem,
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
		Namespace:   promNamespace,
		Subsystem:   promSubsystem,
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
		Namespace:   promNamespace,
		Subsystem:   promSubsystem,
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
