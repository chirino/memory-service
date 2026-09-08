package serve

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/tracing"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	"github.com/stretchr/testify/require"
)

// sentinelEmbedName is a unique embed type name used only in this test file.
// Registered once in init() so it is available to BuildServer at test time.
const sentinelEmbedName = "test-sentinel-provider-capture"

// sentinelProviderWasSet is set to true by the sentinel loader when it finds
// a non-nil provider in the loader context (i.e. WithProviderContext was called
// before the loader ran).
var sentinelProviderWasSet atomic.Bool

func init() {
	registryembed.Register(registryembed.Plugin{
		Name: sentinelEmbedName,
		Loader: func(ctx context.Context) (registryembed.Embedder, error) {
			// tracing.ProviderFromContext returns nil when no provider was stored
			// via WithProviderContext.  If BuildServer's line 157 is removed,
			// this will be nil and the test fails.
			tp := tracing.ProviderFromContext(ctx)
			sentinelProviderWasSet.Store(tp != nil)
			// Return nil so BuildServer treats the embedder as unavailable and
			// continues server initialisation without aborting.
			return nil, nil
		},
	})
}

// TestBuildServerThreadsProviderToEmbedderLoader verifies that BuildServer
// places the server-scoped TracerProvider into the loader context before
// invoking the embedder loader, so plugin loaders (infinispan, qdrant, openai,
// etc.) can retrieve the provider via tracing.ProviderFromContextOrNoop.
//
// Failure mode: if the line
//
//	ctx = tracing.WithProviderContext(ctx, tp)          // server.go:157
//
// is removed from BuildServer, the sentinel loader sees an undecorated context,
// tracing.ProviderFromContext returns nil, sentinelProviderWasSet is false, and
// this test fails.
func TestBuildServerThreadsProviderToEmbedderLoader(t *testing.T) {
	if !buildcaps.SQLite {
		t.Skip("requires sqlite build tag")
	}

	// Reset from any prior run.
	sentinelProviderWasSet.Store(false)

	dbURL := filepath.Join(t.TempDir(), "tracing-test.db")
	base := config.DefaultConfig()
	// Override only the fields required for a minimal in-process server that
	// exercises the embedder loader path (SearchSemanticEnabled + non-"none" EmbedType).
	base.Mode = config.ModeTesting
	base.DatastoreType = "sqlite"
	base.CacheType = "none"
	base.AttachType = "db"
	base.VectorType = ""
	base.EmbedType = sentinelEmbedName
	base.SearchSemanticEnabled = true
	base.DBURL = dbURL
	base.EncryptionProviders = "plain"
	base.EncryptionAllowPlain = true
	base.ManagementOnMainListener = true
	cfg := &base

	ctx := config.WithContext(context.Background(), cfg)
	srv, err := BuildServer(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	require.True(t, sentinelProviderWasSet.Load(),
		"BuildServer must call tracing.WithProviderContext(ctx, tp) before invoking "+
			"the embedder loader; tracing.ProviderFromContext returned nil, which means "+
			"the 'ctx = tracing.WithProviderContext(ctx, tp)' line (server.go:157) is "+
			"absent and all plugin loaders will fall back to a noop provider")
}
