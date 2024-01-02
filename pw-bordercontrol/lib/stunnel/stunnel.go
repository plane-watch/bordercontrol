package stunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/net/nettest"
)

var (
	tlsConfig  tls.Config
	kpr        *keypairReloader
	signalChan chan os.Signal

	initialised   bool
	initialisedMu sync.RWMutex

	ErrNotInitialised = errors.New("TLS/SSL subsystem not initialised")
)

// struct for SSL cert/key (+ mutex for sync)
type keypairReloader struct {
	certMu   sync.RWMutex
	cert     *tls.Certificate
	certPath string
	keyPath  string
}

func Init(reloadSignal os.Signal) {
	// prepares channels to catch signal to reload TLS/SSL cert/key
	signalChan = make(chan os.Signal, 1)
	signal.Notify(signalChan, reloadSignal)

	initialisedMu.Lock()
	defer initialisedMu.Unlock()
	initialised = true
}

func LoadCertAndKeyFromFile(certFile, keyFile string) error {
	// loads certificate and key from file

	if !isInitialised() {
		return ErrNotInitialised
	}

	kpr, err := NewKeypairReloader(certFile, keyFile)
	if err != nil {
		return err
	}
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()
	return nil
}

func NewListener(network, laddr string) (l net.Listener, err error) {
	// returns a net listener to listen for incoming stunnel connections

	if !isInitialised() {
		return l, ErrNotInitialised
	}

	l, err = tls.Listen(
		network,
		laddr,
		&tlsConfig,
	)

	return l, err
}

func isInitialised() bool {
	// returns true if Init() has been called, else false
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}

func NewKeypairReloader(certPath, keyPath string) (*keypairReloader, error) {
	// for reloading SSL cert/key on SIGHUP. Stolen from: https://stackoverflow.com/questions/37473201/is-there-a-way-to-update-the-tls-certificates-in-a-net-http-server-without-any-d

	if !isInitialised() {
		return &keypairReloader{}, ErrNotInitialised
	}

	result := &keypairReloader{
		certPath: certPath,
		keyPath:  keyPath,
	}

	log := log.With().
		Strs("func", []string{"tls.go", "NewKeypairReloader"}).
		Str("certPath", certPath).
		Str("keyPath", keyPath).
		Logger()

	log.Info().Msg("loading TLS certificate and key")
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	result.cert = &cert
	go func() {
		for range signalChan {
			log.Info().Msg("received SIGHUP, reloading TLS certificate and key")
			if err := result.maybeReload(); err != nil {
				log.Err(err).Msg("error loading TLS certificate, continuing to use old TLS certificate")
			}
		}
	}()
	return result, nil
}

func (kpr *keypairReloader) maybeReload() error {
	// for reloading SSL cert/key on SIGHUP. Stolen from: https://stackoverflow.com/questions/37473201/is-there-a-way-to-update-the-tls-certificates-in-a-net-http-server-without-any-d
	newCert, err := tls.LoadX509KeyPair(kpr.certPath, kpr.keyPath)
	if err != nil {
		return err
	}
	kpr.certMu.Lock()
	defer kpr.certMu.Unlock()
	kpr.cert = &newCert
	return nil
}

func (kpr *keypairReloader) GetCertificateFunc() func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	// for reloading SSL cert/key on SIGHUP. Stolen from: https://stackoverflow.com/questions/37473201/is-there-a-way-to-update-the-tls-certificates-in-a-net-http-server-without-any-d
	return func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		kpr.certMu.RLock()
		defer kpr.certMu.RUnlock()
		return kpr.cert, nil
	}
}

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
	kpr, err := NewKeypairReloader(certFile.Name(), keyFile.Name())
	if err != nil {
		return err
	}
	tlsConfig.GetCertificate = kpr.GetCertificateFunc()
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
	return tls.Listen("tcp", tlsListenAddr, &tlsConfig)
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

func HandshakeComplete(c net.Conn) bool {
	return c.(*tls.Conn).ConnectionState().HandshakeComplete
}

func GetSNI(c net.Conn) string {
	return c.(*tls.Conn).ConnectionState().ServerName
}
