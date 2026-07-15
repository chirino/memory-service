//go:build !notcp

package serve

import (
	"net"
	"strconv"
	"strings"

	"github.com/chirino/memory-service/internal/config"
	"github.com/urfave/cli/v3"
)

func prepareTCPListener(cfg config.ListenerConfig) (*PreparedListener, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(cfg.Port)))
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
		&cli.StringFlag{
			Name:        "host",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_HOST"),
			Destination: &cfg.Listener.Host,
			Value:       cfg.Listener.Host,
			Usage:       "HTTP server bind host",
		},
		&cli.IntFlag{
			Name:        "management-port",
			Category:    "Network Listener: Management:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_PORT"),
			Destination: &cfg.ManagementListener.Port,
			Value:       cfg.ManagementListener.Port,
			Usage:       "Dedicated port for health and metrics (0 = OS-assigned random port); when unset, served on the main port",
		},
		&cli.StringFlag{
			Name:        "management-host",
			Category:    "Network Listener: Management:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_HOST"),
			Destination: &cfg.ManagementListener.Host,
			Value:       cfg.ManagementListener.Host,
			Usage:       "Management server bind host",
		},
	}
}
