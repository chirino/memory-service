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
	"github.com/chirino/memory-service/internal/registry/encrypt"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/gin-gonic/gin"
	"github.com/urfave/cli/v3"

	// Import core (non-excludable) plugins to trigger init() registration.
	// Excludable plugins are imported via plugin_*.go files with build constraints.
	_ "github.com/chirino/memory-service/internal/plugin/attach/filesystem"
	_ "github.com/chirino/memory-service/internal/plugin/cache/local"
	_ "github.com/chirino/memory-service/internal/plugin/cache/noop"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/embed/local"
	_ "github.com/chirino/memory-service/internal/plugin/encrypt/dek"
	_ "github.com/chirino/memory-service/internal/plugin/encrypt/plain"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
)

// Command returns the serve sub-command.
func Command() *cli.Command {
	cfg := config.DefaultConfig()
	var readHeaderTimeoutSecs int = 5
	var cacheLocalMaxBytes string
	var cacheLocalNumCounters int
	var cacheLocalBufferItems int
	return &cli.Command{
		Name:  "serve",
		Usage: "Start the memory service HTTP and gRPC servers",
		CustomHelpTemplate: cli.CommandHelpTemplate + `NOTES:
   API key authentication is configured via environment variables — one per client ID:
   MEMORY_SERVICE_API_KEYS_<CLIENT_ID>=key1,key2,...

   Example:
   MEMORY_SERVICE_API_KEYS_AGENT_A=secret-key-1
   MEMORY_SERVICE_API_KEYS_AGENT_B=key-one,key-two
`,
		Flags: flags(&cfg, &readHeaderTimeoutSecs, &cacheLocalMaxBytes, &cacheLocalNumCounters, &cacheLocalBufferItems),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := cfg.ApplyJavaCompatFromEnv(); err != nil {
				return err
			}
			// Let plugins apply post-parse transformations (e.g. env var forwarding).
			registrystore.ApplyAll(&cfg, cmd)
			registrycache.ApplyAll(&cfg, cmd)
			registryvector.ApplyAll(&cfg, cmd)
			registryembed.ApplyAll(&cfg, cmd)
			registryattach.ApplyAll(&cfg, cmd)
			registryeventbus.ApplyAll(&cfg, cmd)
			encrypt.ApplyAll(&cfg, cmd)
			if strings.TrimSpace(cacheLocalMaxBytes) != "" {
				size, err := config.ParseMemorySize(cacheLocalMaxBytes)
				if err != nil {
					return err
				}
				cfg.CacheLocalMaxBytes = size
			}
			if cmd.IsSet("cache-local-num-counters") {
				cfg.CacheLocalNumCounters = int64(cacheLocalNumCounters)
			}
			if cmd.IsSet("cache-local-buffer-items") {
				cfg.CacheLocalBufferItems = int64(cacheLocalBufferItems)
			}
			cfg.Listener.ReadHeaderTimeout = time.Duration(readHeaderTimeoutSecs) * time.Second
			cfg.ManagementListener.ReadHeaderTimeout = cfg.Listener.ReadHeaderTimeout
			selections := listenerSelections{
				mainPortExplicit:       cmd.IsSet("port"),
				mainUnixSocketExplicit: cmd.IsSet("unix-socket"),
				mgmtPortExplicit:       cmd.IsSet("management-port"),
				mgmtUnixSocketExplicit: cmd.IsSet("management-unix-socket"),
			}
			if err := validateListenerSelections(cfg, selections); err != nil {
				return err
			}
			cfg.ManagementListenerEnabled = selections.mgmtPortExplicit || selections.mgmtUnixSocketExplicit
			cfg.AttachTypeExplicit = cmd.IsSet("attachments-kind")
			return run(config.WithContext(ctx, &cfg), cfg)
		},
	}
}

func flags(cfg *config.Config, readHeaderTimeoutSecs *int, cacheLocalMaxBytes *string, cacheLocalNumCounters *int, cacheLocalBufferItems *int) []cli.Flag {
	*cacheLocalNumCounters = int(cfg.CacheLocalNumCounters)
	*cacheLocalBufferItems = int(cfg.CacheLocalBufferItems)
	coreFlags := []cli.Flag{

		// ── Server ────────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "tls-cert-file",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TLS_CERT_FILE"),
			Destination: &cfg.Listener.TLSCertFile,
			Usage:       "TLS certificate file for listener TLS mode",
		},
		&cli.StringFlag{
			Name:        "tls-key-file",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TLS_KEY_FILE"),
			Destination: &cfg.Listener.TLSKeyFile,
			Usage:       "TLS private key file for listener TLS mode",
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
		&cli.BoolFlag{
			Name:        "admin-require-justification",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ADMIN_REQUIRE_JUSTIFICATION"),
			Destination: &cfg.RequireJustification,
			Usage:       "Require justification for admin API calls",
		},

		// ── Network Listener ──────────────────────────────────────
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

		// ── Network Listener: Management ─────────────────────────
		&cli.BoolFlag{
			Name:        "management-plain-text",
			Category:    "Network Listener: Management:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_PLAIN_TEXT"),
			Destination: &cfg.ManagementListener.EnablePlainText,
			Value:       cfg.ManagementListener.EnablePlainText,
			Usage:       "Enable plaintext HTTP for management server",
		},
		&cli.BoolFlag{
			Name:        "management-tls",
			Category:    "Network Listener: Management:",
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
			Name:        "cache-local-max-bytes",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_CACHE_LOCAL_MAX_BYTES"),
			Destination: cacheLocalMaxBytes,
			Usage:       "Process-local memory cache budget (for example 64M, 512K, 1G)",
		},
		&cli.IntFlag{
			Name:        "cache-local-num-counters",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_CACHE_LOCAL_NUM_COUNTERS"),
			Destination: cacheLocalNumCounters,
			Value:       *cacheLocalNumCounters,
			Usage:       "Ristretto counter count for the process-local cache",
		},
		&cli.IntFlag{
			Name:        "cache-local-buffer-items",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_CACHE_LOCAL_BUFFER_ITEMS"),
			Destination: cacheLocalBufferItems,
			Value:       *cacheLocalBufferItems,
			Usage:       "Ristretto buffer size for the process-local cache",
		},

		// ── Event Bus ─────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "eventbus-kind",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EVENTBUS_KIND"),
			Destination: &cfg.EventBusType,
			Value:       cfg.EventBusType,
			Usage:       "Event bus backend (" + strings.Join(registryeventbus.Names(), "|") + ")",
		},
		&cli.IntFlag{
			Name:        "eventbus-outbound-buffer",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EVENTBUS_OUTBOUND_BUFFER"),
			Destination: &cfg.EventBusOutboundBuffer,
			Value:       cfg.EventBusOutboundBuffer,
			Usage:       "Outbound channel capacity for cross-node publish pipeline",
		},
		&cli.IntFlag{
			Name:        "eventbus-batch-size",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EVENTBUS_BATCH_SIZE"),
			Destination: &cfg.EventBusBatchSize,
			Value:       cfg.EventBusBatchSize,
			Usage:       "Max events per cross-node publish batch",
		},
		&cli.DurationFlag{
			Name:        "sse-keepalive-interval",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_SSE_KEEPALIVE_INTERVAL"),
			Destination: &cfg.SSEKeepaliveInterval,
			Value:       cfg.SSEKeepaliveInterval,
			Usage:       "Interval between SSE keepalive comments",
		},
		&cli.DurationFlag{
			Name:        "sse-membership-cache-ttl",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_SSE_MEMBERSHIP_CACHE_TTL"),
			Destination: &cfg.SSEMembershipCacheTTL,
			Value:       cfg.SSEMembershipCacheTTL,
			Usage:       "TTL for local conversation-group to members cache entries",
		},
		&cli.IntFlag{
			Name:        "sse-max-connections-per-user",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_SSE_MAX_CONNECTIONS_PER_USER"),
			Destination: &cfg.SSEMaxConnectionsPerUser,
			Value:       cfg.SSEMaxConnectionsPerUser,
			Usage:       "Max concurrent SSE connections per user (429 if exceeded)",
		},
		&cli.IntFlag{
			Name:        "sse-subscriber-buffer-size",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_SSE_SUBSCRIBER_BUFFER_SIZE"),
			Destination: &cfg.SSESubscriberBufferSize,
			Value:       cfg.SSESubscriberBufferSize,
			Usage:       "Per-subscriber channel buffer; full buffer triggers eviction",
		},
		&cli.BoolFlag{
			Name:        "outbox-enabled",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_OUTBOX_ENABLED"),
			Destination: &cfg.OutboxEnabled,
			Value:       cfg.OutboxEnabled,
			Usage:       "Enable durable event replay parameters on event stream endpoints",
		},
		&cli.IntFlag{
			Name:        "outbox-replay-batch-size",
			Category:    "Event Bus:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_OUTBOX_REPLAY_BATCH_SIZE"),
			Destination: &cfg.OutboxReplayBatchSize,
			Value:       cfg.OutboxReplayBatchSize,
			Usage:       "Replay page size used by outbox-backed event streams",
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
			Name:        "attachments-fs-dir",
			Category:    "Attachment Storage:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ATTACHMENTS_FS_DIR"),
			Destination: &cfg.AttachFSDir,
			Usage:       "Filesystem directory for local attachment storage",
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
			Name:        "encryption-kind",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_KIND"),
			Destination: &cfg.EncryptionProviders,
			Value:       cfg.EncryptionProviders,
			Usage:       "Comma-separated ordered list of encryption providers (" + strings.Join(encrypt.Names(), "|") + "). First is primary (used for new encryptions).",
		},
		&cli.BoolFlag{
			Name:        "encryption-db-disabled",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_DB_DISABLED"),
			Destination: &cfg.EncryptionDBDisabled,
			Usage:       "Disable at-rest encryption for the database even when encryption is configured",
		},
		&cli.BoolFlag{
			Name:        "encryption-attachments-disabled",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_ATTACHMENTS_DISABLED"),
			Destination: &cfg.EncryptionAttachmentsDisabled,
			Usage:       "Disable at-rest encryption for the attachment store even when encryption is configured",
		},

		// ── Encryption: DEK ───────────────────────────────────────
		&cli.StringFlag{
			Name:        "encryption-dek-key",
			Category:    "Encryption: DEK:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_DEK_KEY", "MEMORY_SERVICE_ENCRYPTION_KEY"),
			Destination: &cfg.EncryptionKey,
			Usage:       "Comma-separated AES keys for the 'dek' provider (hex or base64, 32 bytes). First is primary; additional keys are legacy (decryption-only key rotation). Also derives attachment URL signing keys.",
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
		// ── Embedding ─────────────────────────────────────────────
		&cli.StringFlag{
			Name:        "embedding-kind",
			Category:    "Embedding:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EMBEDDING_KIND"),
			Destination: &cfg.EmbedType,
			Value:       cfg.EmbedType,
			Usage:       "Embedding provider (" + strings.Join(registryembed.Names(), "|") + ")",
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
		// ── Episodic Memory ───────────────────────────────────────
		&cli.IntFlag{
			Name:        "episodic-max-depth",
			Category:    "Episodic Memory:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EPISODIC_MAX_DEPTH"),
			Destination: &cfg.EpisodicMaxDepth,
			Value:       cfg.EpisodicMaxDepth,
			Usage:       "Maximum namespace depth for episodic memory",
		},
		&cli.StringFlag{
			Name:        "episodic-policy-dir",
			Category:    "Episodic Memory:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EPISODIC_POLICY_DIR"),
			Destination: &cfg.EpisodicPolicyDir,
			Usage:       "Directory containing OPA Rego policies for episodic memory (authz.rego, attributes.rego, filter.rego); defaults to built-in policies",
		},
		&cli.IntFlag{
			Name:        "episodic-indexing-batch-size",
			Category:    "Episodic Memory:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EPISODIC_INDEXING_BATCH_SIZE"),
			Destination: &cfg.EpisodicIndexingBatchSize,
			Value:       cfg.EpisodicIndexingBatchSize,
			Usage:       "Items processed per episodic indexer cycle",
		},
		&cli.IntFlag{
			Name:        "episodic-eviction-batch-size",
			Category:    "Episodic Memory:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EPISODIC_EVICTION_BATCH_SIZE"),
			Destination: &cfg.EpisodicEvictionBatchSize,
			Value:       cfg.EpisodicEvictionBatchSize,
			Usage:       "Max soft-deleted rows processed per episodic eviction pass",
		},
		&cli.DurationFlag{
			Name:        "episodic-tombstone-retention",
			Category:    "Episodic Memory:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EPISODIC_TOMBSTONE_RETENTION"),
			Destination: &cfg.EpisodicTombstoneRetention,
			Value:       cfg.EpisodicTombstoneRetention,
			Usage:       "How long to retain delete/expired tombstones for event history (default 90d)",
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

	// Append flags contributed by registered plugins.
	// Append listener flags contributed by conditional build-tag files.
	coreFlags = append(coreFlags, tcpListenerFlags(cfg)...)
	coreFlags = append(coreFlags, udsListenerFlags(cfg)...)

	// Append flags contributed by registered plugins.
	coreFlags = append(coreFlags, registrystore.PluginFlags(cfg)...)
	coreFlags = append(coreFlags, registrycache.PluginFlags(cfg)...)
	coreFlags = append(coreFlags, registryeventbus.PluginFlags(cfg)...)
	coreFlags = append(coreFlags, registryvector.PluginFlags(cfg)...)
	coreFlags = append(coreFlags, registryembed.PluginFlags(cfg)...)
	coreFlags = append(coreFlags, registryattach.PluginFlags(cfg)...)
	coreFlags = append(coreFlags, encrypt.PluginFlags(cfg)...)
	return coreFlags
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
