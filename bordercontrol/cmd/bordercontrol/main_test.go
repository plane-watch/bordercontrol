package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/stunnel"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/testutil/daemon"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})
}

func PrepTestEnvironmentTLSClientConfig(sni string) (*tls.Config, error) {
	// prepares client-side TLS config for testing

	var tlsClientConfig tls.Config

	// load root CAs
	scp, err := x509.SystemCertPool()
	if err != nil {
		return &tls.Config{}, err
	}

	// set up tls config
	tlsClientConfig = tls.Config{
		RootCAs:            scp,
		ServerName:         sni,
		InsecureSkipVerify: true,
	}

	return &tlsClientConfig, nil
}

func TestDFWTB(t *testing.T) {
	// don't mess with the banner!
	bannerCheckSum := sha256.Sum256([]byte(banner))
	expectedCheckSum := [32]uint8{28, 39, 253, 10, 28, 108, 170, 133, 71, 150, 147, 107, 235, 39, 187, 141, 112, 229, 54, 58, 2, 39, 205, 10, 136, 172, 42, 112, 13, 56, 182, 97}
	assert.Equal(t, expectedCheckSum, bannerCheckSum, "don't mess with the banner! :-)")
}

func TestGetRepoInfo(t *testing.T) {
	ch, ct := getRepoInfo()

	// return unknown during testing
	assert.Equal(t, "unknown", ch)
	assert.Equal(t, "unknown", ct)
}

func TestLogNumGoroutines(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		logNumGoroutines(ctx, time.Second)
		wg.Done()
	}()
	time.Sleep(time.Second * 2)
	cancel()
	wg.Wait()
}

func TestListenWithContext(t *testing.T) {

	tmpListener, err := nettest.NewLocalListener("tcp4")
	require.NoError(t, err)

	err = tmpListener.Close()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		listenWithContext(
			ctx,
			tmpListener.Addr().String(),
			feedprotocol.BEAST,
			"test-feed-in-",
			12345,
		)
	}()

	time.Sleep(time.Second * 5)

	cancel()

	time.Sleep(time.Second * 5)

}

func TestEndToEnd(t *testing.T) {

	const (
		TestDaemonDockerSocket = "/run/containerd/containerd.sock"
	)

	var (

		// mock ATC server
		ATCServer *httptest.Server

		// mock ATC server data
		TestFeederAPIKeyWorking = uuid.New()
		TestFeederLabel         = "Test Feeder 123"
		TestFeederLatitude      = 123.456789
		TestFeederLongitude     = 98.765432
		TestFeederMux           = "test-mux"
		TestFeederCode          = "ABCD-1234"

		// mock ATC server credentials
		TestATCUser      = "testuser"
		TestATCPassword  = "testpass"
		TestATCAuthToken = "testauthtoken"

		// test docker daemon
		TestDockerDaemon       *daemon.Daemon
		TestDockerDaemonTmpDir string

		// test NATS server
		TestNATSServer *natsserver.Server

		// test SSL/TLS
		keyFile, certFile *os.File
		tmpDir            string

		// client
		clientTLSConfig *tls.Config

		wg sync.WaitGroup

		err error
	)

	t.Run("prepare test environment", func(t *testing.T) {

		t.Run("prep client tls config", func(t *testing.T) {
			clientTLSConfig, err = PrepTestEnvironmentTLSClientConfig(TestFeederAPIKeyWorking.String())
			require.NoError(t, err)
		})

		t.Run("run test ATC server", func(t *testing.T) {
			// Thanks to: https://medium.com/zus-health/mocking-outbound-http-requests-in-go-youre-probably-doing-it-wrong-60373a38d2aa

			// prep test server
			ATCServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

				switch r.URL.Path {

				case "/api/user/sign_in":

					// check request
					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, http.MethodPost, r.Method)
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)

					// ensure correct creds
					require.Equal(
						t,
						fmt.Sprintf(`{"user":{"email":"%s","password":"%s"}}`, TestATCUser, TestATCPassword),
						string(body),
					)

					// auth token
					w.Header().Add("Authorization", TestATCAuthToken)

					// response code
					w.WriteHeader(http.StatusOK)

				case fmt.Sprintf("/api/v1/feeders/%s.json", strings.ToLower(TestFeederAPIKeyWorking.String())):

					// check request
					require.Equal(t, TestATCAuthToken, r.Header.Get("Authorization"))
					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, http.MethodGet, r.Method)

					// mock response

					resp := fmt.Sprintf(
						`{"feeder":{"api_key":"%s","label":"%s","latitude":"%f","longitude":"%f","mux":"%s","FeederCode:"%s"}}`,
						TestFeederAPIKeyWorking,
						TestFeederLabel,
						TestFeederLatitude,
						TestFeederLongitude,
						TestFeederMux,
						TestFeederCode,
					)

					// response code
					w.WriteHeader(http.StatusOK)

					// response body
					w.Write([]byte(resp))

				case fmt.Sprintf("/api/v1/feeders.json"):

					// check request
					require.Equal(t, TestATCAuthToken, r.Header.Get("Authorization"))
					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, http.MethodGet, r.Method)

					// mock response
					resp := fmt.Sprintf(
						`{"Feeders":[{"ApiKey":"%s","Label":"%s","Latitude":"%f","Longitude":"%f","Mux":"%s", "FeederCode":"%s"}]}`,
						TestFeederAPIKeyWorking,
						TestFeederLabel,
						TestFeederLatitude,
						TestFeederLongitude,
						TestFeederMux,
						TestFeederCode,
					)

					// response code
					w.WriteHeader(http.StatusOK)

					// response body
					w.Write([]byte(resp))

				default:
					t.Log("invalid request URL:", r.URL.Path)
					t.FailNow()
				}

			}))

		})

		t.Run("generate self-signed cert & key for testing", func(t *testing.T) {
			// Thanks to: https://go.dev/src/crypto/tls/generate_cert.go

			// prep temp dir & temp files
			tmpDir, err = os.MkdirTemp(os.TempDir(), "bordercontrol_testing_*")
			require.NoError(t, err)
			keyFile, err = os.CreateTemp(tmpDir, "self_signed_tls_key.*")
			require.NoError(t, err)
			certFile, err = os.CreateTemp(tmpDir, "self_signed_tls_key.*")
			require.NoError(t, err)

			// prep certificate info
			hosts := []string{"localhost"}
			ipAddrs := []net.IP{net.IPv4(127, 0, 0, 1)}
			notBefore := time.Now()
			notAfter := time.Now().Add(time.Minute * 15)
			isCA := true

			// generate private key
			_, priv, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)

			keyUsage := x509.KeyUsageDigitalSignature

			// generate serial number
			serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
			serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
			require.NoError(t, err)

			// prep cert template
			template := x509.Certificate{
				SerialNumber: serialNumber,
				Subject: pkix.Name{
					Organization: []string{"plane.watch"},
				},
				NotBefore: notBefore,
				NotAfter:  notAfter,

				KeyUsage:              keyUsage,
				ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
				BasicConstraintsValid: true,
			}

			// add hostname(s)
			for _, host := range hosts {
				template.DNSNames = append(template.DNSNames, host)
			}

			// add ip(s)
			for _, ip := range ipAddrs {
				template.IPAddresses = append(template.IPAddresses, ip)
			}

			// if self-signed, include CA
			if isCA {
				template.IsCA = true
				template.KeyUsage |= x509.KeyUsageCertSign
			}

			// create certificate
			derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, priv.Public().(ed25519.PublicKey), priv)
			require.NoError(t, err)

			// encode certificate
			err = pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
			require.NoError(t, err)

			// marhsal private key
			privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
			require.NoError(t, err)

			// write private key
			err = pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
			require.NoError(t, err)

		})

		t.Run("start testing docker daemon", func(t *testing.T) {
			TestDockerDaemonTmpDir, err = os.MkdirTemp("", "bordercontrol-go-test-*") // get temp path for test docker daemon
			require.NoError(t, err)
			TestDockerDaemon, err = daemon.NewDaemon( // create test docker daemon
				TestDockerDaemonTmpDir,
				daemon.WithContainerdSocket(TestDaemonDockerSocket),
			)
			require.NoError(t, err)
			err = TestDockerDaemon.StartWithError()
			require.NoError(t, err)
		})

		t.Run("start testing NATS server", func(t *testing.T) {
			// get host & port for testing
			tmpListener, err := nettest.NewLocalListener("tcp4")
			require.NoError(t, err)

			natsHost := strings.Split(tmpListener.Addr().String(), ":")[0]
			natsPort, err := strconv.Atoi(strings.Split(tmpListener.Addr().String(), ":")[1])
			require.NoError(t, err)
			tmpListener.Close()

			// create nats server
			TestNATSServer, err = natsserver.NewServer(&natsserver.Options{
				ServerName: "bordercontrol_test_server",
				Host:       natsHost,
				Port:       natsPort,
			})
			require.NoError(t, err)

			// start nats server
			TestNATSServer.Start()

			require.True(t, TestNATSServer.ReadyForConnections(time.Second*5))
		})
	})

	t.Log("setting environment variables for testing")

	// store original DOCKER_HOST
	origEnvDockerHost := os.Getenv("DOCKER_HOST")
	t.Logf("original DOCKER_HOST: %s", os.Getenv("DOCKER_HOST"))
	defer func(t *testing.T) {
		err := os.Setenv("DOCKER_HOST", origEnvDockerHost)
		require.NoError(t, err)
		t.Logf("final DOCKER_HOST: %s", os.Getenv("DOCKER_HOST"))
	}(t)

	// update DOCKER_HOST with socket for test docker daemon
	err = os.Setenv("DOCKER_HOST", TestDockerDaemon.Sock())
	require.NoError(t, err)
	t.Logf("updated DOCKER_HOST: %s", os.Getenv("DOCKER_HOST"))

	// store original ATC_URL
	origEnvATCUrl := os.Getenv("ATC_URL")
	t.Logf("original ATC_URL: %s", os.Getenv("ATC_URL"))
	defer func(t *testing.T) {
		err := os.Setenv("ATC_URL", origEnvATCUrl)
		require.NoError(t, err)
		t.Logf("final ATC_URL: %s", os.Getenv("ATC_URL"))
	}(t)

	// update ATC_URL with socket for test docker daemon
	err = os.Setenv("ATC_URL", ATCServer.URL)
	require.NoError(t, err)
	t.Logf("updated ATC_URL: %s", os.Getenv("ATC_URL"))

	// store original ATC_USER
	origEnvATCUser := os.Getenv("ATC_USER")
	t.Logf("original ATC_USER: %s", os.Getenv("ATC_USER"))
	defer func(t *testing.T) {
		err := os.Setenv("ATC_USER", origEnvATCUser)
		require.NoError(t, err)
		t.Logf("final ATC_USER: %s", os.Getenv("ATC_USER"))
	}(t)

	// update ATC_USER with socket for test docker daemon
	err = os.Setenv("ATC_USER", TestATCUser)
	require.NoError(t, err)
	t.Logf("updated ATC_USER: %s", os.Getenv("ATC_USER"))

	// store original ATC_PASS
	origEnvATCPass := os.Getenv("ATC_PASS")
	t.Logf("original ATC_PASS: %s", os.Getenv("ATC_PASS"))
	defer func(t *testing.T) {
		err := os.Setenv("ATC_PASS", origEnvATCPass)
		require.NoError(t, err)
		t.Logf("final ATC_PASS: %s", os.Getenv("ATC_PASS"))
	}(t)

	// update ATC_PASS with socket for test docker daemon
	err = os.Setenv("ATC_PASS", TestATCPassword)
	require.NoError(t, err)
	t.Logf("updated ATC_PASS: %s", os.Getenv("ATC_PASS"))

	// store original BC_CERT_FILE
	origEnvCertFile := os.Getenv("BC_CERT_FILE")
	t.Logf("original BC_CERT_FILE: %s", os.Getenv("BC_CERT_FILE"))
	defer func(t *testing.T) {
		err := os.Setenv("BC_CERT_FILE", origEnvCertFile)
		require.NoError(t, err)
		t.Logf("final BC_CERT_FILE: %s", os.Getenv("BC_CERT_FILE"))
	}(t)

	// update BC_CERT_FILE with socket for test docker daemon
	err = os.Setenv("BC_CERT_FILE", certFile.Name())
	require.NoError(t, err)
	t.Logf("updated BC_CERT_FILE: %s", os.Getenv("BC_CERT_FILE"))

	// store original BC_KEY_FILE
	origEnvKeyFile := os.Getenv("BC_KEY_FILE")
	t.Logf("original BC_KEY_FILE: %s", os.Getenv("BC_KEY_FILE"))
	defer func(t *testing.T) {
		err := os.Setenv("BC_KEY_FILE", origEnvKeyFile)
		require.NoError(t, err)
		t.Logf("final BC_KEY_FILE: %s", os.Getenv("BC_KEY_FILE"))
	}(t)

	// update BC_KEY_FILE with socket for test docker daemon
	err = os.Setenv("BC_KEY_FILE", keyFile.Name())
	require.NoError(t, err)
	t.Logf("updated BC_KEY_FILE: %s", os.Getenv("BC_KEY_FILE"))

	// store original PW_INGEST_SINK
	origEnvPwInjestSink := os.Getenv("PW_INGEST_SINK")
	t.Logf("original PW_INGEST_SINK: %s", os.Getenv("PW_INGEST_SINK"))
	defer func(t *testing.T) {
		err := os.Setenv("PW_INGEST_SINK", origEnvPwInjestSink)
		require.NoError(t, err)
		t.Logf("final PW_INGEST_SINK: %s", os.Getenv("PW_INGEST_SINK"))
	}(t)

	// update PW_INGEST_SINK with socket for test docker daemon
	err = os.Setenv("PW_INGEST_SINK", TestNATSServer.ClientURL())
	require.NoError(t, err)
	t.Logf("updated PW_INGEST_SINK: %s", os.Getenv("PW_INGEST_SINK"))

	// store original NATS
	origEnvNatsUrl := os.Getenv("NATS")
	t.Logf("original NATS: %s", os.Getenv("NATS"))
	defer func(t *testing.T) {
		err := os.Setenv("NATS", origEnvNatsUrl)
		require.NoError(t, err)
		t.Logf("final NATS: %s", os.Getenv("NATS"))
	}(t)

	// update NATS with socket for test docker daemon
	err = os.Setenv("NATS", TestNATSServer.ClientURL())
	require.NoError(t, err)
	t.Logf("updated NATS: %s", os.Getenv("NATS"))

	// store original NATS_INSTANCE
	origEnvNatsInstance := os.Getenv("NATS_INSTANCE")
	t.Logf("original NATS_INSTANCE: %s", os.Getenv("NATS_INSTANCE"))
	defer func(t *testing.T) {
		err := os.Setenv("NATS_INSTANCE", origEnvNatsInstance)
		require.NoError(t, err)
		t.Logf("final NATS_INSTANCE: %s", os.Getenv("NATS_INSTANCE"))
	}(t)

	// update NATS_INSTANCE with socket for test docker daemon
	err = os.Setenv("NATS_INSTANCE", "testinstance")
	require.NoError(t, err)
	t.Logf("updated NATS_INSTANCE: %s", os.Getenv("NATS_INSTANCE"))

	// get temp ports
	beastAddr, err := nettest.NewLocalListener("tcp4")
	require.NoError(t, err)
	beastAddr.Close()
	mlatAddr, err := nettest.NewLocalListener("tcp4")
	require.NoError(t, err)
	mlatAddr.Close()
	apiAddr, err := nettest.NewLocalListener("tcp4")
	require.NoError(t, err)
	apiAddr.Close()

	// store original BC_LISTEN_API
	origEnvListenAPI := os.Getenv("BC_LISTEN_API")
	t.Logf("original BC_LISTEN_API: %s", os.Getenv("BC_LISTEN_API"))
	defer func(t *testing.T) {
		err := os.Setenv("BC_LISTEN_API", origEnvListenAPI)
		require.NoError(t, err)
		t.Logf("final BC_LISTEN_API: %s", os.Getenv("BC_LISTEN_API"))
	}(t)

	// update BC_LISTEN_API with socket for test docker daemon
	err = os.Setenv("BC_LISTEN_API", apiAddr.Addr().String())
	require.NoError(t, err)
	t.Logf("updated BC_LISTEN_API: %s", os.Getenv("BC_LISTEN_API"))

	// store original BC_LISTEN_BEAST
	origEnvListenBeast := os.Getenv("BC_LISTEN_BEAST")
	t.Logf("original BC_LISTEN_BEAST: %s", os.Getenv("BC_LISTEN_BEAST"))
	defer func(t *testing.T) {
		err := os.Setenv("BC_LISTEN_BEAST", origEnvListenBeast)
		require.NoError(t, err)
		t.Logf("final BC_LISTEN_BEAST: %s", os.Getenv("BC_LISTEN_BEAST"))
	}(t)

	// update BC_LISTEN_BEAST with socket for test docker daemon
	err = os.Setenv("BC_LISTEN_BEAST", beastAddr.Addr().String())
	require.NoError(t, err)
	t.Logf("updated BC_LISTEN_BEAST: %s", os.Getenv("BC_LISTEN_BEAST"))

	// store original BC_LISTEN_MLAT
	origEnvListenMLAT := os.Getenv("BC_LISTEN_MLAT")
	t.Logf("original BC_LISTEN_MLAT: %s", os.Getenv("BC_LISTEN_MLAT"))
	defer func(t *testing.T) {
		err := os.Setenv("BC_LISTEN_MLAT", origEnvListenMLAT)
		require.NoError(t, err)
		t.Logf("final BC_LISTEN_MLAT: %s", os.Getenv("BC_LISTEN_MLAT"))
	}(t)

	// update BC_LISTEN_MLAT with socket for test docker daemon
	err = os.Setenv("BC_LISTEN_MLAT", mlatAddr.Addr().String())
	require.NoError(t, err)
	t.Logf("updated BC_LISTEN_MLAT: %s", os.Getenv("BC_LISTEN_MLAT"))

	// store original BC_VERBOSE
	origEnvBCVerbose := os.Getenv("BC_VERBOSE")
	t.Logf("original BC_VERBOSE: %s", os.Getenv("BC_VERBOSE"))
	defer func(t *testing.T) {
		err := os.Setenv("BC_VERBOSE", origEnvBCVerbose)
		require.NoError(t, err)
		t.Logf("final BC_VERBOSE: %s", os.Getenv("BC_VERBOSE"))
	}(t)

	// update BC_VERBOSE with socket for test docker daemon
	err = os.Setenv("BC_VERBOSE", "true")
	require.NoError(t, err)
	t.Logf("updated BC_VERBOSE: %s", os.Getenv("BC_VERBOSE"))

	// override Finish for testing
	wg.Add(1)
	Finish = func(code int) {
		defer wg.Done()
		require.Equal(t, int(0), code)
	}

	// run our app
	go func(t *testing.T) {
		t.Log("Starting test instance of bordercontrol")
		app.SkipFlagParsing = true
		main()
	}(t)

	// wait for app to start up
	time.Sleep(time.Second * 5)

	// APP TESTING GOES HERE -----------------------------------------------------------

	// test reload cert/key
	stunnel.ReloadSignalChan <- syscall.SIGHUP

	t.Run("test client BEAST connection", func(t *testing.T) {
		conn, err := tls.Dial("tcp4", beastAddr.Addr().String(), clientTLSConfig)
		require.NoError(t, err)
		err = conn.SetDeadline(time.Now().Add(time.Second * 5))
		require.NoError(t, err)
		_, err = conn.Write([]byte("Hello World!"))
		require.NoError(t, err)
		time.Sleep(time.Second)
		err = conn.Close()
		require.NoError(t, err)
	})

	t.Run("test client MLAT connection", func(t *testing.T) {
		conn, err := tls.Dial("tcp4", mlatAddr.Addr().String(), clientTLSConfig)
		require.NoError(t, err)
		err = conn.SetDeadline(time.Now().Add(time.Second * 5))
		require.NoError(t, err)
		_, err = conn.Write([]byte("Hello World!"))
		require.NoError(t, err)
		time.Sleep(time.Second)
		err = conn.Close()
		require.NoError(t, err)
	})

	// TODO: add some tests in here

	// APP TESTING FINISHED HERE -------------------------------------------------------

	// send SIGTERM
	select {
	case sigTermChan <- syscall.SIGTERM:
		t.Log("sent SIGTERM")
	case <-time.After(time.Second * 5):
		t.Log("timeout sending SIGTERM")
		t.Fail()
	}

	// wait for app to finish up
	wg.Wait()

	// -------------

	t.Run("tear down test environment", func(t *testing.T) {

		t.Run("remove temp files", func(t *testing.T) {
			err = keyFile.Close()
			assert.NoError(t, err)
			err = certFile.Close()
			assert.NoError(t, err)
			err = os.RemoveAll(tmpDir)
			assert.NoError(t, err)
		})

		t.Run("stop testing docker daemon & clean-up", func(t *testing.T) {
			err := TestDockerDaemon.Kill()
			assert.NoError(t, err)
			TestDockerDaemon.Cleanup(t)
			err = os.RemoveAll(TestDockerDaemonTmpDir)
			assert.NoError(t, err)
		})

		t.Run("stop testing NATS server", func(t *testing.T) {
			TestNATSServer.Shutdown()
		})

		t.Run("stop testing ATC server", func(t *testing.T) {
			ATCServer.Close()
		})

	})
}
