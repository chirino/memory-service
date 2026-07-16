package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/user"
	"strconv"
	"strings"
	"sync/atomic"
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
	"github.com/chirino/memory-service/internal/security"
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

type FlagState struct {
	ReadHeaderTimeoutSecs int
	CacheLocalMaxBytes    string
	CacheLocalNumCounters int
	CacheLocalBufferItems int
}

func NewFlagState(cfg *config.Config) *FlagState {
	return &FlagState{
		ReadHeaderTimeoutSecs: 5,
		CacheLocalNumCounters: int(cfg.CacheLocalNumCounters),
		CacheLocalBufferItems: int(cfg.CacheLocalBufferItems),
	}
}

// Command returns the serve sub-command.
func Command() *cli.Command {
	cfg := config.DefaultConfig()
	flagState := NewFlagState(&cfg)
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
		Flags: Flags(&cfg, flagState),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := ApplyParsedFlags(&cfg, cmd, flagState, true); err != nil {
				return err
			}
			return run(config.WithContext(ctx, &cfg), cfg)
		},
	}
}

type flagSetOptions struct {
	includeServer            bool
	includeListeners         bool
	includeDatabase          bool
	includeCache             bool
	includeEventBus          bool
	includeAttachments       bool
	includeEncryption        bool
	includeVector            bool
	includeEmbedding         bool
	includeAuthorization     bool
	includeEpisodic          bool
	includeClustering        bool
	includeMonitoring        bool
	includeDeveloperFrontend bool
}

func Flags(cfg *config.Config, state *FlagState) []cli.Flag {
	return flagsFor(cfg, state, flagSetOptions{
		includeServer:            true,
		includeListeners:         true,
		includeDatabase:          true,
		includeCache:             true,
		includeEventBus:          true,
		includeAttachments:       true,
		includeEncryption:        true,
		includeVector:            true,
		includeEmbedding:         true,
		includeAuthorization:     true,
		includeEpisodic:          true,
		includeClustering:        true,
		includeMonitoring:        true,
		includeDeveloperFrontend: true,
	})
}

func EmbeddedFlags(cfg *config.Config, state *FlagState) []cli.Flag {
	flags := embeddedServerFlags(cfg)
	flags = append(flags, flagsFor(cfg, state, flagSetOptions{
		includeDatabase:    true,
		includeCache:       true,
		includeAttachments: true,
		includeEncryption:  true,
		includeVector:      true,
		includeEmbedding:   true,
		includeEpisodic:    true,
		includeClustering:  true,
	})...)
	return flags
}

func flagsFor(cfg *config.Config, state *FlagState, opts flagSetOptions) []cli.Flag {
	var flags []cli.Flag
	if opts.includeServer {
		flags = append(flags, serverFlags(cfg, state)...)
	}
	if opts.includeListeners {
		flags = append(flags, listenerFlags(cfg)...)
	}
	if opts.includeDatabase {
		flags = append(flags, databaseFlags(cfg)...)
		flags = append(flags, registrystore.PluginFlags(cfg)...)
	}
	if opts.includeCache {
		flags = append(flags, cacheFlags(cfg, state)...)
		flags = append(flags, registrycache.PluginFlags(cfg)...)
	}
	if opts.includeEventBus {
		flags = append(flags, eventBusFlags(cfg)...)
		flags = append(flags, registryeventbus.PluginFlags(cfg)...)
	}
	if opts.includeAttachments {
		flags = append(flags, attachmentFlags(cfg)...)
		flags = append(flags, registryattach.PluginFlags(cfg)...)
	}
	if opts.includeEncryption {
		flags = append(flags, encryptionFlags(cfg)...)
		flags = append(flags, encrypt.PluginFlags(cfg)...)
	}
	if opts.includeVector {
		flags = append(flags, vectorFlags(cfg)...)
		flags = append(flags, registryvector.PluginFlags(cfg)...)
	}
	if opts.includeEmbedding {
		flags = append(flags, embeddingFlags(cfg)...)
		flags = append(flags, registryembed.PluginFlags(cfg)...)
	}
	if opts.includeAuthorization {
		flags = append(flags, authorizationFlags(cfg)...)
	}
	if opts.includeEpisodic {
		flags = append(flags, episodicFlags(cfg)...)
	}
	if opts.includeClustering {
		flags = append(flags, clusteringFlags(cfg)...)
	}
	if opts.includeMonitoring {
		flags = append(flags, monitoringFlags(cfg)...)
	}
	if opts.includeDeveloperFrontend {
		flags = append(flags, developerFrontendFlags(cfg)...)
	}
	return flags
}

func serverFlags(cfg *config.Config, state *FlagState) []cli.Flag {
	return []cli.Flag{
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
		&cli.BoolFlag{
			Name:        "tls-self-signed",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TLS_SELF_SIGNED"),
			Destination: &cfg.Listener.TLSSelfSigned,
			Usage:       "Generate an ephemeral self-signed certificate when TLS is enabled and cert/key files are not provided",
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
			Destination: &state.ReadHeaderTimeoutSecs,
			Value:       state.ReadHeaderTimeoutSecs,
			Usage:       "HTTP read header timeout in seconds",
		},
		&cli.IntFlag{
			Name:        "max-page-size",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MAX_PAGE_SIZE"),
			Destination: &cfg.MaxPageSize,
			Value:       cfg.MaxPageSize,
			Usage:       "Maximum items accepted by listing endpoints",
		},
		&cli.DurationFlag{
			Name:        "body-read-timeout",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_BODY_READ_TIMEOUT"),
			Destination: &cfg.BodyReadTimeout,
			Value:       cfg.BodyReadTimeout,
			Usage:       "Maximum time allowed to read ordinary REST request bodies (0 disables)",
		},
		&cli.DurationFlag{
			Name:        "attachment-body-read-timeout",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ATTACHMENT_BODY_READ_TIMEOUT"),
			Destination: &cfg.AttachmentBodyReadTimeout,
			Value:       cfg.AttachmentBodyReadTimeout,
			Usage:       "Maximum time allowed to read multipart attachment upload bodies (0 disables)",
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
			Name:        "management-on-main-listener",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER"),
			Destination: &cfg.ManagementOnMainListener,
			Usage:       "Explicitly serve management endpoints on the main API listener outside testing mode",
		},
		&cli.BoolFlag{
			Name:        "management-allow-non-loopback",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_ALLOW_NON_LOOPBACK"),
			Destination: &cfg.ManagementAllowNonLoopback,
			Usage:       "Explicitly allow the dedicated management listener to bind beyond loopback outside testing mode",
		},
		&cli.BoolFlag{
			Name:        "admin-require-justification",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ADMIN_REQUIRE_JUSTIFICATION"),
			Destination: &cfg.RequireJustification,
			Usage:       "Require justification for admin API calls",
		},
		&cli.StringFlag{
			Name:        "trusted-proxy-cidrs",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TRUSTED_PROXY_CIDRS"),
			Destination: &cfg.TrustedProxyCIDRs,
			Usage:       "Comma-separated trusted TCP proxy IPs/CIDRs for client-IP resolution; empty trusts none, /0 trusts all peers",
		},
		&cli.StringFlag{
			Name:        "rate-limit-mode",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_RATE_LIMIT_MODE"),
			Destination: &cfg.RateLimitMode,
			Value:       cfg.RateLimitMode,
			Usage:       "Process-local rate limiting mode: local or off",
		},
		&cli.StringFlag{
			Name:        "rate-limit-source",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_RATE_LIMIT_SOURCE"),
			Destination: &cfg.RateLimitSource,
			Value:       cfg.RateLimitSource,
			Usage:       "Source admission rate limit as <tokens>/<duration>,burst=<tokens>",
		},
		&cli.StringFlag{
			Name:        "rate-limit-identity",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_RATE_LIMIT_IDENTITY"),
			Destination: &cfg.RateLimitIdentity,
			Value:       cfg.RateLimitIdentity,
			Usage:       "Authenticated identity rate limit as <tokens>/<duration>,burst=<tokens>",
		},
		&cli.StringFlag{
			Name:        "rate-limit-auth-failure",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_RATE_LIMIT_AUTH_FAILURE"),
			Destination: &cfg.RateLimitAuthFailure,
			Value:       cfg.RateLimitAuthFailure,
			Usage:       "Authentication-failure rate limit as <tokens>/<duration>,burst=<tokens>",
		},
		&cli.StringFlag{
			Name:        "rate-limit-expensive",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_RATE_LIMIT_EXPENSIVE"),
			Destination: &cfg.RateLimitExpensive,
			Value:       cfg.RateLimitExpensive,
			Usage:       "Expensive-operation rate limit as <tokens>/<duration>,burst=<tokens>",
		},
		&cli.StringFlag{
			Name:        "rate-limit-stream-open",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_RATE_LIMIT_STREAM_OPEN"),
			Destination: &cfg.RateLimitStreamOpen,
			Value:       cfg.RateLimitStreamOpen,
			Usage:       "Stream-open rate limit as <tokens>/<duration>,burst=<tokens>",
		},
	}
}

func embeddedServerFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:        "max-page-size",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MAX_PAGE_SIZE"),
			Destination: &cfg.MaxPageSize,
			Value:       cfg.MaxPageSize,
			Usage:       "Maximum items accepted by listing endpoints",
		},
		&cli.StringFlag{
			Name:        "temp-dir",
			Category:    "Server:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TEMP_DIR"),
			Destination: &cfg.TempDir,
			Usage:       "Directory for temporary files; defaults to OS temp directory",
		},
	}
}

func listenerFlags(cfg *config.Config) []cli.Flag {
	flags := []cli.Flag{
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
		&cli.BoolFlag{
			Name:        "allow-non-loopback-plaintext",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ALLOW_NON_LOOPBACK_PLAINTEXT"),
			Destination: &cfg.AllowNonLoopbackPlainText,
			Value:       cfg.AllowNonLoopbackPlainText,
			Usage:       "Explicitly allow plaintext API traffic on a non-loopback TCP bind outside testing mode",
		},
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
		&cli.IntFlag{
			Name:        "max-header-bytes",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MAX_HEADER_BYTES"),
			Destination: &cfg.Listener.MaxHeaderBytes,
			Value:       cfg.Listener.MaxHeaderBytes,
			Usage:       "Maximum request header bytes accepted by the main HTTP server",
		},
		&cli.IntFlag{
			Name:        "management-max-header-bytes",
			Category:    "Network Listener: Management:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_MAX_HEADER_BYTES"),
			Destination: &cfg.ManagementListener.MaxHeaderBytes,
			Value:       cfg.ManagementListener.MaxHeaderBytes,
			Usage:       "Maximum request header bytes accepted by the management HTTP server",
		},
		&cli.DurationFlag{
			Name:        "idle-timeout",
			Category:    "Network Listener:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_IDLE_TIMEOUT"),
			Destination: &cfg.Listener.IdleTimeout,
			Value:       cfg.Listener.IdleTimeout,
			Usage:       "HTTP keep-alive idle timeout for the main listener",
		},
		&cli.DurationFlag{
			Name:        "management-idle-timeout",
			Category:    "Network Listener: Management:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_MANAGEMENT_IDLE_TIMEOUT"),
			Destination: &cfg.ManagementListener.IdleTimeout,
			Value:       cfg.ManagementListener.IdleTimeout,
			Usage:       "HTTP keep-alive idle timeout for the management listener",
		},
	}
	flags = append(flags, tcpListenerFlags(cfg)...)
	flags = append(flags, udsListenerFlags(cfg)...)
	flags = append(flags,
		&cli.StringFlag{Name: "unix-socket-auth", Category: "Network Listener:", Sources: cli.EnvVars("MEMORY_SERVICE_UNIX_SOCKET_AUTH"), Destination: &cfg.UnixSocketAuth, Value: cfg.UnixSocketAuth, Usage: "Unix socket authentication mode (credentials|local)"},
		&cli.StringFlag{Name: "local-user-id", Category: "Network Listener:", Sources: cli.EnvVars("MEMORY_SERVICE_LOCAL_USER_ID"), Destination: &cfg.LocalUserID, Usage: "User ID for local Unix socket authentication (defaults to the OS username)"},
		&cli.StringFlag{Name: "local-client-id", Category: "Network Listener:", Sources: cli.EnvVars("MEMORY_SERVICE_LOCAL_CLIENT_ID"), Destination: &cfg.LocalClientID, Value: cfg.LocalClientID, Usage: "Client ID for local Unix socket authentication"},
	)
	return flags
}

func databaseFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
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
	}
}

func cacheFlags(cfg *config.Config, state *FlagState) []cli.Flag {
	return []cli.Flag{
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
			Destination: &state.CacheLocalMaxBytes,
			Usage:       "Process-local memory cache budget (for example 64M, 512K, 1G)",
		},
		&cli.IntFlag{
			Name:        "cache-local-num-counters",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_CACHE_LOCAL_NUM_COUNTERS"),
			Destination: &state.CacheLocalNumCounters,
			Value:       state.CacheLocalNumCounters,
			Usage:       "Ristretto counter count for the process-local cache",
		},
		&cli.IntFlag{
			Name:        "cache-local-buffer-items",
			Category:    "Cache:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_CACHE_LOCAL_BUFFER_ITEMS"),
			Destination: &state.CacheLocalBufferItems,
			Value:       state.CacheLocalBufferItems,
			Usage:       "Ristretto buffer size for the process-local cache",
		},
	}
}

func eventBusFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
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
	}
}

func attachmentFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
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
	}
}

func encryptionFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
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
			Value:       cfg.EncryptionDBDisabled,
			Usage:       "Disable at-rest encryption for the database even when encryption is configured",
		},
		&cli.BoolFlag{
			Name:        "encryption-attachments-disabled",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_ATTACHMENTS_DISABLED"),
			Destination: &cfg.EncryptionAttachmentsDisabled,
			Value:       cfg.EncryptionAttachmentsDisabled,
			Usage:       "Disable at-rest encryption for the attachment store even when encryption is configured",
		},
		&cli.BoolFlag{
			Name:        "encryption-allow-plain",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN"),
			Destination: &cfg.EncryptionAllowPlain,
			Value:       cfg.EncryptionAllowPlain,
			Usage:       "Explicitly allow the plain provider as the primary provider outside testing (unsafe)",
		},
		&cli.BoolFlag{
			Name:        "encryption-legacy-plain-read-enabled",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_LEGACY_PLAIN_READ_ENABLED"),
			Destination: &cfg.EncryptionLegacyPlainReadEnabled,
			Usage:       "Permit headerless legacy plaintext reads when plain is registered as a fallback provider",
		},
		&cli.BoolFlag{
			Name:        "encryption-legacy-byte-v1-read-enabled",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_LEGACY_BYTE_V1_READ_ENABLED"),
			Destination: &cfg.EncryptionLegacyByteV1ReadEnabled,
			Value:       cfg.EncryptionLegacyByteV1ReadEnabled,
			Usage:       "Permit legacy MSEH v1 byte-encrypted field reads during migration",
		},
		&cli.BoolFlag{
			Name:        "encryption-legacy-stream-v2-read-enabled",
			Category:    "Encryption:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_LEGACY_STREAM_V2_READ_ENABLED"),
			Destination: &cfg.EncryptionLegacyStreamV2ReadEnabled,
			Value:       cfg.EncryptionLegacyStreamV2ReadEnabled,
			Usage:       "Permit legacy MSEH v2 AES-CTR attachment stream reads during migration",
		},
		&cli.StringFlag{
			Name:        "encryption-dek-key",
			Category:    "Encryption: DEK:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_ENCRYPTION_DEK_KEY", "MEMORY_SERVICE_ENCRYPTION_KEY"),
			Destination: &cfg.EncryptionKey,
			Usage:       "Comma-separated AES keys for the 'dek' provider (hex or base64, 32 bytes). First is primary; additional keys are legacy (decryption-only key rotation). Also derives attachment URL signing keys.",
		},
	}
}

func vectorFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
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
	}
}

func embeddingFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "embedding-kind",
			Category:    "Embedding:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EMBEDDING_KIND"),
			Destination: &cfg.EmbedType,
			Value:       cfg.EmbedType,
			Usage:       "Embedding provider (" + strings.Join(registryembed.Names(), "|") + ")",
		},
	}
}

func authorizationFlags(cfg *config.Config) []cli.Flag {
	flags := []cli.Flag{
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
		&cli.BoolFlag{
			Name:        "oidc-tls-insecure-skip-verify",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_OIDC_TLS_INSECURE_SKIP_VERIFY"),
			Destination: &cfg.OIDCTLSSkipCertificateVerify,
			Usage:       "Skip TLS certificate verification for OIDC discovery and JWKS requests (unsafe; for self-signed development issuers)",
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
		&cli.StringSliceFlag{
			Name:        "oidc-role-claim",
			Category:    "Authorization:",
			Destination: &cfg.OIDCRoleClaims,
			Usage:       "Repeatable RFC 6901 JSON Pointer to a string or string-array OIDC role claim; MEMORY_SERVICE_OIDC_ROLE_CLAIMS accepts a JSON array",
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
		&cli.StringFlag{
			Name:        "trusted-user-id-clients",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS"),
			Destination: &cfg.TrustedUserIDClients,
			Usage:       "Comma-separated exact client IDs trusted to assert X-User-ID on normal user APIs",
		},
		&cli.StringFlag{
			Name:        "oidc-allowed-clients",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_OIDC_ALLOWED_CLIENTS"),
			Destination: &cfg.OIDCAllowedClients,
			Usage:       "Comma-separated OIDC client IDs allowed to call memory-service",
		},
		&cli.StringFlag{
			Name:        "oidc-allowed-audiences",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES"),
			Destination: &cfg.OIDCAllowedAudiences,
			Usage:       "Comma-separated OIDC audiences accepted by memory-service",
		},
		&cli.BoolFlag{
			Name:        "oidc-allow-missing-audience",
			Category:    "Authorization:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_OIDC_ALLOW_MISSING_AUDIENCE"),
			Destination: &cfg.OIDCAllowMissingAudience,
			Usage:       "Temporarily allow issuer-only OIDC validation without configured audiences (unsafe compatibility mode)",
		},
	}
	for _, desc := range security.PermissionDescriptors() {
		desc := desc
		flags = append(flags, &cli.StringFlag{
			Name:     desc.FlagName,
			Category: "Authorization:",
			Sources:  cli.EnvVars(desc.EnvVar),
			Usage:    desc.Usage,
			Action: func(_ context.Context, _ *cli.Command, value string) error {
				if cfg.OIDCScopes == nil {
					cfg.OIDCScopes = map[string]string{}
				}
				cfg.OIDCScopes[string(desc.Permission)] = value
				return nil
			},
		})
	}
	return flags
}

func episodicFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
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
			Usage:       "Max archived rows processed per episodic eviction pass",
		},
		&cli.DurationFlag{
			Name:        "episodic-tombstone-retention",
			Category:    "Episodic Memory:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_EPISODIC_TOMBSTONE_RETENTION"),
			Destination: &cfg.EpisodicTombstoneRetention,
			Value:       cfg.EpisodicTombstoneRetention,
			Usage:       "How long to retain delete/expired tombstones for event history (default 90d)",
		},
	}
}

func clusteringFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:        "knowledge-clustering-enabled",
			Category:    "Knowledge Clustering:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_ENABLED"),
			Destination: &cfg.KnowledgeClusteringEnabled,
			Value:       cfg.KnowledgeClusteringEnabled,
			Usage:       "Enable adaptive knowledge clustering on embeddings",
		},
		&cli.Float64Flag{
			Name:        "knowledge-clustering-epsilon",
			Category:    "Knowledge Clustering:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_EPSILON"),
			Destination: &cfg.KnowledgeClusteringEpsilon,
			Value:       cfg.KnowledgeClusteringEpsilon,
			Usage:       "DBSCAN neighborhood radius in cosine distance (default 0.3)",
		},
		&cli.IntFlag{
			Name:        "knowledge-clustering-min-points",
			Category:    "Knowledge Clustering:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_MIN_POINTS"),
			Destination: &cfg.KnowledgeClusteringMinPts,
			Value:       cfg.KnowledgeClusteringMinPts,
			Usage:       "DBSCAN minimum points to form a cluster (default 3)",
		},
		&cli.DurationFlag{
			Name:        "knowledge-clustering-decay",
			Category:    "Knowledge Clustering:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_KNOWLEDGE_CLUSTERING_DECAY"),
			Destination: &cfg.KnowledgeClusteringDecay,
			Value:       cfg.KnowledgeClusteringDecay,
			Usage:       "Time with no new members before cluster trend becomes decaying (default 30d)",
		},
	}
}

func monitoringFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
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

func developerFrontendFlags(cfg *config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:        "developer-frontend-enabled",
			Category:    "Developer Frontend:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DEVELOPER_FRONTEND_ENABLED"),
			Destination: &cfg.DeveloperFrontendEnabled,
			Value:       cfg.DeveloperFrontendEnabled,
			Usage:       "Enable serving the developer frontend SPA under /developer",
		},
		&cli.StringFlag{
			Name:        "developer-frontend-dir",
			Category:    "Developer Frontend:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DEVELOPER_FRONTEND_DIR"),
			Destination: &cfg.DeveloperFrontendDir,
			Usage:       "Directory containing built developer frontend assets",
		},
		&cli.StringFlag{
			Name:        "developer-frontend-client-id",
			Category:    "Developer Frontend:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DEVELOPER_FRONTEND_CLIENT_ID"),
			Destination: &cfg.DeveloperFrontendClientID,
			Value:       cfg.DeveloperFrontendClientID,
			Usage:       "Client ID used by the developer frontend",
		},
		&cli.StringFlag{
			Name:        "developer-frontend-auth-mode",
			Category:    "Developer Frontend:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DEVELOPER_FRONTEND_AUTH_MODE"),
			Destination: &cfg.DeveloperFrontendAuthMode,
			Value:       cfg.DeveloperFrontendAuthMode,
			Usage:       "Developer frontend authentication mode: oidc or api-key",
		},
		&cli.StringFlag{
			Name:        "developer-frontend-api-key",
			Category:    "Developer Frontend:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_DEVELOPER_FRONTEND_API_KEY"),
			Destination: &cfg.DeveloperFrontendAPIKey,
			Usage:       "Browser-visible API key for the developer frontend in api-key mode",
		},
		&cli.StringFlag{
			Name:        "base-url",
			Category:    "Developer Frontend:",
			Sources:     cli.EnvVars("MEMORY_SERVICE_BASE_URL"),
			Destination: &cfg.BaseURL,
			Usage:       "External base URL for redirects and runtime config; defaults to advertised address or listener",
		},
	}
}

func ApplyParsedFlags(cfg *config.Config, cmd *cli.Command, state *FlagState, validateListeners bool) error {
	if err := cfg.ApplyJavaCompatFromEnv(); err != nil {
		return err
	}
	// Let plugins apply post-parse transformations (e.g. env var forwarding).
	registrystore.ApplyAll(cfg, cmd)
	registrycache.ApplyAll(cfg, cmd)
	registryvector.ApplyAll(cfg, cmd)
	registryembed.ApplyAll(cfg, cmd)
	registryattach.ApplyAll(cfg, cmd)
	registryeventbus.ApplyAll(cfg, cmd)
	encrypt.ApplyAll(cfg, cmd)
	if strings.TrimSpace(state.CacheLocalMaxBytes) != "" {
		size, err := config.ParseMemorySize(state.CacheLocalMaxBytes)
		if err != nil {
			return err
		}
		cfg.CacheLocalMaxBytes = size
	}
	if cmd.IsSet("cache-local-num-counters") {
		cfg.CacheLocalNumCounters = int64(state.CacheLocalNumCounters)
	}
	if cmd.IsSet("cache-local-buffer-items") {
		cfg.CacheLocalBufferItems = int64(state.CacheLocalBufferItems)
	}
	cfg.Listener.ReadHeaderTimeout = time.Duration(state.ReadHeaderTimeoutSecs) * time.Second
	cfg.ManagementListener.ReadHeaderTimeout = cfg.Listener.ReadHeaderTimeout
	cfg.AttachTypeExplicit = cmd.IsSet("attachments-kind")
	if cfg.MaxPageSize <= 0 {
		return fmt.Errorf("max-page-size must be greater than zero")
	}
	if err := security.ValidateRateLimitConfig(cfg); err != nil {
		return err
	}

	if !validateListeners {
		cfg.ManagementListenerEnabled = false
		return nil
	}

	selections := listenerSelections{
		mainPortExplicit:       cmd.IsSet("port"),
		mainUnixSocketExplicit: cmd.IsSet("unix-socket"),
		mgmtPortExplicit:       cmd.IsSet("management-port"),
		mgmtUnixSocketExplicit: cmd.IsSet("management-unix-socket"),
	}
	if err := validateListenerSelections(*cfg, selections); err != nil {
		return err
	}
	if cfg.ManagementOnMainListener && (selections.mgmtPortExplicit || selections.mgmtUnixSocketExplicit) {
		return fmt.Errorf("--management-on-main-listener cannot be combined with --management-port or --management-unix-socket")
	}
	if err := resolveUnixSocketAuth(cfg); err != nil {
		return err
	}
	cfg.ManagementListenerEnabled = selections.mgmtPortExplicit || selections.mgmtUnixSocketExplicit
	return nil
}

var currentOSUser = user.Current

func resolveUnixSocketAuth(cfg *config.Config) error {
	mode := strings.TrimSpace(cfg.UnixSocketAuth)
	if mode == "" {
		mode = "credentials"
	}
	if mode != "credentials" && mode != "local" {
		return fmt.Errorf("--unix-socket-auth must be credentials or local")
	}
	cfg.UnixSocketAuth = mode
	if mode != "local" {
		return nil
	}
	if strings.TrimSpace(cfg.Listener.UnixSocket) == "" {
		return fmt.Errorf("--unix-socket-auth=local requires the API to be exposed exclusively through --unix-socket")
	}
	if strings.TrimSpace(cfg.LocalClientID) == "" {
		cfg.LocalClientID = "local-agent"
	}
	if strings.TrimSpace(cfg.LocalUserID) == "" {
		u, err := currentOSUser()
		if err != nil || u == nil || strings.TrimSpace(u.Username) == "" {
			return fmt.Errorf("resolve OS username for --unix-socket-auth=local: set --local-user-id explicitly")
		}
		cfg.LocalUserID = u.Username
	}
	return nil
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

var errRequestBodyTimeout = errors.New("request body read timeout")

func bodyReadTimeoutMiddleware(bodyTimeout, attachmentBodyTimeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.Body == nil || c.Request.Body == http.NoBody {
			c.Next()
			return
		}
		timeout := bodyTimeout
		if isStreamingRequest(c.Request) {
			timeout = attachmentBodyTimeout
		}
		if timeout <= 0 {
			c.Next()
			return
		}

		body := newTimeoutReadCloser(c.Request.Body, timeout)
		c.Request.Body = body
		c.Next()
		body.Stop()
		if body.TimedOut() && !c.Writer.Written() {
			c.AbortWithStatusJSON(http.StatusRequestTimeout, gin.H{
				"code":  "request_timeout",
				"error": "request body read timeout",
			})
		}
	}
}

type timeoutReadCloser struct {
	body     io.ReadCloser
	timer    *time.Timer
	timedOut atomic.Bool
}

func newTimeoutReadCloser(body io.ReadCloser, timeout time.Duration) *timeoutReadCloser {
	wrapped := &timeoutReadCloser{body: body}
	wrapped.timer = time.AfterFunc(timeout, func() {
		wrapped.timedOut.Store(true)
		_ = body.Close()
	})
	return wrapped
}

func (b *timeoutReadCloser) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err != nil && b.timedOut.Load() {
		return n, errRequestBodyTimeout
	}
	return n, err
}

func (b *timeoutReadCloser) Close() error {
	b.Stop()
	return b.body.Close()
}

func (b *timeoutReadCloser) Stop() {
	if b.timer != nil {
		b.timer.Stop()
	}
}

func (b *timeoutReadCloser) TimedOut() bool {
	return b.timedOut.Load()
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

func maxPageSizeMiddleware(cfg *config.Config) gin.HandlerFunc {
	maxPageSize := cfg.EffectiveMaxPageSize()
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(config.WithContext(c.Request.Context(), cfg))
		raw := strings.TrimSpace(c.Query("limit"))
		if raw == "" {
			c.Next()
			return
		}
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > maxPageSize {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("limit must be between 1 and %d", maxPageSize),
			})
			return
		}
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
