package stats

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"pw_bordercontrol/lib/feedprotocol"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

var (
	// mock feeder details
	TestFeederAPIKey    = uuid.MustParse("6261B9C8-25C1-4B67-A5A2-51FC688E8A25") // not a real feeder api key, generated with uuidgen
	TestFeederLabel     = "Test Feeder 123"
	TestFeederLatitude  = 123.456789
	TestFeederLongitude = 98.765432
	TestFeederMux       = "test-mux"
	TestFeederCode      = "ABCD-1234"
	TestFeederAddr      = net.IPv4(127, 0, 0, 1)
	TestPWIngestSink    = "nats://pw-ingest-sink:12345"
	TestConnNumBEAST    = uint(1)
	TestConnNumMLAT     = uint(2)
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
		assert.Equal(t,
			1,
			strings.Count(body, expectedMetric),
			fmt.Sprintf(`expected to find: "%s"`, expectedMetric),
		)
		if t.Failed() {
			fmt.Println("---- BEGIN RESPONSE BODY ----")
			fmt.Println(body)
			fmt.Println("---- END RESPONSE BODY ----")
		}
	}
}

func checkPromMetricsNotExist(t *testing.T, body string, notExpectedMetrics []string) {
	for _, notExpectedMetric := range notExpectedMetrics {
		assert.Equal(t,
			0,
			strings.Count(body, notExpectedMetric),
			fmt.Sprintf(`expected not to find: "%s"`, notExpectedMetric),
		)
		if t.Failed() {
			fmt.Println("---- BEGIN RESPONSE BODY ----")
			fmt.Println(body)
			fmt.Println("---- END RESPONSE BODY ----")
		}
	}
}

func TestStats(t *testing.T) {

	// prep test connections
	TestConnBEAST := Connection{
		ApiKey: TestFeederAPIKey,
		SrcAddr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 23456,
		},
		DstAddr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 12345,
		},
		Proto:      feedprotocol.BEAST,
		FeederCode: TestFeederCode,
		ConnNum:    TestConnNumBEAST,
	}
	TestConnMLAT := Connection{
		ApiKey: TestFeederAPIKey,
		SrcAddr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 23457,
		},
		DstAddr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 12346,
		},
		Proto:      feedprotocol.MLAT,
		FeederCode: TestFeederCode,
		ConnNum:    TestConnNumMLAT,
	}

	// prep test prom metric names
	TestPromMetricFeederDataInBytesTotalBEAST := fmt.Sprintf(`pw_bordercontrol_feeder_data_in_bytes_total{connnum="%d",feeder_code="%s",label="%s",protocol="%s",uuid="%s"}`,
		TestConnNumBEAST,
		TestFeederCode,
		TestFeederLabel,
		"beast",
		TestFeederAPIKey,
	)
	TestPromMetricFeederDataInBytesTotalMLAT := fmt.Sprintf(`pw_bordercontrol_feeder_data_in_bytes_total{connnum="%d",feeder_code="%s",label="%s",protocol="%s",uuid="%s"}`,
		TestConnNumMLAT,
		TestFeederCode,
		TestFeederLabel,
		"mlat",
		TestFeederAPIKey,
	)
	TestPromMetricFeederDataOutBytesTotalBEAST := fmt.Sprintf(`pw_bordercontrol_feeder_data_out_bytes_total{connnum="%d",feeder_code="%s",label="%s",protocol="%s",uuid="%s"}`,
		TestConnNumBEAST,
		TestFeederCode,
		TestFeederLabel,
		"beast",
		TestFeederAPIKey,
	)
	TestPromMetricFeederDataOutBytesTotalMLAT := fmt.Sprintf(`pw_bordercontrol_feeder_data_out_bytes_total{connnum="%d",feeder_code="%s",label="%s",protocol="%s",uuid="%s"}`,
		TestConnNumMLAT,
		TestFeederCode,
		TestFeederLabel,
		"mlat",
		TestFeederAPIKey,
	)

	// get listenable address
	testListener, err := nettest.NewLocalListener("tcp")
	if err != nil {
		assert.Fail(t, "error creating test listener")
		t.FailNow()
	}
	testAddr := testListener.Addr().String()
	testListener.Close()

	t.Run("test statsInitialised false", func(t *testing.T) {
		assert.False(t, statsInitialised())
	})

	t.Run("test GetNumConnections ErrStatsNotInitialised", func(t *testing.T) {
		_, err := GetNumConnections(TestFeederAPIKey, feedprotocol.BEAST)
		assert.Error(t, err)
	})

	t.Run("test IncrementByteCounters ErrStatsNotInitialised", func(t *testing.T) {
		err := IncrementByteCounters(TestFeederAPIKey, 0, 1, 1)
		assert.Error(t, err)
		assert.Equal(t, ErrStatsNotInitialised.Error(), err.Error())
	})

	t.Run("test RegisterFeeder ErrStatsNotInitialised", func(t *testing.T) {
		f := FeederDetails{
			Label:      TestFeederLabel,
			FeederCode: TestFeederCode,
			ApiKey:     TestFeederAPIKey,
		}
		err := RegisterFeeder(f)
		assert.Error(t, err)
	})

	t.Run("test RegisterConnection ErrStatsNotInitialised", func(t *testing.T) {
		err := TestConnBEAST.RegisterConnection()
		assert.Error(t, err)
		assert.Equal(t, ErrStatsNotInitialised, err)
	})

	// initialising stats subsystem
	Init(testAddr)

	t.Run("test RegisterConnection ErrUnknownProtocol", func(t *testing.T) {
		c := TestConnBEAST
		c.Proto = feedprotocol.Protocol(254)
		err := c.RegisterConnection()
		assert.Error(t, err)
		assert.Equal(t, feedprotocol.ErrUnknownProtocol, err)
	})

	t.Run("test statsInitialised true", func(t *testing.T) {
		assert.True(t, statsInitialised())
	})

	t.Run("test RegisterFeeder", func(t *testing.T) {
		f := FeederDetails{
			Label:      TestFeederLabel,
			FeederCode: TestFeederCode,
			ApiKey:     TestFeederAPIKey,
		}
		err := RegisterFeeder(f)
		assert.NoError(t, err)
	})

	t.Run("test GetNumConnections BEAST 0", func(t *testing.T) {
		i, err := GetNumConnections(TestFeederAPIKey, feedprotocol.BEAST)
		assert.NoError(t, err)
		assert.Equal(t, 0, i)
	})

	t.Run("test GetNumConnections MLAT 0", func(t *testing.T) {
		i, err := GetNumConnections(TestFeederAPIKey, feedprotocol.BEAST)
		assert.NoError(t, err)
		assert.Equal(t, 0, i)
	})

	t.Run("test IncrementByteCounters ErrConnNumNotFound", func(t *testing.T) {
		err := IncrementByteCounters(TestFeederAPIKey, 0, 1, 1)
		assert.Error(t, err)
		assert.Equal(t, ErrConnNumNotFound.Error(), err.Error())
	})

	// make dupe connection to raise prom error: feeder_data_in_bytes_total
	promMetricForError := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: PromNamespace,
		Subsystem: PromSubsystem,
		Name:      "feeder_data_in_bytes_total",
		Help:      "Per-feeder bytes received (in)",
		ConstLabels: prometheus.Labels{
			"protocol":    strings.ToLower(TestConnBEAST.Proto.Name()),
			"uuid":        TestFeederAPIKey.String(),
			"label":       TestFeederLabel,
			"connnum":     fmt.Sprint(TestConnNumBEAST),
			"feeder_code": TestFeederCode,
		}})
	err = prometheus.Register(promMetricForError)
	assert.NoError(t, err)

	t.Run("test RegisterConnection BEAST prom error", func(t *testing.T) {
		err := TestConnBEAST.RegisterConnection()
		assert.Error(t, err)
	})

	// unregister dupe
	ok := prometheus.Unregister(promMetricForError)
	assert.True(t, ok)

	// make dupe connection to raise prom error: feeder_data_in_bytes_total
	promMetricForError = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: PromNamespace,
		Subsystem: PromSubsystem,
		Name:      "feeder_data_out_bytes_total",
		Help:      "Per-feeder bytes sent (out)",
		ConstLabels: prometheus.Labels{
			"protocol":    strings.ToLower(TestConnBEAST.Proto.Name()),
			"uuid":        TestFeederAPIKey.String(),
			"label":       TestFeederLabel,
			"connnum":     fmt.Sprint(TestConnNumBEAST),
			"feeder_code": TestFeederCode,
		}})
	err = prometheus.Register(promMetricForError)
	assert.NoError(t, err)

	t.Run("test RegisterConnection BEAST prom error", func(t *testing.T) {
		err := TestConnBEAST.RegisterConnection()
		assert.Error(t, err)
	})

	// unregister dupe
	ok = prometheus.Unregister(promMetricForError)
	assert.True(t, ok)

	t.Run("test RegisterConnection BEAST", func(t *testing.T) {
		err := TestConnBEAST.RegisterConnection()
		assert.NoError(t, err)
	})

	t.Run("test GetNumConnections BEAST 1", func(t *testing.T) {
		i, err := GetNumConnections(TestFeederAPIKey, feedprotocol.BEAST)
		assert.NoError(t, err)
		assert.Equal(t, 1, i)
	})

	t.Run("test RegisterConnection MLAT", func(t *testing.T) {
		err := TestConnMLAT.RegisterConnection()
		assert.NoError(t, err)
	})

	t.Run("test GetNumConnections BEAST 1", func(t *testing.T) {
		i, err := GetNumConnections(TestFeederAPIKey, feedprotocol.MLAT)
		assert.NoError(t, err)
		assert.Equal(t, 1, i)
	})

	t.Run("test prom metrics zero values", func(t *testing.T) {
		testURL := fmt.Sprintf("http://%s/metrics", testAddr)
		body := getMetricsFromTestServer(t, testURL)

		expectedMetrics := []string{
			`pw_bordercontrol_connections{protocol="beast"} 1`,
			`pw_bordercontrol_connections{protocol="mlat"} 1`,
			`pw_bordercontrol_data_in_bytes_total{protocol="beast"} 0`,
			`pw_bordercontrol_data_in_bytes_total{protocol="mlat"} 0`,
			`pw_bordercontrol_data_out_bytes_total{protocol="beast"} 0`,
			`pw_bordercontrol_data_out_bytes_total{protocol="mlat"} 0`,
			`pw_bordercontrol_feeders_active{protocol="beast"} 1`,
			`pw_bordercontrol_feeders_active{protocol="mlat"} 1`,
			fmt.Sprintf("%s 0", TestPromMetricFeederDataInBytesTotalBEAST),
			fmt.Sprintf("%s 0", TestPromMetricFeederDataInBytesTotalMLAT),
			fmt.Sprintf("%s 0", TestPromMetricFeederDataOutBytesTotalBEAST),
			fmt.Sprintf("%s 0", TestPromMetricFeederDataOutBytesTotalMLAT),
		}
		checkPromMetricsExist(t, body, expectedMetrics)
	})

	t.Run("test IncrementByteCounters BEAST", func(t *testing.T) {
		err := IncrementByteCounters(TestFeederAPIKey, TestConnNumBEAST, 10, 20)
		assert.NoError(t, err)
	})

	t.Run("test IncrementByteCounters MLAT", func(t *testing.T) {
		err := IncrementByteCounters(TestFeederAPIKey, TestConnNumMLAT, 100, 200)
		assert.NoError(t, err)
	})

	t.Run("test prom metrics nonzero values", func(t *testing.T) {
		testURL := fmt.Sprintf("http://%s/metrics", testAddr)
		body := getMetricsFromTestServer(t, testURL)

		expectedMetrics := []string{
			`pw_bordercontrol_connections{protocol="beast"} 1`,
			`pw_bordercontrol_connections{protocol="mlat"} 1`,
			`pw_bordercontrol_data_in_bytes_total{protocol="beast"} 10`,
			`pw_bordercontrol_data_in_bytes_total{protocol="mlat"} 100`,
			`pw_bordercontrol_data_out_bytes_total{protocol="beast"} 20`,
			`pw_bordercontrol_data_out_bytes_total{protocol="mlat"} 200`,
			`pw_bordercontrol_feeders_active{protocol="beast"} 1`,
			`pw_bordercontrol_feeders_active{protocol="mlat"} 1`,
			fmt.Sprintf("%s 10", TestPromMetricFeederDataInBytesTotalBEAST),
			fmt.Sprintf("%s 100", TestPromMetricFeederDataInBytesTotalMLAT),
			fmt.Sprintf("%s 20", TestPromMetricFeederDataOutBytesTotalBEAST),
			fmt.Sprintf("%s 200", TestPromMetricFeederDataOutBytesTotalMLAT),
		}
		checkPromMetricsExist(t, body, expectedMetrics)
	})

	t.Run("test UnregisterConnection BEAST", func(t *testing.T) {
		err := TestConnBEAST.UnregisterConnection()
		assert.NoError(t, err)
	})

	t.Run("test GetNumConnections BEAST 0", func(t *testing.T) {
		i, err := GetNumConnections(TestFeederAPIKey, feedprotocol.BEAST)
		assert.NoError(t, err)
		assert.Equal(t, 0, i)
	})

	t.Run("test UnregisterConnection MLAT", func(t *testing.T) {
		err := TestConnMLAT.UnregisterConnection()
		assert.NoError(t, err)
	})

	t.Run("test GetNumConnections MLAT 0", func(t *testing.T) {
		i, err := GetNumConnections(TestFeederAPIKey, feedprotocol.MLAT)
		assert.NoError(t, err)
		assert.Equal(t, 0, i)
	})

	t.Run("test prom metrics after unregisters", func(t *testing.T) {
		testURL := fmt.Sprintf("http://%s/metrics", testAddr)
		body := getMetricsFromTestServer(t, testURL)

		expectedMetrics := []string{
			`pw_bordercontrol_connections{protocol="beast"} 0`,
			`pw_bordercontrol_connections{protocol="mlat"} 0`,
			`pw_bordercontrol_data_in_bytes_total{protocol="beast"} 10`,
			`pw_bordercontrol_data_in_bytes_total{protocol="mlat"} 100`,
			`pw_bordercontrol_data_out_bytes_total{protocol="beast"} 20`,
			`pw_bordercontrol_data_out_bytes_total{protocol="mlat"} 200`,
			`pw_bordercontrol_feeders_active{protocol="beast"} 0`,
			`pw_bordercontrol_feeders_active{protocol="mlat"} 0`,
		}

		notExpectedMetrics := []string{
			TestPromMetricFeederDataInBytesTotalBEAST,
			TestPromMetricFeederDataInBytesTotalMLAT,
			TestPromMetricFeederDataOutBytesTotalBEAST,
			TestPromMetricFeederDataOutBytesTotalMLAT,
		}

		checkPromMetricsExist(t, body, expectedMetrics)
		checkPromMetricsNotExist(t, body, notExpectedMetrics)
	})
}
