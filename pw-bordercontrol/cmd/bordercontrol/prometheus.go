package main

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// FYI - Prometheus HTTP handler started via statsManager() in stats.go

// prometheus metrics
var (
	promTotalConnectionsBEAST = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "bordercontrol_total_connections_beast",
		Help: "The total number of active BEAST protocol connections being handled by this instance of bordercontrol.",
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u, _ := range stats.Feeders {
				n += float64(stats.Feeders[u].Connections["BEAST"].ConnectionCount)
			}
			return n
		})

	promTotalConnectionsMLAT = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "bordercontrol_total_connections_mlat",
		Help: "The total number of active MLAT protocol connections being handled by this instance of bordercontrol.",
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u, _ := range stats.Feeders {
				n += float64(stats.Feeders[u].Connections["MLAT"].ConnectionCount)
			}
			return n
		})

	promTotalFeeders = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "bordercontrol_total_feeders",
		Help: "The total number of feeders configured in ATC (active and inactive).",
	},
		func() float64 {
			validFeeders.mu.RLock()
			defer validFeeders.mu.RUnlock()
			return float64(len(validFeeders.Feeders))
		})

	promActiveFeeders = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "bordercontrol_active_feeders",
		Help: "The total number of feeders with an active connection to this instance of bordercontrol.",
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u, _ := range stats.Feeders {
				for p, _ := range stats.Feeders[u].Connections {
					if stats.Feeders[u].Connections[p].Status == true {
						n++
						break
					}
				}
			}
			return n
		})

	promActiveFeedersBEAST = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "bordercontrol_active_feeders_beast",
		Help: "The total number of feeders with an active BEAST protocol connection to this instance of bordercontrol.",
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u, _ := range stats.Feeders {
				if stats.Feeders[u].Connections["BEAST"].Status == true {
					n++
				}
			}
			return n
		})

	promActiveFeedersMLAT = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "bordercontrol_active_feeders_mlat",
		Help: "The total number of feeders with an active MLAT protocol connection to this instance of bordercontrol.",
	},
		func() float64 {
			stats.mu.RLock()
			defer stats.mu.RUnlock()
			n := float64(0)
			for u, _ := range stats.Feeders {
				if stats.Feeders[u].Connections["MLAT"].Status == true {
					n++
				}
			}
			return n
		})

	promFeederContainersImageCurrent = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "bordercontrol_feeder_containers_image_current",
		Help: "The number of feed-in-* containers running on this host that are using the latest feed-in image.",
	},
		func() float64 {
			n := float64(0)

			// set up docker client
			dockerCtx := context.Background()
			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				panic(err)
			}
			defer cli.Close()

			// prepare filter to find feed-in containers
			filters := filters.NewArgs()
			filters.Add("name", "feed-in-*")

			// find containers
			containers, err := cli.ContainerList(dockerCtx, types.ContainerListOptions{Filters: filters})
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

	promFeederContainersImageNotCurrent = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "bordercontrol_feeder_containers_image_not_current",
		Help: "The number of feed-in-* containers running on this host that are using an out of date feed-in image and require upgrading.",
	},
		func() float64 {
			n := float64(0)

			// set up docker client
			dockerCtx := context.Background()
			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				panic(err)
			}
			defer cli.Close()

			// prepare filter to find feed-in containers
			filters := filters.NewArgs()
			filters.Add("name", "feed-in-*")

			// find containers
			containers, err := cli.ContainerList(dockerCtx, types.ContainerListOptions{Filters: filters})
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
)
