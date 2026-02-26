package serve

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/gin-gonic/gin"
	"github.com/urfave/cli/v3"

	// Import all plugins to trigger init() registration
	_ "github.com/chirino/memory-service/internal/plugin/attach/mongostore"
	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/attach/s3store"
	_ "github.com/chirino/memory-service/internal/plugin/cache/infinispan"
	_ "github.com/chirino/memory-service/internal/plugin/cache/noop"
	_ "github.com/chirino/memory-service/internal/plugin/cache/redis"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/embed/local"
	_ "github.com/chirino/memory-service/internal/plugin/embed/openai"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
	_ "github.com/chirino/memory-service/internal/plugin/store/mongo"
	_ "github.com/chirino/memory-service/internal/plugin/store/postgres"
	_ "github.com/chirino/memory-service/internal/plugin/vector/pgvector"
	_ "github.com/chirino/memory-service/internal/plugin/vector/qdrant"
)

// Command returns the serve sub-command.
func Command() *cli.Command {
	cfg := config.DefaultConfig()
	var readHeaderTimeoutSecs int = 5
	return &cli.Command{
		Name:  "serve",
		Usage: "Start the memory service HTTP and gRPC servers",
		Flags: flags(&cfg, &readHeaderTimeoutSecs),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := cfg.ApplyJavaCompatFromEnv(); err != nil {
				return err
			}
			cfg.Listener.ReadHeaderTimeout = time.Duration(readHeaderTimeoutSecs) * time.Second
			cfg.ManagementListener.ReadHeaderTimeout = cfg.Listener.ReadHeaderTimeout
			cfg.ManagementListenerEnabled = cmd.IsSet("management-port")
			return run(config.WithContext(ctx, &cfg), cfg)
		},
	}
}

func flags(cfg *config.Config, readHeaderTimeoutSecs *int) []cli.Flag {
	return []cli.Flag{

		// ── Server ────────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "tls-cert-file",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TLS_CERT_FILE"),
			Destination: &cfg.Listener.TLSCertFile,
			Usage:       "TLS certificate file for single-port TLS mode",
		},
		&cli.StringFlag{
			Name:        "tls-key-file",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TLS_KEY_FILE"),
			Destination: &cfg.Listener.TLSKeyFile,
			Usage:       "TLS private key file for single-port TLS mode",
		},
		&cli.StringFlag{
			Name:        "advertised-address",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ADVERTISED_ADDRESS"),
			Destination: &cfg.ResumerAdvertisedAddress,
			Usage:       "Advertised host:port for client redirects",
		},
		&cli.IntFlag{
			Name:        "read-header-timeout-seconds",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_READ_HEADER_TIMEOUT_SECONDS"),
			Destination: readHeaderTimeoutSecs,
			Value:       *readHeaderTimeoutSecs,
			Usage:       "HTTP read header timeout in seconds",
		},
		&cli.StringFlag{
			Name:        "temp-dir",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TEMP_DIR"),
			Destination: &cfg.TempDir,
			Usage:       "Directory for temporary files; defaults to OS temp directory",
		},
		&cli.BoolFlag{
			Name:        "management-access-log",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_ACCESS_LOG"),
			Destination: &cfg.ManagementAccessLog,
			Usage:       "Enable HTTP access logging for management endpoints (/health, /ready, /metrics)",
		},

		// ── Network Listener ──────────────────────────────────────
		&cli.IntFlag{
			Name:        "port",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_PORT"),
			Destination: &cfg.Listener.Port,
			Value:       cfg.Listener.Port,
			Usage:       "HTTP server port",
		},
		&cli.BoolFlag{
			Name:        "plain-text",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_PLAIN_TEXT"),
			Destination: &cfg.Listener.EnablePlainText,
			Value:       cfg.Listener.EnablePlainText,
			Usage:       "Enable plaintext HTTP/1.1 + h2c + gRPC",
		},
		&cli.BoolFlag{
			Name:        "tls",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TLS"),
			Destination: &cfg.Listener.EnableTLS,
			Value:       cfg.Listener.EnableTLS,
			Usage:       "Enable TLS HTTP/1.1 + HTTP/2 + gRPC",
		},

		// ── Management Network Listener ───────────────────────────
		&cli.IntFlag{
			Name:        "management-port",
			Category:    "Management Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_PORT"),
			Destination: &cfg.ManagementListener.Port,
			Value:       cfg.ManagementListener.Port,
			Usage:       "Dedicated port for health and metrics (0 = OS-assigned random port); when unset, served on the main port",
		},
		&cli.BoolFlag{
			Name:        "management-plain-text",
			Category:    "Management Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_PLAIN_TEXT"),
			Destination: &cfg.ManagementListener.EnablePlainText,
			Value:       cfg.ManagementListener.EnablePlainText,
			Usage:       "Enable plaintext HTTP for management server",
		},
		&cli.BoolFlag{
			Name:        "management-tls",
			Category:    "Management Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_TLS"),
			Destination: &cfg.ManagementListener.EnableTLS,
			Value:       cfg.ManagementListener.EnableTLS,
			Usage:       "Enable TLS for management server",
		},

		// ── Database ───────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "db-kind",
			Category:    "Database:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DB_KIND"),
			Destination: &cfg.DatastoreType,
			Value:       cfg.DatastoreType,
			Usage:       "Backend store (" + strings.Join(registrystore.Names(), "|") + ")",
		},
		&cli.StringFlag{
			Name:        "db-url",
			Category:    "Database:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DB_URL"),
			Destination: &cfg.DBURL,
			Usage:       "Database connection URL",
			Required:    true,
		},
		&cli.IntFlag{
			Name:        "db-max-open-conns",
			Category:    "Database:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DB_MAX_OPEN_CONNS"),
			Destination: &cfg.DBMaxOpenConns,
			Value:       cfg.DBMaxOpenConns,
			Usage:       "Maximum number of open database connections",
		},
		&cli.IntFlag{
			Name:        "db-max-idle-conns",
			Category:    "Database:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DB_MAX_IDLE_CONNS"),
			Destination: &cfg.DBMaxIdleConns,
			Value:       cfg.DBMaxIdleConns,
			Usage:       "Maximum number of idle database connections",
		},

		// ── Cache ─────────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "cache-kind",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_CACHE_KIND"),
			Destination: &cfg.CacheType,
			Value:       cfg.CacheType,
			Usage:       "Cache backend (" + strings.Join(registrycache.Names(), "|") + ")",
		},
		&cli.StringFlag{
			Name:        "redis-hosts",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_REDIS_HOSTS"),
			Destination: &cfg.RedisURL,
			Usage:       "Redis connection URL",
		},
		&cli.StringFlag{
			Name:        "infinispan-host",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_INFINISPAN_HOST"),
			Destination: &cfg.InfinispanHost,
			Usage:       "Infinispan RESP host:port (e.g. localhost:11222)",
		},
		&cli.StringFlag{
			Name:        "infinispan-username",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_INFINISPAN_USERNAME"),
			Destination: &cfg.InfinispanUsername,
			Usage:       "Infinispan username",
		},
		&cli.StringFlag{
			Name:        "infinispan-password",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_INFINISPAN_PASSWORD"),
			Destination: &cfg.InfinispanPassword,
			Usage:       "Infinispan password",
		},

		// ── Attachment Storage ────────────────────────────────────
		&cli.StringFlag{
			Name:        "attachments-kind",
			Category:    "Attachment Storage:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ATTACHMENTS_KIND"),
			Destination: &cfg.AttachType,
			Value:       cfg.AttachType,
			Usage:       "Attachment store (db|" + strings.Join(registryattach.Names(), "|") + ")",
		},
		&cli.StringFlag{
			Name:        "attachments-s3-bucket",
			Category:    "Attachment Storage:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ATTACHMENTS_S3_BUCKET"),
			Destination: &cfg.S3Bucket,
			Usage:       "S3 bucket for attachments",
		},
		&cli.BoolFlag{
			Name:        "attachments-s3-use-path-style",
			Category:    "Attachment Storage:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ATTACHMENTS_S3_USE_PATH_STYLE"),
			Destination: &cfg.S3UsePathStyle,
			Usage:       "Use path-style S3 addressing (required for LocalStack/MinIO)",
		},
		&cli.BoolFlag{
			Name:        "attachments-allow-private-source-urls",
			Category:    "Attachment Storage:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ATTACHMENTS_ALLOW_PRIVATE_SOURCE_URLS"),
			Destination: &cfg.AllowPrivateSourceURLs,
			Usage:       "Allow sourceUrl attachment downloads from private/loopback network addresses (unsafe)",
		},
		// ── Encryption ────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "encryption-dek-key",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_DEK_KEY"),
			Destination: &cfg.EncryptionKey,
			Usage:       "Encryption key (hex or base64, 16/24/32 bytes). Enables at-rest encryption and signed download URLs",
		},
		&cli.BoolFlag{
			Name:        "encryption-db-disabled",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_DB_DISABLED"),
			Destination: &cfg.EncryptionDBDisabled,
			Usage:       "Disable at-rest encryption for the database even when --encryption-dek-key is set",
		},
		&cli.BoolFlag{
			Name:        "encryption-attachments-disabled",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_ATTACHMENTS_DISABLED"),
			Destination: &cfg.EncryptionAttachmentsDisabled,
			Usage:       "Disable at-rest encryption for the attachment store even when --encryption-dek-key is set",
		},

		// ── Vector Store ──────────────────────────────────────────
		&cli.StringFlag{
			Name:        "vector-kind",
			Category:    "Vector Store:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_KIND"),
			Destination: &cfg.VectorType,
			Value:       cfg.VectorType,
			Usage:       "Vector store (" + strings.Join(registryvector.Names(), "|") + ")",
		},
		&cli.IntFlag{
			Name:        "vector-indexer-batch-size",
			Category:    "Vector Store:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_INDEXER_BATCH_SIZE"),
			Destination: &cfg.VectorIndexerBatchSize,
			Value:       cfg.VectorIndexerBatchSize,
			Usage:       "Number of entries to embed and index per background indexer tick",
		},
		&cli.StringFlag{
			Name:        "vector-qdrant-host",
			Category:    "Vector Store:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_QDRANT_HOST", "MEMORY_SERVICE_QDRANT_HOST"),
			Destination: &cfg.QdrantHost,
			Value:       cfg.QdrantAddress(),
			Usage:       "Qdrant host or host:port",
		},

		// ── Embedding ─────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "embedding-kind",
			Category:    "Embedding:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EMBEDDING_KIND"),
			Destination: &cfg.EmbedType,
			Value:       cfg.EmbedType,
			Usage:       "Embedding provider (" + strings.Join(registryembed.Names(), "|") + ")",
		},
		&cli.StringFlag{
			Name:        "embedding-openai-api-key",
			Category:    "Embedding:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY", "MEMORY_SERVICE_OPENAI_API_KEY", "OPENAI_API_KEY"),
			Destination: &cfg.OpenAIAPIKey,
			Usage:       "OpenAI API key",
		},

		// ── Authorization ─────────────────────────────────────────
		&cli.StringFlag{
			Name:        "oidc-issuer",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_OIDC_ISSUER"),
			Destination: &cfg.OIDCIssuer,
			Usage:       "OIDC issuer URL (enables OIDC auth)",
		},
		&cli.StringFlag{
			Name:        "oidc-discovery-url",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_OIDC_DISCOVERY_URL"),
			Destination: &cfg.OIDCDiscoveryURL,
			Usage:       "OIDC discovery URL (internal URL when issuer is not directly reachable)",
		},
		&cli.StringFlag{
			Name:        "roles-admin-oidc-role",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_ADMIN_OIDC_ROLE"),
			Destination: &cfg.AdminOIDCRole,
			Value:       cfg.AdminOIDCRole,
			Usage:       "OIDC role name that maps to admin permissions",
		},
		&cli.StringFlag{
			Name:        "roles-auditor-oidc-role",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_AUDITOR_OIDC_ROLE"),
			Destination: &cfg.AuditorOIDCRole,
			Value:       cfg.AuditorOIDCRole,
			Usage:       "OIDC role name that maps to auditor permissions",
		},
		&cli.StringFlag{
			Name:        "roles-indexer-oidc-role",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_INDEXER_OIDC_ROLE"),
			Destination: &cfg.IndexerOIDCRole,
			Usage:       "OIDC role name that maps to indexer permissions",
		},
		&cli.StringFlag{
			Name:        "roles-admin-users",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_ADMIN_USERS"),
			Destination: &cfg.AdminUsers,
			Usage:       "Comma-separated user IDs with admin permissions",
		},
		&cli.StringFlag{
			Name:        "roles-auditor-users",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_AUDITOR_USERS"),
			Destination: &cfg.AuditorUsers,
			Usage:       "Comma-separated user IDs with auditor permissions",
		},
		&cli.StringFlag{
			Name:        "roles-indexer-users",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_INDEXER_USERS"),
			Destination: &cfg.IndexerUsers,
			Usage:       "Comma-separated user IDs with indexer permissions",
		},
		&cli.StringFlag{
			Name:        "roles-admin-clients",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_ADMIN_CLIENTS"),
			Destination: &cfg.AdminClients,
			Usage:       "Comma-separated API client IDs with admin permissions",
		},
		&cli.StringFlag{
			Name:        "roles-auditor-clients",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_AUDITOR_CLIENTS"),
			Destination: &cfg.AuditorClients,
			Usage:       "Comma-separated API client IDs with auditor permissions",
		},
		&cli.StringFlag{
			Name:        "roles-indexer-clients",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ROLES_INDEXER_CLIENTS"),
			Destination: &cfg.IndexerClients,
			Usage:       "Comma-separated API client IDs with indexer permissions",
		},
		&cli.BoolFlag{
			Name:        "admin-require-justification",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ADMIN_REQUIRE_JUSTIFICATION"),
			Destination: &cfg.RequireJustification,
			Usage:       "Require justification for admin API calls",
		},

		// ── Monitoring ────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "prometheus-url",
			Category:    "Monitoring:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_PROMETHEUS_URL"),
			Destination: &cfg.PrometheusURL,
			Usage:       "Prometheus base URL for admin stats (e.g. http://prometheus:9090)",
		},
		&cli.StringFlag{
			Name:        "metrics-labels",
			Category:    "Monitoring:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_METRICS_LABELS"),
			Destination: &cfg.MetricsLabels,
			Value:       "service=memory-service",
			Usage:       "Comma-separated key=value pairs added as constant labels to all Prometheus metrics. Supports ${VAR} expansion.",
		},
	}
}

func run(ctx context.Context, cfg config.Config) error {
	srv, err := StartServer(ctx, &cfg)
	if err != nil {
		return err
	}

	<-ctx.Done()
	log.Info("Shutting down...")

	drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Duration(cfg.DrainTimeout)*time.Second)
	defer drainCancel()
	if err := srv.Shutdown(drainCtx); err != nil {
		log.Error("Shutdown error", "err", err)
	}
	log.Info("Server stopped")
	return nil
}

func maxBodySizeMiddleware(maxBodySize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isStreamingRequest(c.Request) {
			c.Next()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodySize)
		c.Next()
	}
}

func isStreamingRequest(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	if req.Method != http.MethodPost || req.URL.Path != "/v1/attachments" {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Type")))
	return strings.HasPrefix(contentType, "multipart/form-data")
}
