package serve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"

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
	return prepareTCPListener(cfg)
}
