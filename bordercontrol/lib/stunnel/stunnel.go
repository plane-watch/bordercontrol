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

	// NATS subject to trigger cert/key reload
	NatsSubjReloadCertKey = "pw_bordercontrol.stunnel.reloadcertkey"

	// error: TLS/SSL subsystem not initialised
	ErrNotInitialised = errors.New("TLS/SSL subsystem not initialised")

	// wrapper functions that can be overridden for testing
	wrapperNatsioSignalSendOnSubj = func(subj string, sig os.Signal, ch chan os.Signal) error {
		return nats_io.SignalSendOnSubj(subj, sig, ch)
	}
)

// struct for SSL cert/key (+ mutex for sync)
type keypairReloader struct {
	certMu   sync.RWMutex
	cert     *tls.Certificate
	certPath string
	keyPath  string
}

type Server struct {

	// server context
	ctx context.Context

	// cancel function for server context
	cancel context.CancelFunc

	// path to certificate file
	certFile string

	// path to private key file
	keyFile string

	// tlsConfig contains tls.Config used to terminate stunnel connections
	tlsConfig tls.Config

	// kpr represents a keypairReloader, allowing for reload of cert & key files on signal
	kpr *keypairReloader

	// channel to receive cert & key reload signal
	reloadSignalChan chan os.Signal

	// waitgroup for signal catcher goroutine
	signalCatcherWg sync.WaitGroup

	// type of signal to trigger certificate reload
	reloadSignal os.Signal
}

func NewServer(certFile, keyFile string, reloadSignal os.Signal) (*Server, error) {
	return NewServerWithContext(context.Background(), certFile, keyFile, reloadSignal)
}

func NewServerWithContext(ctx context.Context, certFile, keyFile string, reloadSignal os.Signal) (*Server, error) {

	var err error

	s := &Server{
		certFile:     certFile,
		keyFile:      keyFile,
		reloadSignal: reloadSignal,
	}

	// prep context
	s.ctx, s.cancel = context.WithCancel(ctx)

	// prepares channel to catch signal to reload TLS/SSL cert/key
	s.reloadSignalChan = make(chan os.Signal, 1)
	signal.Notify(s.reloadSignalChan, s.reloadSignal)

	// load cert & key from file
	err = s.loadCertAndKeyFromFile(s.certFile, s.keyFile)
	if err != nil {
		return &Server{}, err
	}

	log.Info().Msg("started stunnel subsystem")

	return s, nil
}

func (s *Server) Close() error {
	// close signalChan so we will no longer perform cert/key reload on signal
	close(s.reloadSignalChan)

	// cancel the context, so any goroutines will exit
	s.cancel()

	// wait for goroutines to exit
	s.signalCatcherWg.Wait()

	log.Info().Msg("started stunnel subsystem")

	return nil
}

func (s *Server) ReloadCertOnNatsMsg(natsSubject string) error {
	err := wrapperNatsioSignalSendOnSubj(natsSubject, s.reloadSignal, s.reloadSignalChan)
	if err != nil {
		log.Err(err).Str("subj", natsSubject).Err(err).Msg("subscribe failed")
		return err
	}
	return nil
}

// LoadCertAndKeyFromFile loads certificate and key from file.
// Should be run immediately after Init.
func (s *Server) loadCertAndKeyFromFile(certFile, keyFile string) error {

	var err error

	s.kpr, err = s.newKeypairReloader(certFile, keyFile)
	if err != nil {
		return err
	}
	s.tlsConfig.GetCertificate = s.kpr.getCertificateFunc()
	return nil
}

// NewListener returns a net.Listener preconfigured with TLS settings to listen for incoming stunnel connections
func (s *Server) NewListener(network, laddr string) (net.Listener, error) {
	return tls.Listen(
		network,
		laddr,
		&s.tlsConfig,
	)
}

// newKeypairReloader handles loading TLS certificate and key from file (certPath, keyPath).
// It also starts a goroutine to handle reloading TLS cert & key receive from signalChan.
func (s *Server) newKeypairReloader(certPath, keyPath string) (*keypairReloader, error) {
	// for reloading SSL cert/key on SIGHUP. Stolen from: https://stackoverflow.com/questions/37473201/is-there-a-way-to-update-the-tls-certificates-in-a-net-http-server-without-any-d

	result := &keypairReloader{
		certPath: certPath,
		keyPath:  keyPath,
	}

	log := log.With().
		Str("certPath", certPath).
		Str("keyPath", keyPath).
		Logger()

	log.Info().Msg("loading TLS certificate and key")
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	result.cert = &cert
	s.signalCatcherWg.Add(1)
	go func() {
		defer s.signalCatcherWg.Done()
		for {
			select {
			// if context cancelled, then end this goroutine
			case <-s.ctx.Done():
				log.Info().Msg("shutting down TLS certificate and key reloader")
				return
			// if signal caught, then reload certificates if they exist
			case <-s.reloadSignalChan:
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
