package stunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"pw_bordercontrol/lib/nats_io"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
)

func TestWrapperNatsioSignalSendOnSubj(t *testing.T) {
	ch := make(chan os.Signal)
	defer close(ch)
	err := wrapperNatsioSignalSendOnSubj("test", os.Interrupt, ch)
	require.Error(t, err)
	assert.ErrorIs(t, err, nats_io.ErrNotInitialised)
}

func TestStunnel(t *testing.T) {

	GenerateSelfSignedTLSCertAndKey := func(keyFile, certFile *os.File) error {
		// Thanks to: https://go.dev/src/crypto/tls/generate_cert.go

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

	// prepares self-signed server certificate for testing

	// prep cert file
	certFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_cert.pem")
	require.NoError(t, err, "error creating temp file for self signed ssl cert")
	t.Logf("created temp file for ssl cert: %s", certFile.Name())
	defer func() {
		certFile.Close()
		os.Remove(certFile.Name())
	}()

	// prep key file
	keyFile, err := os.CreateTemp("", "bordercontrol_unit_testing_*_key.pem")
	require.NoError(t, err, "error creating temp file for self signed ssl key")
	t.Logf("created temp file for ssl key: %s", keyFile.Name())
	defer func() {
		keyFile.Close()
		os.Remove(keyFile.Name())
	}()

	// generate cert/key for testing
	err = GenerateSelfSignedTLSCertAndKey(keyFile, certFile)
	require.NoError(t, err, "error creating self signed ssl cert/key")
	t.Logf("generated self-signed ssl cert/key")

	t.Run("NewServer/invalid_cert_key_files", func(t *testing.T) {
		_, err := NewServer("", "", syscall.SIGHUP)
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("NewServer/working", func(t *testing.T) {
		s, err := NewServer(certFile.Name(), keyFile.Name(), syscall.SIGHUP)
		require.NoError(t, err)
		err = s.Close()
		require.NoError(t, err)
	})

	t.Run("ReloadCertOnNatsMsg/error", func(t *testing.T) {
		s, err := NewServer(certFile.Name(), keyFile.Name(), syscall.SIGHUP)
		require.NoError(t, err)
		defer s.Close()
		err = s.ReloadCertOnNatsMsg("pw_bordercontrol.stunnel.reloadcertkey")
		require.Error(t, err)
		assert.ErrorIs(t, err, nats_io.ErrNotInitialised)
	})

	t.Run("ReloadCertOnNatsMsg/working", func(t *testing.T) {
		s, err := NewServer(certFile.Name(), keyFile.Name(), syscall.SIGHUP)
		require.NoError(t, err)
		defer s.Close()

		// override wrapper function for testing
		origWrapperNatsioSignalSendOnSubj := wrapperNatsioSignalSendOnSubj
		defer func() {
			wrapperNatsioSignalSendOnSubj = origWrapperNatsioSignalSendOnSubj
		}()
		wrapperNatsioSignalSendOnSubj = func(subj string, sig os.Signal, ch chan os.Signal) error {
			return nil
		}

		err = s.ReloadCertOnNatsMsg("test")
		require.NoError(t, err)
	})

	t.Run("NewListener/working", func(t *testing.T) {

		var wg sync.WaitGroup

		testData := []byte("Hello World!")
		testUUID := uuid.New()

		// start server
		s, err := NewServer(certFile.Name(), keyFile.Name(), syscall.SIGHUP)
		require.NoError(t, err)
		defer s.Close()

		// get local listener addr
		nl, err := nettest.NewLocalListener("tcp")
		require.NoError(t, err)
		nl.Close()

		// start tcp listener
		tlsListener, err := s.NewListener("tcp", nl.Addr().String())
		require.NoError(t, err)
		defer tlsListener.Close()
		assert.Equal(t, nl.Addr().String(), tlsListener.Addr().String())

		// handle incoming connection
		wg.Add(1)
		go func(t *testing.T) {
			defer wg.Done()
			t.Log("server: waiting to accept connection")
			svrConn, err := tlsListener.Accept()
			require.NoError(t, err)
			t.Log("server: connection accepted")
			defer svrConn.Close()

			// read
			t.Log("server: reading data")
			b := make([]byte, 1024)
			n, err := svrConn.Read(b)
			require.NoError(t, err)
			t.Log("server: read OK")
			assert.Equal(t, n, len(testData))

			time.Sleep(time.Second)

			assert.True(t, HandshakeComplete(svrConn))
			assert.Equal(t, testUUID.String(), GetSNI(svrConn))
			t.Log("server: completed")
		}(t)

		// attempt connection
		tlsConfig := tls.Config{}
		tlsConfig.ServerName = testUUID.String()
		tlsConfig.InsecureSkipVerify = true
		t.Log("client: dialling server")
		cliConn, err := tls.Dial("tcp", tlsListener.Addr().String(), &tlsConfig)
		require.NoError(t, err)
		defer cliConn.Close()

		// write
		wg.Add(1)
		go func(t *testing.T) {
			defer wg.Done()
			t.Log("client: writing data")
			n, err := cliConn.Write(testData)
			require.NoError(t, err)
			t.Log("client: write OK")
			assert.Equal(t, n, len(testData))
		}(t)

		// wait for goroutines
		wg.Wait()

	})

	t.Run("newKeypairReloader/cert_reload_fail", func(t *testing.T) {
		// start server
		s, err := NewServer(certFile.Name(), keyFile.Name(), syscall.SIGHUP)
		require.NoError(t, err)
		defer s.Close()

		// delete cert & key files to induce error
		certFile.Close()
		os.Remove(certFile.Name())
		keyFile.Close()
		os.Remove(keyFile.Name())

		// request reload
		s.reloadSignalChan <- s.reloadSignal

		time.Sleep(time.Second)
	})
}
