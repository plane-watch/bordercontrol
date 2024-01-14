package stunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"os/signal"
	"pw_bordercontrol/lib/nats_io"
	"sync"

	"github.com/rs/zerolog/log"
)

var (

	// tlsConfig contains tls.Config used to terminate stunnel connections
	tlsConfig tls.Config

	// kpr represents a keypairReloader, allowing for reload of cert & key files on signal
	kpr *keypairReloader

	// channel to receive cert & key reload signal
	ReloadSignalChan chan os.Signal

	// context for this module, allows for clean shutdown
	ctx context.Context

	// cancel function for ctx
	cancelCtx context.CancelFunc

	// waitgroup for signal catcher goroutine
	signalCatcherWg sync.WaitGroup

	// NATS subject to trigger cert/key reload
	natsSubjReloadCertKey = "pw_bordercontrol.stunnel.reloadcertkey"

	// set to true when Init() is run
	initialised bool

	// mutex for initialised
	initialisedMu sync.RWMutex

	// error: TLS/SSL subsystem not initialised
	ErrNotInitialised = errors.New("TLS/SSL subsystem not initialised")
)

// struct for SSL cert/key (+ mutex for sync)
type keypairReloader struct {
	certMu   sync.RWMutex
	cert     *tls.Certificate
	certPath string
	keyPath  string
}

// Init initialises the TLS/SSL subsystem to enable listening and accepting stunnel connections.
// Must be run during app initialisation, followed by LoadCertAndKeyFromFile.
func Init(parentContext context.Context, reloadSignal os.Signal) error {

	// prepares context
	ctx, cancelCtx = context.WithCancel(parentContext)

	// prep waitgroup for signal catching goroutine
	signalCatcherWg = sync.WaitGroup{}

	// prepares channels to catch signal to reload TLS/SSL cert/key
	ReloadSignalChan = make(chan os.Signal, 1)
	signal.Notify(ReloadSignalChan, reloadSignal)

	if nats_io.IsConnected() {
		err := nats_io.SignalSendOnSubj(natsSubjReloadCertKey, reloadSignal, ReloadSignalChan)
		if err != nil {
			log.Err(err).Str("subj", natsSubjReloadCertKey).Err(err).Msg("subscribe failed")
			return err
		}
	}

	initialisedMu.Lock()
	defer initialisedMu.Unlock()
	initialised = true

	log.Info().Msg("started stunnel subsystem")

	return nil
}

// Close shuts down the SSL/TLS subsystem. Should be run on application shutdown.
func Close() error {

	// only run if initialised
	if !isInitialised() {
		return ErrNotInitialised
	}

	// close signalChan so we will no longer perform cert/key reload on signal
	close(ReloadSignalChan)

	// cancel the context, so any goroutines will exit
	cancelCtx()

	// wait for goroutines to exit
	signalCatcherWg.Wait()

	// set initialised to false
	initialisedMu.Lock()
	defer initialisedMu.Unlock()
	initialised = false

	log.Info().Msg("stopped stunnel subsystem")

	return nil

}

// LoadCertAndKeyFromFile loads certificate and key from file.
// Should be run immediately after Init.
func LoadCertAndKeyFromFile(certFile, keyFile string) error {

	if !isInitialised() {
		return ErrNotInitialised
	}

	var err error
	kpr, err = newKeypairReloader(certFile, keyFile)
	if err != nil {
		return err
	}
	tlsConfig.GetCertificate = kpr.getCertificateFunc()
	return nil
}

// NewListener returns a net.Listener preconfigured with TLS settings to listen for incoming stunnel connections
func NewListener(network, laddr string) (l net.Listener, err error) {

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

// isInitialised returns true if Init() has been called, else false
func isInitialised() bool {
	initialisedMu.RLock()
	defer initialisedMu.RUnlock()
	return initialised
}

// newKeypairReloader handles loading TLS certificate and key from file (certPath, keyPath).
// It also starts a goroutine to handle reloading TLS cert & key receive from signalChan.
func newKeypairReloader(certPath, keyPath string) (*keypairReloader, error) {
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
	signalCatcherWg.Add(1)
	go func() {
		defer signalCatcherWg.Done()
		for {
			select {
			// if context cancelled, then end this goroutine
			case <-ctx.Done():
				log.Info().Msg("shutting down TLS certificate and key reloader")
				return
			// if signal caught, then reload certificates if they exist
			case <-ReloadSignalChan:
				log.Info().Msg("reloading TLS certificate and key")
				if err := result.maybeReload(); err != nil {
					log.Err(err).Msg("error loading TLS certificate, continuing to use old TLS certificate")
				}
			}
		}
	}()
	return result, nil
}

// maybeReload attempts to reload the TLS certificate & file. On error, will continue to use existing cert & key.
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

// getCertificateFunc provides a function for tls.Config{}.GetCertificate
func (kpr *keypairReloader) getCertificateFunc() func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	// for reloading SSL cert/key on SIGHUP. Stolen from: https://stackoverflow.com/questions/37473201/is-there-a-way-to-update-the-tls-certificates-in-a-net-http-server-without-any-d
	return func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		kpr.certMu.RLock()
		defer kpr.certMu.RUnlock()
		return kpr.cert, nil
	}
}

// HandshakeComplete returns true of a connection's TLS handshake has completed successfully
func HandshakeComplete(c net.Conn) bool {
	return c.(*tls.Conn).ConnectionState().HandshakeComplete
}

// GetSNI returns the contents of the SNI field in the TLS handshake (used for feeder authentication - should contain API Key)
func GetSNI(c net.Conn) string {
	return c.(*tls.Conn).ConnectionState().ServerName
}
