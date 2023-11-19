package main

import (
	"fmt"
	"io"
	"net/http"
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
	fmt.Println(io.ReadAll(res.Body))

}
