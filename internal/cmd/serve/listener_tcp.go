//go:build !notcp

package serve

import (
	"fmt"
	"net"

	"github.com/chirino/memory-service/internal/config"
	"github.com/urfave/cli/v3"
)

func prepareTCPListener(cfg config.ListenerConfig) (*PreparedListener, error) {
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

func tcpListenerFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:        "port",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_PORT"),
			Destination: &cfg.Listener.Port,
			Value:       cfg.Listener.Port,
			Usage:       "HTTP server port",
		},
		&cli.IntFlag{
			Name:        "management-port",
			Category:    "Network Listener: Management:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_PORT"),
			Destination: &cfg.ManagementListener.Port,
			Value:       cfg.ManagementListener.Port,
			Usage:       "Dedicated port for health and metrics (0 = OS-assigned random port); when unset, served on the main port",
		},
	}
}
