package main

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

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
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		err = http.ListenAndServe(srv.Addr().String(), nil)
		assert.NoError(t, err)
		wg.Done()
	}()
	wg.Wait()

	// request metrics
	requestURL := fmt.Sprintf("http://%s", srv.Addr().String())
	res, err := http.Get(requestURL)
	assert.NoError(t, err)
	fmt.Printf("client: got response!\n")
	fmt.Printf("client: status code: %d\n", res.StatusCode)

}
