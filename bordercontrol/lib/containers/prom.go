package containers

import (
	"fmt"
	"pw_bordercontrol/lib/stats"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (

	// prom metric "feedercontainers_image_current"
	promMetricFeederContainersImageCurrent prometheus.GaugeFunc

	// prom metric "feedercontainers_image_not_current"
	promMetricFeederContainersImageNotCurrent prometheus.GaugeFunc
)

// promMetricFeederContainersImageCurrentGaugeFunc provides a count of feed-in containers that are running the current feed-in image
func promMetricFeederContainersImageCurrentGaugeFunc(feedInImage, feedInContainerPrefix string) float64 {
	n := float64(0)

	// set up docker client
	getDockerClientMu.RLock()
	cli, err := getDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	// prepare filter to find feed-in containers
	filters := filters.NewArgs()
	filters.Add("name", fmt.Sprintf("%s*", feedInContainerPrefix))

	// find containers
	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{Filters: filters})
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
}

// promMetricFeederContainersImageCurrentGaugeFunc provides a count of feed-in containers that are not running the current feed-in image
func promMetricFeederContainersImageNotCurrentGaugeFunc(feedInImage, feedInContainerPrefix string) float64 {
	n := float64(0)

	// set up docker client
	getDockerClientMu.RLock()
	cli, err := getDockerClient()
	getDockerClientMu.RUnlock()
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	// prepare filter to find feed-in containers
	filters := filters.NewArgs()
	filters.Add("name", fmt.Sprintf("%s*", feedInContainerPrefix))

	// find containers
	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{Filters: filters})
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
}

// registerPromMetrics registers promMetricFeederContainersImageCurrent & promMetricFeederContainersImageNotCurrent
func registerPromMetrics(feedInImage, feedInContainerPrefix string) {

	promMetricFeederContainersImageCurrent = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: stats.PromNamespace,
		Subsystem: stats.PromSubsystem,
		Name:      "feedercontainers_image_current",
		Help:      "The number of feed-in-* containers running on this host that are using the latest feed-in image.",
	},
		func() float64 {
			return promMetricFeederContainersImageCurrentGaugeFunc(feedInImage, feedInContainerPrefix)
		})

	promMetricFeederContainersImageNotCurrent = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: stats.PromNamespace,
		Subsystem: stats.PromSubsystem,
		Name:      "feedercontainers_image_not_current",
		Help:      "The number of feed-in-* containers running on this host that are using an out of date feed-in image and require upgrading.",
	},
		func() float64 {
			return promMetricFeederContainersImageNotCurrentGaugeFunc(feedInImage, feedInContainerPrefix)
		})
}
