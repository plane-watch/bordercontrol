package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"pw_bordercontrol/lib/atc"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/docker/docker/testutil/daemon"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

func getMetricsFromTestServer(t *testing.T, requestURL string) (body string) {
	// request metrics
	res, err := http.Get(requestURL)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	bodyBytes, err := io.ReadAll(res.Body)
	assert.NoError(t, err)
	return string(bodyBytes)
}

func checkPromMetricsExist(t *testing.T, body string, expectedMetrics []string) {
	for _, expectedMetric := range expectedMetrics {
		t.Log("checking existence of:", expectedMetric)
		assert.Equal(t,
			1,
			strings.Count(body, expectedMetric),
		)
	}
}

func checkPromMetricsNotExist(t *testing.T, body string, notExpectedMetrics []string) {
	for _, notExpectedMetric := range notExpectedMetrics {
		t.Log("checking for absence of:", notExpectedMetric)
		assert.Equal(t,
			0,
			strings.Count(body, notExpectedMetric),
		)
	}
}

func TestStats(t *testing.T) {

	// starting test docker daemon
	t.Log("starting test docker daemon")
	TestDaemon := daemon.New(
		t,
		daemon.WithContainerdSocket(TestDaemonDockerSocket),
	)
	TestDaemon.Start(t)

	// prep testing client
	t.Log("prep testing client")
	getDockerClient = func() (ctx *context.Context, cli *client.Client, err error) {
		log.Debug().Msg("using test docker client")
		cctx := context.Background()
		cli = TestDaemon.NewClientT(t, client.WithAPIVersionNegotiation())
		return &cctx, cli, nil
	}

	validFeeders = atcFeeders{}

	// start statsManager testing server
	statsManagerMu.RLock()
	if statsManagerAddr == "" {
		statsManagerMu.RUnlock()
		// get address for testing
		nl, err := nettest.NewLocalListener("tcp4")
		assert.NoError(t, err)
		nl.Close()
		go statsManager(nl.Addr().String())

		// wait for server to come up
		time.Sleep(1 * time.Second)
	} else {
		statsManagerMu.RUnlock()
	}

	// prep url pats
	statsBaseURL := fmt.Sprintf("http://%s", statsManagerAddr)
	metricsURL := fmt.Sprintf("%s/metrics", statsBaseURL)

	body := getMetricsFromTestServer(t, metricsURL)

	expectedMetrics := []string{
		`pw_bordercontrol_connections{protocol="beast"} 0`,
		`pw_bordercontrol_connections{protocol="mlat"} 0`,
		`pw_bordercontrol_data_in_bytes_total{protocol="beast"} 0`,
		`pw_bordercontrol_data_in_bytes_total{protocol="mlat"} 0`,
		`pw_bordercontrol_data_out_bytes_total{protocol="beast"} 0`,
		`pw_bordercontrol_data_out_bytes_total{protocol="mlat"} 0`,
		`pw_bordercontrol_feedercontainers_image_current 0`,
		`pw_bordercontrol_feedercontainers_image_not_current 0`,
		`pw_bordercontrol_feeders 0`,
		`pw_bordercontrol_feeders_active{protocol="beast"} 0`,
		`pw_bordercontrol_feeders_active{protocol="mlat"} 0`,
	}

	// tests
	checkPromMetricsExist(t, body, expectedMetrics)

	// add a feeder
	ip := net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 12345,
	}
	u := uuid.New()
	fc := feederClient{
		clientApiKey: u,
		refLat:       123.4567,
		refLon:       98.7654,
		mux:          "test-mux",
		label:        "test-feeder",
	}

	// add valid feeder
	validFeeders.mu.Lock()
	validFeeders.Feeders = append(validFeeders.Feeders, atc.Feeder{
		ApiKey: u,
	})
	validFeeders.mu.Unlock()

	// init stats variable
	stats.Feeders = make(map[uuid.UUID]FeederStats)

	// check num conns
	assert.Equal(t, 0, stats.getNumConnections(u, protoBeast))
	assert.Equal(t, 0, stats.getNumConnections(u, protoBeast))

	// add some fake feeder connections
	stats.setFeederDetails(&fc)
	stats.addConnection(u, &ip, &ip, protoBeast, 1)
	stats.addConnection(u, &ip, &ip, protoMLAT, 2)

	// check num conns
	assert.Equal(t, 1, stats.getNumConnections(u, protoBeast))
	assert.Equal(t, 1, stats.getNumConnections(u, protoBeast))

	// add some traffic
	stats.incrementByteCounters(u, 1, 100, 200)
	stats.incrementByteCounters(u, 2, 300, 400)

	body = getMetricsFromTestServer(t, metricsURL)

	// new expected metrics
	expectedMetrics = []string{
		`pw_bordercontrol_connections{protocol="beast"} 1`,
		`pw_bordercontrol_connections{protocol="mlat"} 1`,
		`pw_bordercontrol_data_in_bytes_total{protocol="beast"} 100`,
		`pw_bordercontrol_data_in_bytes_total{protocol="mlat"} 300`,
		`pw_bordercontrol_data_out_bytes_total{protocol="beast"} 200`,
		`pw_bordercontrol_data_out_bytes_total{protocol="mlat"} 400`,
		`pw_bordercontrol_feedercontainers_image_current 0`,
		`pw_bordercontrol_feedercontainers_image_not_current 0`,
		`pw_bordercontrol_feeders 1`,
		`pw_bordercontrol_feeders_active{protocol="beast"} 1`,
		`pw_bordercontrol_feeders_active{protocol="mlat"} 1`,
		fmt.Sprintf(`pw_bordercontrol_feeder_data_in_bytes_total{connnum="1",label="%s",protocol="beast",uuid="%s"} 100`, fc.label, fc.clientApiKey),
		fmt.Sprintf(`pw_bordercontrol_feeder_data_in_bytes_total{connnum="2",label="%s",protocol="mlat",uuid="%s"} 300`, fc.label, fc.clientApiKey),
		fmt.Sprintf(`pw_bordercontrol_feeder_data_out_bytes_total{connnum="1",label="%s",protocol="beast",uuid="%s"} 200`, fc.label, fc.clientApiKey),
		fmt.Sprintf(`pw_bordercontrol_feeder_data_out_bytes_total{connnum="2",label="%s",protocol="mlat",uuid="%s"} 400`, fc.label, fc.clientApiKey),
	}

	// tests
	checkPromMetricsExist(t, body, expectedMetrics)

	// add some traffic
	stats.incrementByteCounters(u, 1, 1024, 1048576)
	stats.incrementByteCounters(u, 2, 1073741824, 1099511627776)

	// test APIs
	_ = getMetricsFromTestServer(t, fmt.Sprintf("%s/api/v1/feeders/", statsBaseURL))
	_ = getMetricsFromTestServer(t, fmt.Sprintf("%s/api/v1/feeder/%s", statsBaseURL, u.String()))
	_ = getMetricsFromTestServer(t, fmt.Sprintf("%s", statsBaseURL))

	// add another beast connection
	stats.addConnection(u, &ip, &ip, protoBeast, 3)

	// remove connections (working)
	stats.delConnection(u, protoBeast, 1)

	// remove connections (working)
	stats.delConnection(u, protoBeast, 3)

	// remove connection (connnum not found)
	stats.delConnection(u, protoBeast, 1)

	// remove connection (proto not found)
	stats.delConnection(u, "no_such_proto", 2)

	// remove connections (working)
	stats.delConnection(u, protoMLAT, 2)

	// check num conns
	assert.Equal(t, 0, stats.getNumConnections(u, protoBeast))
	assert.Equal(t, 0, stats.getNumConnections(u, protoBeast))

	body = getMetricsFromTestServer(t, metricsURL)

	// new expected metrics
	expectedMetrics = []string{
		`pw_bordercontrol_connections{protocol="beast"} 0`,
		`pw_bordercontrol_connections{protocol="mlat"} 0`,
		`pw_bordercontrol_data_in_bytes_total{protocol="beast"} 1124`,
		`pw_bordercontrol_data_in_bytes_total{protocol="mlat"} 1073742124`,
		`pw_bordercontrol_data_out_bytes_total{protocol="beast"} 1048776`,
		`pw_bordercontrol_data_out_bytes_total{protocol="mlat"} 1099511628176`,
		`pw_bordercontrol_feedercontainers_image_current 0`,
		`pw_bordercontrol_feedercontainers_image_not_current 0`,
		`pw_bordercontrol_feeders 1`,
		`pw_bordercontrol_feeders_active{protocol="beast"} 0`,
		`pw_bordercontrol_feeders_active{protocol="mlat"} 0`,
	}
	notExpectedMetrics := []string{
		fmt.Sprintf(`pw_bordercontrol_feeder_data_in_bytes_total{connnum="1",label="%s",protocol="beast",uuid="%s"}`, fc.label, fc.clientApiKey),
		fmt.Sprintf(`pw_bordercontrol_feeder_data_in_bytes_total{connnum="2",label="%s",protocol="mlat",uuid="%s"}`, fc.label, fc.clientApiKey),
		fmt.Sprintf(`pw_bordercontrol_feeder_data_out_bytes_total{connnum="1",label="%s",protocol="beast",uuid="%s"}`, fc.label, fc.clientApiKey),
		fmt.Sprintf(`pw_bordercontrol_feeder_data_out_bytes_total{connnum="2",label="%s",protocol="mlat",uuid="%s"}`, fc.label, fc.clientApiKey),
	}

	checkPromMetricsExist(t, body, expectedMetrics)
	checkPromMetricsNotExist(t, body, notExpectedMetrics)

	// clean up
	t.Log("cleaning up")
	TestDaemon.Stop(t)
	TestDaemon.Cleanup(t)

}
