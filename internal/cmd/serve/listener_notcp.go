//go:build notcp

package serve

import (
	"fmt"

	"github.com/chirino/memory-service/internal/config"
	"github.com/urfave/cli/v3"
)

func prepareTCPListener(_ config.ListenerConfig) (*PreparedListener, error) {
	return nil, fmt.Errorf("TCP listener support was excluded at build time (notcp)")
}

func tcpListenerFlags(_ *config.Config) []cli.Flag { return nil }
