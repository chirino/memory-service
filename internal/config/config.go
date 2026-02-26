package config

import (
	"context"
	"os"
	"strings"
	"time"
)

// ListenerConfig holds the network/TLS settings for a single listener (main or management).
type ListenerConfig struct {
	Port              int
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
	CacheType string // "redis", "infinispan", or "none"

	// Optional named Redis client (Java parity surface).
	CacheRedisClient string

	// Infinispan cache options (Java parity surface).
	InfinispanStartupTimeout              time.Duration
	InfinispanMemoryEntriesCacheName      string
	InfinispanResponseRecordingsCacheName string

	// Memory entries cache TTL.
	CacheEpochTTL time.Duration

	// Attachment store type
	AttachType string // "db", "postgres", "mongo", or "s3"

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
	// ManagementListenerEnabled is true when --management-port (or MEMORY_SERVICE_MANAGEMENT_PORT)
	// was explicitly provided. When false, management endpoints are served on the main port.
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
		S3DirectDownload:             true,
		AdminOIDCRole:                "admin",
		AuditorOIDCRole:              "auditor",
		EncryptionProviders:          "plain",
		EncryptionProviderDEKType:    "dek",
		EncryptionProviderDEKEnabled: true,
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
