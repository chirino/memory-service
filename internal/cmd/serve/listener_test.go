//go:build !nouds

package serve

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	grpcserver "github.com/chirino/memory-service/internal/grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestValidateListenerSelections(t *testing.T) {
	t.Run("allows unix socket with default port when port not explicitly set", func(t *testing.T) {
		cfg := configFixture()
		cfg.Listener.UnixSocket = "/tmp/memory-service.sock"

		err := validateListenerSelections(cfg, listenerSelections{
			mainUnixSocketExplicit: true,
		})
		require.NoError(t, err)
	})

	t.Run("rejects explicitly setting port and unix socket", func(t *testing.T) {
		cfg := configFixture()
		cfg.Listener.UnixSocket = "/tmp/memory-service.sock"

		err := validateListenerSelections(cfg, listenerSelections{
			mainPortExplicit:       true,
			mainUnixSocketExplicit: true,
		})
		require.EqualError(t, err, "--port and --unix-socket are mutually exclusive")
	})

	t.Run("rejects relative unix socket path", func(t *testing.T) {
		cfg := configFixture()
		cfg.Listener.UnixSocket = "memory-service.sock"

		err := validateListenerSelections(cfg, listenerSelections{
			mainUnixSocketExplicit: true,
		})
		require.EqualError(t, err, "--unix-socket must be an absolute path")
	})
}

func TestPrepareUnixListenerCreatesSecureDirectoryAndSocket(t *testing.T) {
	root := shortSocketRoot(t)
	socketPath := filepath.Join(root, "nested", "memservice.sock")

	prepared, err := prepareListener(config.ListenerConfig{UnixSocket: socketPath})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = prepared.Listener.Close()
		_ = prepared.Cleanup()
	})

	parentInfo, err := os.Stat(filepath.Dir(socketPath))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), parentInfo.Mode().Perm())

	socketInfo, err := os.Stat(socketPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), socketInfo.Mode().Perm())
	require.Equal(t, "unix", prepared.Network)
	require.Equal(t, socketPath, prepared.Address)
}

func TestPrepareUnixListenerRejectsInsecureExistingParent(t *testing.T) {
	root := shortSocketRoot(t)
	parent := filepath.Join(root, "existing")
	require.NoError(t, os.Mkdir(parent, 0o755))
	require.NoError(t, os.Chmod(parent, 0o755))

	_, err := prepareListener(config.ListenerConfig{UnixSocket: filepath.Join(parent, "memservice.sock")})
	require.ErrorContains(t, err, "must not be group/world accessible")
}

func TestPrepareUnixListenerRejectsRegularFile(t *testing.T) {
	root := shortSocketRoot(t)
	parent := filepath.Join(root, "secure")
	require.NoError(t, os.Mkdir(parent, 0o700))
	socketPath := filepath.Join(parent, "memservice.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("not a socket"), 0o600))

	_, err := prepareListener(config.ListenerConfig{UnixSocket: socketPath})
	require.ErrorContains(t, err, "already exists and is not a socket")
}

func TestPrepareUnixListenerRemovesStaleSocket(t *testing.T) {
	root := shortSocketRoot(t)
	parent := filepath.Join(root, "secure")
	require.NoError(t, os.Mkdir(parent, 0o700))
	socketPath := filepath.Join(parent, "memservice.sock")

	stale, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	require.NoError(t, stale.Close())

	prepared, err := prepareListener(config.ListenerConfig{UnixSocket: socketPath})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = prepared.Listener.Close()
		_ = prepared.Cleanup()
	})

	_, err = os.Stat(socketPath)
	require.NoError(t, err)
}

func TestPreparedUnixListenerCleanupRemovesSocket(t *testing.T) {
	root := shortSocketRoot(t)
	parent := filepath.Join(root, "secure")
	require.NoError(t, os.Mkdir(parent, 0o700))
	socketPath := filepath.Join(parent, "memservice.sock")

	prepared, err := prepareListener(config.ListenerConfig{UnixSocket: socketPath})
	require.NoError(t, err)

	require.NoError(t, prepared.Listener.Close())
	require.NoError(t, prepared.Cleanup())
	_, err = os.Stat(socketPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStartSinglePortHTTPAndGRPCOverUnixSocket(t *testing.T) {
	root := shortSocketRoot(t)
	socketPath := filepath.Join(root, "api", "memservice.sock")

	httpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	})

	grpcServer := grpc.NewServer()
	pb.RegisterSystemServiceServer(grpcServer, &grpcserver.SystemServer{Config: &config.Config{}})

	running, err := StartSinglePortHTTPAndGRPC(context.Background(), config.ListenerConfig{
		UnixSocket:      socketPath,
		EnablePlainText: true,
	}, httpHandler, grpcServer)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, running.Close(ctx))
	}()

	resp, err := unixHTTPClient(socketPath).Get("http://unix/ready")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	conn, err := grpc.NewClient(
		"passthrough:///unix",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		}),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()

	client := pb.NewSystemServiceClient(conn)
	health, err := client.GetHealth(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, "ok", health.GetStatus())

	require.Equal(t, "unix", running.Network)
	require.Equal(t, socketPath, running.Endpoint)
	require.Zero(t, running.Port)
}

func TestStartManagementServerOverUnixSocket(t *testing.T) {
	root := shortSocketRoot(t)
	socketPath := filepath.Join(root, "mgmt", "metrics.sock")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("metrics"))
			return
		}
		http.NotFound(w, r)
	})

	_, closeFn, err := startManagementServer(config.ListenerConfig{
		UnixSocket:      socketPath,
		EnablePlainText: true,
	}, handler)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, closeFn(ctx))
	}()

	resp, err := unixHTTPClient(socketPath).Get("http://unix/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func unixHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2: false,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
			TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
		},
	}
}

func configFixture() config.Config {
	cfg := config.DefaultConfig()
	return cfg
}

func shortSocketRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "mss-*")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(root, 0o700))
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
