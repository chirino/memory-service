package serve

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
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
	"google.golang.org/grpc"
)

type RunningServers struct {
	Addr            net.Addr
	Port            int
	HTTPServerPlain *http.Server
	HTTPServerTLS   *http.Server
	GRPCServer      *grpc.Server
	Close           func(ctx context.Context) error
}

func StartSinglePortHTTPAndGRPC(
	_ context.Context,
	cfg config.ListenerConfig,
	httpHandler http.Handler,
	grpcServer *grpc.Server,
) (*RunningServers, error) {
	if !cfg.EnablePlainText && !cfg.EnableTLS {
		return nil, fmt.Errorf("single-port configuration requires plaintext and/or tls enabled")
	}
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = 5 * time.Second
	}

	baseLis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("single-port listen failed: %w", err)
	}

	dispatch := grpcOrHTTPHandler(grpcServer, httpHandler)
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
			Handler:           h2c.NewHandler(dispatch, &http2.Server{}),
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		}
		go func() {
			if err := plainServer.Serve(plainLis); err != nil && err != http.ErrServerClosed {
				log.Error("single-port plaintext server failed", "err", err)
			}
		}()
	}

	var tlsServer *http.Server
	if cfg.EnableTLS {
		cert, err := loadServerCertificate(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			_ = baseLis.Close()
			return nil, err
		}

		tlsWrapped := tls.NewListener(tlsLis, &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
			MinVersion:   tls.VersionTLS12,
		})
		tlsServer = &http.Server{
			Handler:           dispatch,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		}
		go func() {
			if err := tlsServer.Serve(tlsWrapped); err != nil && err != http.ErrServerClosed {
				log.Error("single-port tls server failed", "err", err)
			}
		}()
	}

	go func() {
		if err := muxer.Serve(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			log.Error("single-port mux failed", "err", err)
		}
	}()

	port := 0
	if tcpAddr, ok := baseLis.Addr().(*net.TCPAddr); ok {
		port = tcpAddr.Port
	}

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

			done := make(chan struct{})
			go func() {
				grpcServer.GracefulStop()
				close(done)
			}()
			select {
			case <-done:
			case <-ctx.Done():
				grpcServer.Stop()
			}

			_ = baseLis.Close()
		})
		return shutdownErr
	}

	return &RunningServers{
		Addr:            baseLis.Addr(),
		Port:            port,
		HTTPServerPlain: plainServer,
		HTTPServerTLS:   tlsServer,
		GRPCServer:      grpcServer,
		Close:           closeFn,
	}, nil
}

func grpcOrHTTPHandler(grpcServer *grpc.Server, httpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := strings.ToLower(r.Header.Get("Content-Type"))
		if r.ProtoMajor == 2 && strings.HasPrefix(contentType, "application/grpc") {
			grpcServer.ServeHTTP(w, r)
			return
		}
		httpHandler.ServeHTTP(w, r)
	})
}

func loadServerCertificate(certFile, keyFile string) (tls.Certificate, error) {
	if strings.TrimSpace(certFile) != "" && strings.TrimSpace(keyFile) != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("failed to load tls certificate: %w", err)
		}
		return cert, nil
	}
	return generateSelfSignedCertificate()
}

func generateSelfSignedCertificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate tls key failed: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate tls serial failed: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate tls certificate failed: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        template,
	}, nil
}
