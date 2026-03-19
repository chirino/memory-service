//go:build !nouds

package serve

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/urfave/cli/v3"
)

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

func udsListenerFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "unix-socket",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_UNIX_SOCKET"),
			Destination: &cfg.Listener.UnixSocket,
			Usage:       "Absolute path to a Unix socket for the HTTP/gRPC server",
		},
		&cli.StringFlag{
			Name:        "management-unix-socket",
			Category:    "Network Listener: Management:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_UNIX_SOCKET"),
			Destination: &cfg.ManagementListener.UnixSocket,
			Usage:       "Absolute path to a Unix socket for the management server",
		},
	}
}
