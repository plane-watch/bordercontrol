package main

import (
	"crypto/tls"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

var certExpiryDate time.Time

// struct for SSL cert/key (+ mutex for sync)
type keypairReloader struct {
	certMu   sync.RWMutex
	cert     *tls.Certificate
	certPath string
	keyPath  string
}

func NewKeypairReloader(certPath, keyPath string) (*keypairReloader, error) {
	// for reloading SSL cert/key on SIGHUP. Stolen from: https://stackoverflow.com/questions/37473201/is-there-a-way-to-update-the-tls-certificates-in-a-net-http-server-without-any-d
	result := &keypairReloader{
		certPath: certPath,
		keyPath:  keyPath,
	}
	log.Info().Str("cert", certPath).Str("key", keyPath).Msg("loading TLS certificate and key")
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	result.cert = &cert
	certExpiryDate = result.cert.Leaf.NotAfter
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGHUP)
		for range c {
			log.Info().Str("cert", certPath).Str("key", keyPath).Msg("Received SIGHUP, reloading TLS certificate and key")
			if err := result.maybeReload(); err != nil {
				log.Err(err).Msg("Keeping old TLS certificate because the new one could not be loaded")
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
	certExpiryDate = kpr.cert.Leaf.NotAfter
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
