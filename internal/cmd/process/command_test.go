package process

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessorsRejectFixedBearerTokenFlag(t *testing.T) {
	for _, processor := range []string{"clickhouse", "turn-traces"} {
		t.Run(processor, func(t *testing.T) {
			cmd := Command()
			cmd.Writer, cmd.ErrWriter = io.Discard, io.Discard
			err := cmd.Run(t.Context(), []string{"process", processor, "--bearer-token", "fixed-token"})
			require.ErrorContains(t, err, "flag provided but not defined: -bearer-token")
		})
	}
}
