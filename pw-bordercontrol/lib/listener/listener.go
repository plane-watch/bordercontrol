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
	// allow override of these functions to simplify testing
	stunnelNewListenerWrapper = func(network string, laddr string) (l net.Listener, err error) {
		return stunnel.NewListener(network, laddr)
	}
	proxyConnStartWrapper = func(f *feedproxy.ProxyConnection, ctx context.Context) error {
		return f.Start(ctx)
	}
	feedproxyGetConnectionNumberWrapper = func() (num uint, err error) {
		return feedproxy.GetConnectionNumber()
	}
)

func NewListener(listenAddr string, proto feedprotocol.Protocol, feedInContainerPrefix string, InnerConnectionPort int) (*listener, error) {
	// prep listener config
	ip := net.ParseIP(strings.Split(listenAddr, ":")[0])
	if ip == nil {
		ip = net.IPv4(0, 0, 0, 0) // 0.0.0.0
	}
	port, err := strconv.Atoi(strings.Split(listenAddr, ":")[1])
	if err != nil {
		return &listener{}, err
	}
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

type listener struct {
	Protocol              feedprotocol.Protocol // Protocol handled by the listener
	ListenAddr            net.TCPAddr           // TCP address to listen on for incoming stunnel'd BEAST connections
	FeedInContainerPrefix string
	InnerConnectionPort   int
}

func (l *listener) Run(ctx context.Context) error {
	// incoming connection listener

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
