package stunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
)

var (
	TestSNI = uuid.New()
)

func GenerateSelfSignedTLSCertAndKey(keyFile, certFile *os.File) error {
	// Thanks to: https://go.dev/src/crypto/tls/generate_cert.go

	if !initialised {
		return ErrNotInitialised
	}

	// prep certificate info
	hosts := []string{"localhost"}
	ipAddrs := []net.IP{net.IPv4(127, 0, 0, 1)}
	notBefore := time.Now()
	notAfter := time.Now().Add(time.Minute * 15)
	isCA := true

	// generate private key
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	keyUsage := x509.KeyUsageDigitalSignature

	// generate serial number
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return err
	}

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
	if err != nil {
		return err
	}

	// encode certificate
	err = pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err != nil {
		return err
	}

	// marhsal private key
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}

	// write private key
	err = pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	if err != nil {
		return err
	}

	return nil
}

func PrepTestEnvironmentTLSCertAndKey() error {
	// prepares self-signed server certificate for testing

	if !initialised {
		return ErrNotInitialised
	}

	// prep cert file
	certFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_cert.pem")
	if err != nil {
		return err
	}
	defer func() {
		certFile.Close()
		os.Remove(certFile.Name())
	}()

	// prep key file
	keyFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_key.pem")
	if err != nil {
		return err
	}
	defer func() {
		keyFile.Close()
		os.Remove(keyFile.Name())
	}()

	// generate cert/key for testing
	err = GenerateSelfSignedTLSCertAndKey(keyFile, certFile)
	if err != nil {
		return err
	}

	// prep tls config for mocked server
	kpr, err := newKeypairReloader(certFile.Name(), keyFile.Name())
	if err != nil {
		return err
	}
	tlsConfig.GetCertificate = kpr.getCertificateFunc()
	return nil
}

func PrepTestEnvironmentTLSListener() (l net.Listener, err error) {
	// prepares server-side TLS listener for testing

	if !initialised {
		return l, ErrNotInitialised
	}

	// get temp listener address
	tempListener, err := nettest.NewLocalListener("tcp")
	if err != nil {
		return l, err
	}
	tlsListenAddr := tempListener.Addr().String()
	tempListener.Close()

	// configure temp listener
	return NewListener("tcp", tlsListenAddr)

}

func PrepTestEnvironmentTLSClientConfig(sni string) (*tls.Config, error) {
	// prepares client-side TLS config for testing

	if !initialised {
		return &tls.Config{}, ErrNotInitialised
	}

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

func TestStunnel(t *testing.T) {

	// ensure functions that need initialisation throw an error if not initialised
	t.Run("test not initialised", func(t *testing.T) {
		t.Run("Close", func(t *testing.T) {
			err := Close()
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("LoadCertAndKeyFromFile", func(t *testing.T) {
			err := LoadCertAndKeyFromFile("", "")
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("NewListener", func(t *testing.T) {
			_, err := NewListener("", "")
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("NewKeypairReloader", func(t *testing.T) {
			_, err := newKeypairReloader("", "")
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("GenerateSelfSignedTLSCertAndKey", func(t *testing.T) {

			// get test name & remove path separator chars
			tName := strings.ReplaceAll(t.Name(), "/", "_")

			// make temp file for cert
			tmpCertFileName := fmt.Sprintf("bordercontrol-testing-%s-certfile-*", tName)
			tmpCertFile, err := os.CreateTemp("", tmpCertFileName)
			require.NoError(t, err)
			t.Cleanup(func() {
				tmpCertFile.Close()
				os.Remove(tmpCertFile.Name())
			})

			// make temp file for key
			tmpKeyFileName := fmt.Sprintf("bordercontrol-testing-%s-keyfile-*", tName)
			tmpKeyFile, err := os.CreateTemp("", tmpKeyFileName)
			require.NoError(t, err)
			t.Cleanup(func() {
				tmpKeyFile.Close()
				os.Remove(tmpKeyFile.Name())
			})

			// finally, test
			err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("PrepTestEnvironmentTLSCertAndKey", func(t *testing.T) {
			err := PrepTestEnvironmentTLSCertAndKey()
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("PrepTestEnvironmentTLSListener", func(t *testing.T) {
			_, err := PrepTestEnvironmentTLSListener()
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
		t.Run("PrepTestEnvironmentTLSClientConfig", func(t *testing.T) {
			_, err := PrepTestEnvironmentTLSClientConfig("")
			require.Error(t, err)
			require.Equal(t, ErrNotInitialised.Error(), err.Error())
		})
	})

	// initialise stunnel subsystem, with SIGHUP as signal to reload
	t.Run("initialise stunnel subsystem", func(t *testing.T) {
		ptf := assert.PanicTestFunc(func() { Init(context.Background(), syscall.SIGHUP) })
		require.NotPanics(t, ptf)
	})

	// ensure now initialised
	t.Run("isInitialised", func(t *testing.T) {
		initialisedMu.RLock()
		t.Cleanup(func() { initialisedMu.RUnlock() })
		require.True(t, initialised)
	})

	t.Run("test handling of invalid cert/key", func(t *testing.T) {

		// get test name & remove path separator chars
		tName := strings.ReplaceAll(t.Name(), "/", "_")

		// make temp file for cert
		tmpCertFileName := fmt.Sprintf("bordercontrol-testing-%s-certfile-*", tName)
		tmpCertFile, err := os.CreateTemp("", tmpCertFileName)
		require.NoError(t, err)
		t.Cleanup(func() {
			tmpCertFile.Close()
			os.Remove(tmpCertFile.Name())
		})
		t.Logf("created temp certificate file: %s", tmpCertFile.Name())

		// make temp file for key
		tmpKeyFileName := fmt.Sprintf("bordercontrol-testing-%s-keyfile-*", tName)
		tmpKeyFile, err := os.CreateTemp("", tmpKeyFileName)
		require.NoError(t, err)
		t.Cleanup(func() {
			tmpKeyFile.Close()
			os.Remove(tmpKeyFile.Name())
		})
		t.Logf("created temp key file: %s", tmpKeyFile.Name())

		err = LoadCertAndKeyFromFile(tmpCertFile.Name(), tmpKeyFile.Name())
		require.Error(t, err)
		require.Equal(t, "tls: failed to find any PEM data in certificate input", err.Error())

	})

	t.Run("test handling of nonexistant cert/key files", func(t *testing.T) {

		// get test name & remove path separator chars
		tName := strings.ReplaceAll(t.Name(), "/", "_")

		// make temp file for cert
		tmpCertFileName := fmt.Sprintf("bordercontrol-testing-%s-certfile-*", tName)
		tmpCertFile, err := os.CreateTemp("", tmpCertFileName)
		require.NoError(t, err)
		tmpCertFile.Close()
		os.Remove(tmpCertFile.Name())

		// make temp file for key
		tmpKeyFileName := fmt.Sprintf("bordercontrol-testing-%s-keyfile-*", tName)
		tmpKeyFile, err := os.CreateTemp("", tmpKeyFileName)
		require.NoError(t, err)
		tmpKeyFile.Close()
		os.Remove(tmpKeyFile.Name())

		err = LoadCertAndKeyFromFile(tmpCertFile.Name(), tmpKeyFile.Name())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no such file or directory")
	})

	// test cert reload
	t.Run("test reload via signal", func(t *testing.T) {

		// get test name & remove path separator chars
		tName := strings.ReplaceAll(t.Name(), "/", "_")

		// make temp file for cert
		tmpCertFileName := fmt.Sprintf("bordercontrol-testing-%s-certfile-*", tName)
		tmpCertFile, err := os.CreateTemp("", tmpCertFileName)
		require.NoError(t, err)
		t.Cleanup(func() {
			tmpCertFile.Close()
			os.Remove(tmpCertFile.Name())
		})
		t.Logf("created temp certificate file: %s", tmpCertFile.Name())

		// make temp file for key
		tmpKeyFileName := fmt.Sprintf("bordercontrol-testing-%s-keyfile-*", tName)
		tmpKeyFile, err := os.CreateTemp("", tmpKeyFileName)
		require.NoError(t, err)
		t.Cleanup(func() {
			tmpKeyFile.Close()
			os.Remove(tmpKeyFile.Name())
		})
		t.Logf("created temp key file: %s", tmpKeyFile.Name())

		// generate test environment TLS Cert/Key
		t.Log("generating self signed TLS cert & key")
		err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
		require.NoError(t, err)

		// initialise keypair reloader
		t.Log("load TLS cert & key")
		err = LoadCertAndKeyFromFile(tmpCertFile.Name(), tmpKeyFile.Name())
		require.NoError(t, err)

		// get copy of original cert
		kpr.certMu.RLock()
		c1 := *kpr.cert
		kpr.certMu.RUnlock()

		// generate new test environment TLS Cert/Key
		t.Log("generating new self signed TLS cert & key")
		err = GenerateSelfSignedTLSCertAndKey(tmpKeyFile, tmpCertFile)
		require.NoError(t, err)

		// send signal
		t.Log("trigger cert/key reload")
		signalChan <- syscall.SIGHUP

		// wait for the channel to be read
		time.Sleep(time.Second)

		// get copy of new cert
		kpr.certMu.RLock()
		c2 := *kpr.cert
		kpr.certMu.RUnlock()

		// remove cert & key files
		t.Log("delete cert/key files")
		tmpCertFile.Close()
		os.Remove(tmpCertFile.Name())
		tmpKeyFile.Close()
		os.Remove(tmpKeyFile.Name())

		// send signal
		t.Log("trigger cert/key reload")
		signalChan <- syscall.SIGHUP

		// wait for the channel to be read
		time.Sleep(time.Second)

		// get copy of new new cert
		kpr.certMu.RLock()
		c3 := *kpr.cert
		kpr.certMu.RUnlock()

		t.Log("check in-memory certificates are as-expected")
		// ensure c1 & c2 different
		require.NotEqual(t, c1, c2)
		// ensure c2 & c3 are the same (as no files to reload, they were deleted)
		require.Equal(t, c2, c3)
	})

	t.Run("prepare for testing connections", func(t *testing.T) {
		err := PrepTestEnvironmentTLSCertAndKey()
		require.NoError(t, err)
	})

	var listener net.Listener
	var clientTlsConfig *tls.Config

	t.Run("prepare test environment listener", func(t *testing.T) {
		var err error
		listener, err = PrepTestEnvironmentTLSListener()
		require.NoError(t, err)
	})

	t.Run("prepare test environment client tls config", func(t *testing.T) {
		var err error
		clientTlsConfig, err = PrepTestEnvironmentTLSClientConfig(TestSNI.String())
		require.NoError(t, err)
	})

	t.Run("test client connection", func(t *testing.T) {

		testData := "Hello World!"
		stopListener := make(chan bool, 1)
		wg := sync.WaitGroup{}

		// server side
		wg.Add(1)
		go func(t *testing.T) {

			buf := make([]byte, len(testData))

			// accept one connection
			conn, err := listener.Accept()
			require.NoError(t, err)

			// read some data
			n, err := conn.Read(buf)
			require.NoError(t, err)

			// check TLS handshake completed
			require.True(t, HandshakeComplete(conn), "TLS handshake")

			// check SNI
			sni := GetSNI(conn)
			require.Equal(t, TestSNI.String(), sni)

			// check received data
			require.Equal(t, len(testData), n)
			require.Equal(t, testData, string(buf))

			// write some data
			n, err = conn.Write(buf)
			require.NoError(t, err)
			require.Equal(t, len(testData), n)

			// wait until we're ready to stop
			_ = <-stopListener
			conn.Close()
			wg.Done()
		}(t)

		// client side
		wg.Add(1)
		go func(t *testing.T) {

			buf := make([]byte, len(testData))

			// dial server
			conn, err := tls.Dial("tcp", listener.Addr().String(), clientTlsConfig)
			require.NoError(t, err)

			// write some data
			n, err := conn.Write([]byte(testData))
			require.NoError(t, err)
			require.Equal(t, len(testData), n)

			// read some data
			n, err = conn.Read(buf)
			require.NoError(t, err)
			require.Equal(t, len(testData), n)
			require.Equal(t, testData, string(buf))

			err = conn.Close()
			require.NoError(t, err)

			// stop listener
			stopListener <- true
			wg.Done()
		}(t)

		wg.Wait()
	})

	t.Run("Close", func(t *testing.T) {
		err := Close()
		require.NoError(t, err)
	})

}
