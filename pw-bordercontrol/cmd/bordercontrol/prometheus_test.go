package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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
		assert.Equal(t,
			strings.Count(body, fmt.Sprintf(expectedMetric, 0)),
			1,
		)
	}

}
