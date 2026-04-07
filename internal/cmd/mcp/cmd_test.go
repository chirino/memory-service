package mcp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestCommandIncludesRemoteAndEmbeddedSubcommands(t *testing.T) {
	cmd := Command()
	require.Len(t, cmd.Commands, 2)
	assert.Equal(t, "remote", cmd.Commands[0].Name)
	assert.Equal(t, "embedded", cmd.Commands[1].Name)
	assert.Nil(t, cmd.Action)
}

func TestRemoteCommandRequiresConnectionFlags(t *testing.T) {
	cmd := RemoteCommand()
	require.Len(t, cmd.Flags, 3)
	urlFlag, ok := cmd.Flags[0].(*cli.StringFlag)
	require.True(t, ok)
	apiKeyFlag, ok := cmd.Flags[1].(*cli.StringFlag)
	require.True(t, ok)
	assert.True(t, urlFlag.Required)
	assert.True(t, apiKeyFlag.Required)
}

func TestEmbeddedCommandUsesEmbeddedFlagSubset(t *testing.T) {
	cmd := EmbeddedCommand()

	flagNames := make(map[string]bool, len(cmd.Flags))
	for _, flag := range cmd.Flags {
		for _, name := range flag.Names() {
			flagNames[name] = true
		}
	}

	assert.True(t, flagNames["db-url"])
	assert.True(t, flagNames["cache-kind"])
	assert.True(t, flagNames["attachments-kind"])
	assert.True(t, flagNames["embedding-kind"])
	assert.True(t, flagNames["vector-kind"])
	assert.True(t, flagNames["temp-dir"])

	assert.False(t, flagNames["port"])
	assert.False(t, flagNames["plain-text"])
	assert.False(t, flagNames["tls"])
	assert.False(t, flagNames["management-port"])
	assert.False(t, flagNames["tls-cert-file"])
	assert.False(t, flagNames["oidc-issuer"])
	assert.False(t, flagNames["roles-admin-users"])
	assert.False(t, flagNames["eventbus-kind"])
	assert.False(t, flagNames["outbox-enabled"])
	assert.False(t, flagNames["prometheus-url"])
}

func TestHandlerTransportRoundTrip(t *testing.T) {
	transport := &handlerTransport{
		h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1/test", r.URL.Path)
			assert.Equal(t, "value", r.Header.Get("X-Test"))
			w.Header().Set("X-Reply", "ok")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, "handled")
		}),
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://embedded.local/v1/test", nil)
	require.NoError(t, err)
	req.Header.Set("X-Test", "value")

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.Equal(t, "ok", resp.Header.Get("X-Reply"))
	assert.Equal(t, "handled", strings.TrimSpace(string(body)))
}
