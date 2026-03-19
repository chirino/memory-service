//go:build nouds

package serve

import (
	"fmt"

	"github.com/chirino/memory-service/internal/config"
	"github.com/urfave/cli/v3"
)

func prepareUnixListener(_ string) (*PreparedListener, error) {
	return nil, fmt.Errorf("Unix domain socket support was excluded at build time (nouds)")
}

func udsListenerFlags(_ *config.Config) []cli.Flag { return nil }
