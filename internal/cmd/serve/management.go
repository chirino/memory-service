package serve

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/soheilhy/cmux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// startManagementServer starts a dedicated HTTP-only server for management endpoints
// (health, metrics). It reuses config.ListenerConfig for plain-text/TLS options but has no gRPC.
// Returns the bound address and a shutdown function.
func startManagementServer(cfg config.ListenerConfig, handler http.Handler) (net.Addr, func(context.Context) error, error) {
	if !cfg.EnablePlainText && !cfg.EnableTLS {
		cfg.EnablePlainText = true
	}
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = 5 * time.Second
	}

	baseLis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, nil, fmt.Errorf("management listen failed: %w", err)
	}

	muxer := cmux.New(baseLis)

	var tlsLis net.Listener
	if cfg.EnableTLS {
		tlsLis = muxer.Match(cmux.TLS())
	}
	var plainLis net.Listener
	if cfg.EnablePlainText {
		plainLis = muxer.Match(cmux.Any())
	}

	var plainServer *http.Server
	if cfg.EnablePlainText {
		plainServer = &http.Server{
			Handler:           h2c.NewHandler(handler, &http2.Server{}),
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		}
		go func() {
			if err := plainServer.Serve(plainLis); err != nil && err != http.ErrServerClosed {
				log.Error("management plaintext server failed", "err", err)
			}
		}()
	}

	var tlsServer *http.Server
	if cfg.EnableTLS {
		cert, err := loadServerCertificate(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			_ = baseLis.Close()
			return nil, nil, err
		}
		tlsWrapped := tls.NewListener(tlsLis, &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
			MinVersion:   tls.VersionTLS12,
		})
		tlsServer = &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		}
		go func() {
			if err := tlsServer.Serve(tlsWrapped); err != nil && err != http.ErrServerClosed {
				log.Error("management tls server failed", "err", err)
			}
		}()
	}

	go func() {
		if err := muxer.Serve(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			log.Error("management mux failed", "err", err)
		}
	}()

	var closeOnce sync.Once
	closeFn := func(ctx context.Context) error {
		var shutdownErr error
		closeOnce.Do(func() {
			if plainServer != nil {
				if err := plainServer.Shutdown(ctx); err != nil && err != context.Canceled {
					shutdownErr = err
				}
			}
			if tlsServer != nil {
				if err := tlsServer.Shutdown(ctx); err != nil && err != context.Canceled && shutdownErr == nil {
					shutdownErr = err
				}
			}
			_ = baseLis.Close()
		})
		return shutdownErr
	}

	log.Info("Management server listening", "addr", baseLis.Addr())
	return baseLis.Addr(), closeFn, nil
}
