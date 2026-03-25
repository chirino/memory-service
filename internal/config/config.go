package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ListenerConfig holds the network/TLS settings for a single listener (main or management).
type ListenerConfig struct {
	Port              int
	UnixSocket        string
	EnablePlainText   bool
	EnableTLS         bool
	TLSCertFile       string
	TLSKeyFile        string
	ReadHeaderTimeout time.Duration
}

type contextKey struct{}

// WithContext returns a new context carrying the given Config.
func WithContext(ctx context.Context, cfg *Config) context.Context {
	return context.WithValue(ctx, contextKey{}, cfg)
}

// FromContext retrieves the Config from the context.
func FromContext(ctx context.Context) *Config {
	cfg, _ := ctx.Value(contextKey{}).(*Config)
	return cfg
}

const (
	ModeProd    = "prod"
	ModeTesting = "testing"
)

// Config holds all configuration for the memory service.
type Config struct {
	// Mode controls security behavior: "prod" (default) or "testing".
	// In testing mode, X-Client-ID header is accepted and API key validation is relaxed.
	Mode string

	// Database
	DBURL string

	// Run datastore migrations on startup.
	DatastoreMigrateAtStart bool

	// Redis
	RedisURL string

	// Infinispan (RESP protocol — connects via go-redis under the covers)
	InfinispanHost     string // host:port (e.g. "localhost:11222")
	InfinispanUsername string
	InfinispanPassword string

	// Datastore backend type
	DatastoreType string // "postgres" or "mongo"

	// Cache backend type
	CacheType string // "local", "redis", "infinispan", or "none"

	// Optional named Redis client (Java parity surface).
	CacheRedisClient string

	// Infinispan cache options (Java parity surface).
	InfinispanStartupTimeout              time.Duration
	InfinispanMemoryEntriesCacheName      string
	InfinispanResponseRecordingsCacheName string

	// Memory entries cache TTL.
	CacheEpochTTL time.Duration
	// Process-local cache options.
	CacheLocalMaxBytes    int64
	CacheLocalNumCounters int64
	CacheLocalBufferItems int64

	// Attachment store type
	AttachType string // "db", "postgres", "mongo", "s3", or "fs"
	// AttachTypeExplicit records whether the attachment store was explicitly set by flag/env.
	AttachTypeExplicit bool
	// AttachFSDir overrides the local filesystem directory used by the "fs" attachment store.
	AttachFSDir string

	// Attachment behavior.
	AttachmentMaxSize              int64
	AttachmentDefaultExpiresIn     time.Duration
	AttachmentMaxExpiresIn         time.Duration
	AttachmentCleanupInterval      time.Duration
	AttachmentDownloadURLExpiresIn time.Duration

	// Vector store type
	VectorType string // "pgvector", "qdrant", or "" (disabled)

	// Run vector migrations on startup.
	VectorMigrateAtStart bool

	// Number of entries to embed and index per background indexer tick.
	VectorIndexerBatchSize int

	// Qdrant
	QdrantHost             string
	QdrantPort             int
	QdrantCollectionPrefix string
	QdrantCollectionName   string
	QdrantAPIKey           string
	QdrantUseTLS           bool
	QdrantStartupTimeout   time.Duration

	// Embedding type
	EmbedType string // "none", "local", or "openai"

	// OpenAI
	OpenAIAPIKey     string
	OpenAIModelName  string
	OpenAIBaseURL    string
	OpenAIDimensions int

	// Search feature toggles.
	SearchSemanticEnabled bool
	SearchFulltextEnabled bool

	// OIDC
	OIDCIssuer       string
	OIDCDiscoveryURL string // Internal URL for OIDC discovery (when issuer URL is not reachable)

	// Prometheus
	PrometheusURL string

	// MetricsLabels is a comma-separated list of key=value pairs added as
	// constant labels to all Prometheus metrics. Values support ${VAR} expansion.
	// Defaults to "service=memory-service".
	MetricsLabels string

	// S3
	S3Bucket           string
	S3Prefix           string
	S3DirectDownload   bool
	S3ExternalEndpoint string
	S3UsePathStyle     bool

	// Server
	Listener           ListenerConfig
	ManagementListener ListenerConfig
	// ManagementListenerEnabled is true when --management-port / --management-unix-socket
	// (or their env vars) was explicitly provided. When false, management endpoints are
	// served on the main port.
	ManagementListenerEnabled bool
	// ManagementAccessLog enables HTTP access logging for management endpoints (/health, /ready, /metrics).
	// Disabled by default to suppress high-frequency probe noise from the access log.
	ManagementAccessLog bool
	CORSEnabled         bool
	CORSOrigins         string

	// Security
	// APIKeys maps API key values to client IDs (Java parity: MEMORY_SERVICE_API_KEYS_<CLIENT_ID>=<key>).
	APIKeys         map[string]string // key value → clientId
	AdminOIDCRole   string
	AuditorOIDCRole string
	IndexerOIDCRole string
	AdminUsers      string
	AuditorUsers    string
	IndexerUsers    string
	AdminClients    string
	AuditorClients  string
	IndexerClients  string

	// Encryption
	EncryptionProviders          string
	EncryptionProviderDEKType    string
	EncryptionProviderDEKEnabled bool
	EncryptionVaultTransitKey    string
	// EncryptionKMSKeyID is the AWS KMS key ID or ARN used by the "kms" provider.
	EncryptionKMSKeyID string
	// EncryptionKey is a comma-separated list of AES keys for the "dek" provider.
	// The first key is primary (used for new encryptions); subsequent keys are legacy
	// (decryption-only, for zero-downtime key rotation).
	EncryptionKey string
	// EncryptionDBDisabled skips GCM cipher setup in the postgres/mongo stores even when
	// EncryptionKey is set. Useful when you want signed download URLs without encrypting data at rest.
	EncryptionDBDisabled bool
	// EncryptionAttachmentsDisabled skips the encrypt.Wrap layer on the attachment store even when
	// EncryptionKey is set.
	EncryptionAttachmentsDisabled bool

	// Body size limit (bytes)
	MaxBodySize int64

	// Attachments
	AllowPrivateSourceURLs bool

	// Temporary file directory. Empty uses platform default temp directory.
	TempDir string

	// Graceful shutdown drain timeout (seconds)
	DrainTimeout int

	// DB pool
	DBMaxOpenConns int
	DBMaxIdleConns int

	// Eviction
	EvictionBatchSize  int
	EvictionBatchDelay int // milliseconds

	// How long to retain response-resumer temp files.
	ResumerTempFileRetention time.Duration

	// Resumer advertised address
	ResumerAdvertisedAddress string

	// Admin
	RequireJustification bool

	// Event bus
	EventBusType           string // "local", "redis", "postgres"
	EventBusOutboundBuffer int    // outbound channel capacity for cross-node publish pipeline
	EventBusBatchSize      int    // max events per cross-node publish batch

	// SSE event stream
	SSEKeepaliveInterval     time.Duration
	SSEMembershipCacheTTL    time.Duration
	SSEMaxConnectionsPerUser int
	SSESubscriberBufferSize  int
	OutboxEnabled            bool
	OutboxReplayBatchSize    int

	// Episodic memory settings
	EpisodicMaxDepth           int           // Maximum namespace depth (default 5)
	EpisodicIndexingBatchSize  int           // Items processed per indexer cycle (default 100)
	EpisodicIndexingInterval   time.Duration // Polling interval for vector indexer (default 30s)
	EpisodicTTLInterval        time.Duration // Polling interval for TTL expiry + eviction (default 60s)
	EpisodicEvictionBatchSize  int           // Max rows processed per eviction pass (default 100)
	EpisodicTombstoneRetention time.Duration // How long to keep delete/expired tombstones (default 90 days)
	EpisodicPolicyDir          string        // Directory for OPA Rego policies (default: built-in)
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Mode:                                  ModeProd,
		DatastoreType:                         "postgres",
		DatastoreMigrateAtStart:               true,
		CacheType:                             "none",
		CacheRedisClient:                      "default",
		InfinispanStartupTimeout:              30 * time.Second,
		InfinispanMemoryEntriesCacheName:      "memory-entries",
		InfinispanResponseRecordingsCacheName: "response-recordings",
		CacheEpochTTL:                         10 * time.Minute,
		CacheLocalMaxBytes:                    64 * 1024 * 1024,
		CacheLocalNumCounters:                 100000,
		CacheLocalBufferItems:                 64,
		AttachType:                            "db",
		AttachmentMaxSize:                     10 * 1024 * 1024, // 10 MB
		AttachmentDefaultExpiresIn:            time.Hour,
		AttachmentMaxExpiresIn:                24 * time.Hour,
		AttachmentCleanupInterval:             5 * time.Minute,
		AttachmentDownloadURLExpiresIn:        5 * time.Minute,
		VectorType:                            "",
		VectorMigrateAtStart:                  true,
		VectorIndexerBatchSize:                500,
		EmbedType:                             "local",
		OpenAIModelName:                       "text-embedding-3-small",
		OpenAIBaseURL:                         "https://api.openai.com/v1",
		SearchSemanticEnabled:                 true,
		SearchFulltextEnabled:                 true,
		Listener: ListenerConfig{
			Port:              8080,
			EnablePlainText:   true,
			EnableTLS:         true,
			ReadHeaderTimeout: 5 * time.Second,
		},
		ManagementListener: ListenerConfig{
			EnablePlainText: true,
			EnableTLS:       true,
		},
		MaxBodySize:                  20 * 1024 * 1024, // 2x attachment max-size
		DrainTimeout:                 30,
		DBMaxOpenConns:               25,
		DBMaxIdleConns:               5,
		EvictionBatchSize:            1000,
		EvictionBatchDelay:           100,
		ResumerTempFileRetention:     30 * time.Minute,
		QdrantHost:                   "localhost",
		QdrantPort:                   6334,
		QdrantCollectionPrefix:       "memory-service",
		QdrantStartupTimeout:         30 * time.Second,
		S3DirectDownload:             false,
		AdminOIDCRole:                "admin",
		AuditorOIDCRole:              "auditor",
		EncryptionProviders:          "plain",
		EncryptionProviderDEKType:    "dek",
		EncryptionProviderDEKEnabled: true,

		// Event bus defaults
		EventBusType:             "local",
		EventBusOutboundBuffer:   200,
		EventBusBatchSize:        100,
		SSEKeepaliveInterval:     30 * time.Second,
		SSEMembershipCacheTTL:    5 * time.Minute,
		SSEMaxConnectionsPerUser: 5,
		SSESubscriberBufferSize:  64,
		OutboxEnabled:            false,
		OutboxReplayBatchSize:    1000,

		// Episodic memory defaults
		EpisodicMaxDepth:           5,
		EpisodicIndexingBatchSize:  100,
		EpisodicIndexingInterval:   30 * time.Second,
		EpisodicTTLInterval:        60 * time.Second,
		EpisodicEvictionBatchSize:  100,
		EpisodicTombstoneRetention: 90 * 24 * time.Hour,
	}
}

// ResolvedTempDir returns the configured temp directory or the platform default.
func (c *Config) ResolvedTempDir() string {
	if c == nil {
		return os.TempDir()
	}
	if dir := strings.TrimSpace(c.TempDir); dir != "" {
		return dir
	}
	return os.TempDir()
}

// ResolvedAttachmentsFSDir returns the configured filesystem attachment root or derives it from
// the SQLite DB path when possible.
func (c *Config) ResolvedAttachmentsFSDir() (string, error) {
	if c == nil {
		return "", fmt.Errorf("attachment fs dir: missing config")
	}
	if dir := strings.TrimSpace(c.AttachFSDir); dir != "" {
		return dir, nil
	}
	dbPath, err := c.SQLiteFilePath()
	if err != nil {
		return "", fmt.Errorf("attachment fs dir: %w", err)
	}
	return dbPath + ".attachments", nil
}

// SQLiteFilePath resolves a file-backed SQLite DB path from the configured DBURL.
func (c *Config) SQLiteFilePath() (string, error) {
	if c == nil {
		return "", fmt.Errorf("sqlite db url is not configured")
	}
	dsn := strings.TrimSpace(c.DBURL)
	if dsn == "" {
		return "", fmt.Errorf("sqlite db url is not configured")
	}
	if dsn == ":memory:" {
		return "", fmt.Errorf("sqlite db url %q is not file-backed", dsn)
	}

	if strings.HasPrefix(dsn, "file:") {
		return sqliteURIFilePath(dsn)
	}

	dbPath := dsn
	if idx := strings.IndexRune(dbPath, '?'); idx >= 0 {
		if strings.Contains(strings.ToLower(dbPath[idx+1:]), "mode=memory") {
			return "", fmt.Errorf("sqlite db url %q is not file-backed", dsn)
		}
		dbPath = dbPath[:idx]
	}
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return "", fmt.Errorf("sqlite db url %q does not include a file path", dsn)
	}
	return filepath.Clean(dbPath), nil
}

func sqliteURIFilePath(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("invalid sqlite file URI %q: %w", dsn, err)
	}
	q := u.Query()
	if strings.EqualFold(strings.TrimSpace(q.Get("mode")), "memory") {
		return "", fmt.Errorf("sqlite db url %q is not file-backed", dsn)
	}

	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("sqlite db url %q does not resolve to a local file path", dsn)
	}

	rawPath := strings.TrimSpace(u.Path)
	if rawPath == "" {
		rawPath = strings.TrimSpace(u.Opaque)
	}
	if rawPath == "" || rawPath == ":memory:" || rawPath == "::memory:" {
		return "", fmt.Errorf("sqlite db url %q is not file-backed", dsn)
	}
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", fmt.Errorf("invalid sqlite file URI path %q: %w", dsn, err)
	}
	return filepath.Clean(filepath.FromSlash(path)), nil
}
