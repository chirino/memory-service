package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplyJavaCompatFromEnv(t *testing.T) {
	t.Setenv("MEMORY_SERVICE_ATTACHMENTS_MAX_SIZE", "12M")
	t.Setenv("MEMORY_SERVICE_ATTACHMENTS_DEFAULT_EXPIRES_IN", "PT2H")
	t.Setenv("MEMORY_SERVICE_ATTACHMENTS_MAX_EXPIRES_IN", "PT6H")
	t.Setenv("MEMORY_SERVICE_ATTACHMENTS_DOWNLOAD_URL_EXPIRES_IN", "PT10M")
	t.Setenv("MEMORY_SERVICE_SEARCH_SEMANTIC_ENABLED", "false")
	t.Setenv("MEMORY_SERVICE_CORS_ENABLED", "true")
	t.Setenv("MEMORY_SERVICE_VECTOR_QDRANT_PORT", "7443")
	t.Setenv("MEMORY_SERVICE_VECTOR_QDRANT_HOST", "qdrant.example")

	cfg := DefaultConfig()
	err := cfg.ApplyJavaCompatFromEnv()
	require.NoError(t, err)

	require.Equal(t, int64(12*1024*1024), cfg.AttachmentMaxSize)
	require.Equal(t, 2*time.Hour, cfg.AttachmentDefaultExpiresIn)
	require.Equal(t, 6*time.Hour, cfg.AttachmentMaxExpiresIn)
	require.Equal(t, 10*time.Minute, cfg.AttachmentDownloadURLExpiresIn)
	require.False(t, cfg.SearchSemanticEnabled)
	require.True(t, cfg.CORSEnabled)
	require.Equal(t, "qdrant.example", cfg.QdrantHost)
	require.Equal(t, 7443, cfg.QdrantPort)
}

func TestQdrantAddress_Defaults(t *testing.T) {
	var cfg Config
	require.Equal(t, "localhost:6334", cfg.QdrantAddress())
}

func TestQdrantAddress_UsesPortFromHostWhenProvided(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QdrantHost = "localhost:7443"
	cfg.QdrantPort = 6334

	require.Equal(t, "localhost:7443", cfg.QdrantAddress())
}

func TestQdrantAddress_UsesHostPortFromURLWhenProvided(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QdrantHost = "http://localhost:9443"
	cfg.QdrantPort = 6334

	require.Equal(t, "localhost:9443", cfg.QdrantAddress())
}
