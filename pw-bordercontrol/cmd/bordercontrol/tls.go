package main

import (
	"crypto/tls"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
)

// struct for SSL cert/key (+ mutex for sync)
type keypairReloader struct {
	certMu   sync.RWMutex
	cert     *tls.Certificate
	certPath string
	keyPath  string
}

func NewKeypairReloader(certPath, keyPath string, sigChan chan os.Signal) (*keypairReloader, error) {
	// for reloading SSL cert/key on SIGHUP. Stolen from: https://stackoverflow.com/questions/37473201/is-there-a-way-to-update-the-tls-certificates-in-a-net-http-server-without-any-d

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
		for range sigChan {
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
