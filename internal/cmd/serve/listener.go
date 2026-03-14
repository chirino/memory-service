package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"google.golang.org/grpc"
)

type RunningServers struct {
	Addr            net.Addr
	Port            int
	Endpoint        string
	Network         string
	HTTPServerPlain *http.Server
	HTTPServerTLS   *http.Server
	GRPCServer      *grpc.Server
	Close           func(ctx context.Context) error
}

type PreparedListener struct {
	Listener net.Listener
	Network  string
	Address  string
	Cleanup  func() error
}

type listenerSelections struct {
	mainPortExplicit       bool
	mainUnixSocketExplicit bool
	mgmtPortExplicit       bool
	mgmtUnixSocketExplicit bool
}

func validateListenerSelections(cfg config.Config, selections listenerSelections) error {
	if selections.mainPortExplicit && selections.mainUnixSocketExplicit {
		return fmt.Errorf("--port and --unix-socket are mutually exclusive")
	}
	if selections.mgmtPortExplicit && selections.mgmtUnixSocketExplicit {
		return fmt.Errorf("--management-port and --management-unix-socket are mutually exclusive")
	}
	if socket := strings.TrimSpace(cfg.Listener.UnixSocket); socket != "" && !filepath.IsAbs(socket) {
		return fmt.Errorf("--unix-socket must be an absolute path")
	}
	if socket := strings.TrimSpace(cfg.ManagementListener.UnixSocket); socket != "" && !filepath.IsAbs(socket) {
		return fmt.Errorf("--management-unix-socket must be an absolute path")
	}
	return nil
}

func prepareListener(cfg config.ListenerConfig) (*PreparedListener, error) {
	if socketPath := strings.TrimSpace(cfg.UnixSocket); socketPath != "" {
		return prepareUnixListener(socketPath)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, err
	}
	return &PreparedListener{
		Listener: listener,
		Network:  "tcp",
		Address:  listener.Addr().String(),
		Cleanup:  func() error { return nil },
	}, nil
}

func prepareUnixListener(socketPath string) (*PreparedListener, error) {
	parent := filepath.Dir(socketPath)
	createdParent := false

	info, err := os.Stat(parent)
	switch {
	case err == nil:
		if !info.IsDir() {
			return nil, fmt.Errorf("unix socket parent %q is not a directory", parent)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("unix socket parent %q must not be group/world accessible", parent)
		}
	case errors.Is(err, os.ErrNotExist):
		if mkErr := os.MkdirAll(parent, 0o700); mkErr != nil {
			return nil, fmt.Errorf("create unix socket parent %q: %w", parent, mkErr)
		}
		if chmodErr := os.Chmod(parent, 0o700); chmodErr != nil {
			return nil, fmt.Errorf("secure unix socket parent %q: %w", parent, chmodErr)
		}
		createdParent = true
	default:
		return nil, fmt.Errorf("stat unix socket parent %q: %w", parent, err)
	}

	if err := removeStaleSocket(socketPath); err != nil {
		return nil, err
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		if createdParent {
			_ = os.Remove(parent)
		}
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, fmt.Errorf("secure unix socket %q: %w", socketPath, err)
	}

	var cleanupOnce sync.Once
	cleanup := func() error {
		var cleanupErr error
		cleanupOnce.Do(func() {
			if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = err
			}
		})
		return cleanupErr
	}

	return &PreparedListener{
		Listener: listener,
		Network:  "unix",
		Address:  socketPath,
		Cleanup:  cleanup,
	}, nil
}

func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat unix socket %q: %w", socketPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("unix socket path %q already exists and is not a socket", socketPath)
	}

	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("unix socket %q is already in use", socketPath)
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale unix socket %q: %w", socketPath, err)
	}
	return nil
}
