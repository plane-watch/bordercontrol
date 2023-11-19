package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

func TestProm(t *testing.T) {

	// start metrics server
	srv, err := nettest.NewLocalListener("tcp4")
	assert.NoError(t, err)
	err = srv.Close()
	assert.NoError(t, err)
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		http.ListenAndServe(srv.Addr().String(), nil)
	}()

	// wait for server
	time.Sleep(time.Second * 1)

	// request metrics
	requestURL := fmt.Sprintf("http://%s/metrics", srv.Addr().String())
	res, err := http.Get(requestURL)
	assert.NoError(t, err)
	fmt.Printf("client: got response!\n")
	fmt.Printf("client: status code: %d\n", res.StatusCode)
	bodyBytes, err := io.ReadAll(res.Body)
	assert.NoError(t, err)
	body := string(bodyBytes)

	expectedMetrics := []string{
		`pw_bordercontrol_connections{protocol="beast"} %d`,
		`pw_bordercontrol_connections{protocol="mlat"} %d`,
		`pw_bordercontrol_data_in_bytes_total{protocol="beast"} %d`,
		`pw_bordercontrol_data_in_bytes_total{protocol="mlat"} %d`,
		`pw_bordercontrol_data_out_bytes_total{protocol="beast"} %d`,
		`pw_bordercontrol_data_out_bytes_total{protocol="mlat"} %d`,
		`pw_bordercontrol_feedercontainers_image_current %d`,
		`pw_bordercontrol_feedercontainers_image_not_current %d`,
		`pw_bordercontrol_feeders %d`,
		`pw_bordercontrol_feeders_active{protocol="beast"} %d`,
		`pw_bordercontrol_feeders_active{protocol="mlat"} %d`,
	}

	// tests
	for _, expectedMetric := range expectedMetrics {
		s := fmt.Sprintf(expectedMetric, 0)
		t.Log("checking for:", s)
		assert.Equal(t,
			strings.Count(body, s),
			1,
		)
	}

	u := uuid.New()

	// add a feeder
	ip := net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 12345,
	}
	fc := feederClient{
		clientApiKey: u,
		refLat:       123.4567,
		refLon:       98.7654,
		mux:          "test-mux",
		label:        "test-feeder",
	}

	stats.setFeederDetails(&fc)
	stats.addConnection(u, &ip, &ip, protoBeast, 1)

}
