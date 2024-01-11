package listener

import (
	"context"
	"errors"
	"fmt"
	"net"
	"pw_bordercontrol/lib/feedprotocol"
	"pw_bordercontrol/lib/feedproxy"
	"pw_bordercontrol/lib/stunnel"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

var (

	// stunnelNewListenerWrapper is a wrapper for stunnel.NewListener.
	// Variableised to allow overwriting for testing.
	stunnelNewListenerWrapper = func(network string, laddr string) (l net.Listener, err error) {
		return stunnel.NewListener(network, laddr)
	}

	// proxyConnStartWrapper is a wrapper for *feedproxy.Start().
	// Variableised to allow overwriting for testing.
	proxyConnStartWrapper = func(f *feedproxy.ProxyConnection, ctx context.Context) error {
		return f.Start(ctx)
	}

	// feedproxyGetConnectionNumberWrapper is a wrapper for feedproxy.GetConnectionNumber().
	// Variableised to allow overwriting for testing.
	feedproxyGetConnectionNumberWrapper = func() (num uint, err error) {
		return feedproxy.GetConnectionNumber()
	}
)

// NewListener returns a listener struct that can be Run() to start listening for incoming connections.
func NewListener(listenAddr string, proto feedprotocol.Protocol, feedInContainerPrefix string, InnerConnectionPort int) (*listener, error) {
	// get IP address from listenAddr
	ip := net.ParseIP(strings.Split(listenAddr, ":")[0])
	if ip == nil {
		ip = net.IPv4(0, 0, 0, 0) // 0.0.0.0
	}
	// get port from listenAddr
	port, err := strconv.Atoi(strings.Split(listenAddr, ":")[1])
	if err != nil {
		return &listener{}, err
	}
	// return listener struct
	return &listener{
		Protocol: proto,
		ListenAddr: net.TCPAddr{
			IP:   ip,
			Port: port,
			Zone: "",
		},
		FeedInContainerPrefix: feedInContainerPrefix,
		InnerConnectionPort:   InnerConnectionPort,
	}, nil
}

// listener represents the configuration for a proxy service
type listener struct {

	// Protocol handled by the listener
	Protocol feedprotocol.Protocol

	// TCP address to listen on for incoming stunnel connections
	ListenAddr net.TCPAddr

	// Prefix for feed-in containers to be created.
	FeedInContainerPrefix string

	// Port to connect to on feed-in container or MLAT server.
	InnerConnectionPort int
}

// Run starts a listener listening for incoming connections
func (l *listener) Run(ctx context.Context) error {

	// waitgroup used for clean exit of goroutines
	wg := sync.WaitGroup{}

	// get protocol name
	protoName, err := feedprotocol.GetName(l.Protocol)
	if err != nil {
		return err
	}

	// update log context
	log := log.With().
		Str("proto", protoName).
		Int("port", l.ListenAddr.Port).
		Logger()

	// start TLS server
	log.Info().Msg("starting listener")
	stunnelListener, err := stunnelNewListenerWrapper(
		"tcp",
		fmt.Sprintf("%s:%d", l.ListenAddr.IP.String(), l.ListenAddr.Port),
	)
	if err != nil {
		log.Err(err).Msg("error staring listener")
		return err
	}
	defer stunnelListener.Close()

	// handle context closure
	go func() {
		// wait for context closure
		_ = <-ctx.Done()
		// let user know what's happenning
		log.Info().Msg("shutting down listener")
		// close stunnelListener, which will cause any .Accept() to throw net.ErrClosed
		err := stunnelListener.Close()
		if err != nil {
			log.Err(err).Msg("error closing listener")
		}
	}()

	// handle incoming connections until stunnelListener is closed (or has other error)
	for {

		// accept incoming connection
		conn, err := stunnelListener.Accept()
		if errors.Is(err, net.ErrClosed) {
			// if network connection has been closed, then ctx has likely been cancelled, meaning we should quit
			// break out of for loop so we can wait for connections to close
			break
		} else if err != nil {
			log.Err(err).Msg("error accepting connection")
			continue
		}

		// prep proxy config
		connNum, err := feedproxyGetConnectionNumberWrapper()
		if err != nil {
			log.Err(err).Msg("could not get connection number")
			continue
		}
		proxyConn := feedproxy.ProxyConnection{
			Connection:                  conn,
			ConnectionProtocol:          l.Protocol,
			ConnectionNumber:            connNum,
			FeedInContainerPrefix:       l.FeedInContainerPrefix,
			Logger:                      log,
			FeederValidityCheckInterval: time.Second * 60,
			InnerConnectionPort:         l.InnerConnectionPort,
		}

		// initiate proxying of the connection
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := proxyConnStartWrapper(&proxyConn, ctx)
			if err != nil {
				log.Err(err).
					Str("proto", proxyConn.ConnectionProtocol.Name()).
					Str("src", conn.RemoteAddr().String()).
					Uint("connnum", connNum).
					Msg("error proxying connection")
			}
		}()

	}

	// wait for connections to close
	wg.Wait()
	return nil
}
