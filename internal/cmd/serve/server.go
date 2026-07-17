package serve

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/episodic"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	grpcserver "github.com/chirino/memory-service/internal/grpc"
	"github.com/chirino/memory-service/internal/knowledge"
	"github.com/chirino/memory-service/internal/plugin/attach/encrypt"
	routedeveloper "github.com/chirino/memory-service/internal/plugin/route/developer"
	routeknowledge "github.com/chirino/memory-service/internal/plugin/route/knowledge"
	routesystem "github.com/chirino/memory-service/internal/plugin/route/system"
	storemetrics "github.com/chirino/memory-service/internal/plugin/store/metrics"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registryroute "github.com/chirino/memory-service/internal/registry/route"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	internalresumer "github.com/chirino/memory-service/internal/resumer"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/service"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server holds the running server and its subsystems.
type Server struct {
	Config          *config.Config
	Store           registrystore.MemoryStore
	Router          *gin.Engine
	GRPCServer      *grpc.Server
	Running         *RunningServers
	TokenResolver   *security.TokenResolver
	closeManagement func(context.Context) error
}

// GetTokenResolver returns the TokenResolver used by this server, or nil if not yet built.
// Used by embedded MCP to call ConfigureEmbeddedMCP after BuildServer.
func GetTokenResolver(s *Server) *security.TokenResolver {
	if s == nil {
		return nil
	}
	return s.TokenResolver
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.closeManagement != nil {
		_ = s.closeManagement(ctx)
	}
	if s.Running != nil {
		return s.Running.Close(ctx)
	}
	return nil
}

func resolveAttachmentStoreName(cfg *config.Config) (string, error) {
	return config.ResolveAttachmentStoreName(cfg)
}

// BuildServer initializes all subsystems without binding any network listeners.
func BuildServer(ctx context.Context, cfg *config.Config) (*Server, error) {
	// Initialize Prometheus metrics before startup validation because validation
	// can record unsafe-config acknowledgements. BDD starts scenario servers
	// concurrently under the race detector, so all goroutines must pass through
	// the metrics sync.Once before any validation path reads metric collectors.
	metricsLabels, err := security.ParseMetricsLabels(cfg.MetricsLabels)
	if err != nil {
		return nil, fmt.Errorf("invalid --metrics-labels: %w", err)
	}
	security.InitMetrics(metricsLabels)

	if err := validateStartupConfig(cfg); err != nil {
		return nil, err
	}
	if err := resolveUnixSocketAuth(cfg); err != nil {
		return nil, err
	}
	log.Info("Initializing memory service",
		"httpPort", cfg.Listener.Port,
		"httpSocket", strings.TrimSpace(cfg.Listener.UnixSocket),
		"db", cfg.DatastoreType,
		"cache", cfg.CacheType,
		"vector", cfg.VectorType,
		"embedding", cfg.EmbedType,
	)

	// Initialize embedder early so vector store migrations can use the detected dimension
	var embedder registryembed.Embedder
	if cfg.SearchSemanticEnabled && cfg.EmbedType != "" && cfg.EmbedType != "none" {
		embedLoader, err := registryembed.Select(cfg.EmbedType)
		if err != nil {
			log.Warn("Embedder not available", "err", err)
		} else {
			embedder, err = embedLoader(ctx)
			if err != nil {
				log.Warn("Failed to initialize embedder", "err", err)
			} else if embedder != nil {
				// Update config with detected dimension for vector store initialization
				if cfg.OpenAIDimensions <= 0 {
					cfg.OpenAIDimensions = embedder.Dimension()
					log.Info("Auto-detected embedding dimension", "dimension", cfg.OpenAIDimensions)
					// Update the context with the modified config so migrations see the detected dimension
					ctx = config.WithContext(ctx, cfg)
				}
			}
		}
	}

	// Run migrations
	if err := registrymigrate.RunAll(ctx); err != nil {
		return nil, fmt.Errorf("migrations failed: %w", err)
	}

	// Initialize encryption service and inject into context so store loaders can read it.
	encSvc, err := dataencryption.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize encryption service: %w", err)
	}
	ctx = dataencryption.WithContext(ctx, encSvc)

	// S3 direct download bypasses the server, so encrypted attachments cannot be
	// decrypted on the fly. Fail fast when both options are active.
	if cfg.S3DirectDownload && encSvc.IsPrimaryReal() {
		return nil, fmt.Errorf("MEMORY_SERVICE_ATTACHMENTS_S3_DIRECT_DOWNLOAD=true is incompatible with encryption provider %q; S3 direct downloads bypass the server so attachments cannot be decrypted on the fly — disable direct download or use encryption-kind=plain", cfg.EncryptionProviders)
	}

	// Initialize cache and inject into context so store loaders can read it.
	if cacheLoader, err := registrycache.Select(cfg.CacheType); err != nil {
		log.Warn("Cache not available", "cache", cfg.CacheType, "err", err)
	} else if entriesCache, err := cacheLoader(ctx); err != nil {
		log.Warn("Failed to initialize cache", "cache", cfg.CacheType, "err", err)
	} else {
		ctx = registrycache.WithEntriesCacheContext(ctx, entriesCache)
	}

	// Initialize event bus
	eventBusLoader, err := registryeventbus.Select(cfg.EventBusType)
	if err != nil {
		return nil, fmt.Errorf("failed to select event bus: %w", err)
	}
	eventBus, err := eventBusLoader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize event bus: %w", err)
	}

	// Initialize store
	storeLoader, err := registrystore.Select(cfg.DatastoreType)
	if err != nil {
		return nil, err
	}
	store, err := storeLoader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}
	store = storemetrics.Wrap(store)

	// Set up gin
	gin.SetMode(gin.ReleaseMode)
	router := newGinRouter()
	trustedProxies, err := parseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	if err := router.SetTrustedProxies(trustedProxies); err != nil {
		return nil, fmt.Errorf("failed to configure trusted proxies: %w", err)
	}
	rateLimiter, err := security.NewRateLimiter(cfg)
	if err != nil {
		return nil, fmt.Errorf("rate limit configuration error: %w", err)
	}
	router.Use(security.RequestIDMiddleware())
	router.Use(security.ErrorEnvelopeMiddleware())
	router.Use(security.SourceRateLimitMiddleware(rateLimiter))
	router.Use(gin.Recovery())
	router.Use(securityHeadersMiddleware())
	if cfg.ManagementAccessLog {
		router.Use(security.AccessLogMiddleware())
	} else {
		router.Use(security.AccessLogMiddleware("/health", "/ready", "/metrics"))
	}
	router.Use(security.MetricsMiddleware())
	router.Use(security.AdminAuditMiddleware(cfg.RequireJustification))
	router.Use(bodyReadTimeoutMiddleware(cfg.BodyReadTimeout, cfg.AttachmentBodyReadTimeout))
	router.Use(maxBodySizeMiddleware(cfg.MaxBodySize))
	router.Use(maxPageSizeMiddleware(cfg))
	if cfg.CORSEnabled {
		router.Use(corsMiddleware(cfg.CORSOrigins))
	}

	// Initialize attachment store (optional).
	// "db" is a Java-parity alias: resolve to the store matching the configured datastore.
	var attachStore registryattach.AttachmentStore
	attachStoreName, err := resolveAttachmentStoreName(cfg)
	if err != nil {
		return nil, err
	}
	if attachStoreName != "" {
		attachLoader, err := registryattach.Select(attachStoreName)
		if err != nil {
			log.Warn("Attachment store not available", "err", err)
		} else {
			attachStore, err = attachLoader(ctx)
			if err != nil {
				log.Warn("Failed to initialize attachment store", "err", err)
			}
		}
	}
	// Wrap with encryption when the primary provider is real (not plain) and attachment
	// encryption is not explicitly disabled.
	if attachStore != nil && encSvc.IsPrimaryReal() && !cfg.EncryptionAttachmentsDisabled {
		attachStore, err = encrypt.Wrap(attachStore, encSvc)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize attachment encryption: %w", err)
		}
	}

	// Initialize vector store (optional, for semantic search)
	// Note: embedder was already initialized earlier before migrations
	var vectorStore registryvector.VectorStore
	if cfg.VectorType == "sqlite" && cfg.DatastoreType != "sqlite" {
		return nil, fmt.Errorf("vector-kind=%q requires db-kind=sqlite", cfg.VectorType)
	}
	if cfg.SearchSemanticEnabled && cfg.VectorType != "" && cfg.VectorType != "none" {
		if embedder == nil {
			return nil, fmt.Errorf("vector store %q requires an embedding provider: set --embedding-kind to a value other than 'none'", cfg.VectorType)
		}
		vectorLoader, err := registryvector.Select(cfg.VectorType)
		if err != nil {
			log.Warn("Vector store not available", "err", err)
		} else {
			vectorStore, err = vectorLoader(ctx)
			if err != nil {
				log.Warn("Failed to initialize vector store", "err", err)
			}
		}
	}

	// Initialize response recorder store and cache-backed locator resolution.
	locatorStore, err := internalresumer.NewLocatorStore(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize response recorder locator store: %w", err)
	}
	resumerEnabled := locatorStore.Available()
	resumer := internalresumer.NewTempFileStore(cfg.ResolvedTempDir(), cfg.ResumerTempFileRetention, locatorStore)

	// Create shared token resolver and auth middleware.
	resolver, err := security.NewTokenResolver(cfg)
	if err != nil {
		return nil, fmt.Errorf("auth configuration error: %w", err)
	}
	auth := security.AuthMiddlewareWithAuthFailureRateLimiter(resolver, rateLimiter)
	authenticatedRateLimit := security.AuthenticatedRateLimitMiddleware(rateLimiter)
	userIDAsserter := security.NewUserIDAsserter(cfg.TrustedUserIDClients)
	// Store resolver on Server so callers like embedded MCP can configure it after build.
	var builtServer Server
	builtServer.TokenResolver = resolver

	// Initialize and mount episodic memory store + routes.
	episodicStore, episodicPolicy, err := initEpisodic(ctx, cfg)
	if err != nil {
		return nil, err
	}
	episodicTTL := service.NewEpisodicTTLService(episodicStore, cfg.EpisodicTTLInterval, cfg.EpisodicEvictionBatchSize, cfg.EpisodicTombstoneRetention)
	episodicIdx := service.NewEpisodicIndexer(episodicStore, embedder, cfg.EpisodicIndexingInterval, cfg.EpisodicIndexingBatchSize)

	attachSigningKeys, signingKeysErr := encSvc.AttachmentSigningKeys(ctx)
	if signingKeysErr != nil {
		log.Warn("Attachment signing keys unavailable; signed download URLs disabled", "err", signingKeysErr)
	}

	// Register generated wrappers on the public router.
	registerAPIRoutes(router, auth, authenticatedRateLimit, userIDAsserter.HTTPMiddleware(), cfg, store, attachStore, attachSigningKeys, embedder, vectorStore, resumer, resumerEnabled, episodicStore, episodicPolicy, episodicIdx, eventBus)

	// Register developer frontend routes when enabled.
	if cfg.DeveloperFrontendEnabled {
		if err := routedeveloper.RegisterRoutes(router, cfg); err != nil {
			return nil, fmt.Errorf("failed to register developer frontend routes: %w", err)
		}
		log.Info("Developer frontend enabled", "path", "/developer", "dir", cfg.DeveloperFrontendDir)
	}

	if starter, ok := store.(interface {
		StartOutboxRelay(context.Context, registryeventbus.EventBus) error
	}); ok {
		if err := starter.StartOutboxRelay(ctx, eventBus); err != nil {
			return nil, fmt.Errorf("Failed to start outbox relay: %w", err)
		}
	}

	// Start background services
	indexer := service.NewBackgroundIndexer(store, embedder, vectorStore, cfg.VectorIndexerBatchSize)
	go indexer.Start(ctx)

	evictionSvc := service.NewEvictionService(store, eventBus, cfg.EvictionBatchSize, cfg.EvictionBatchDelay)
	go evictionSvc.Start(ctx)

	taskProc := service.NewTaskProcessor(store, vectorStore)
	go taskProc.Start(ctx)

	attachmentCleanup := service.NewAttachmentCleanupService(store, attachStore, cfg.AttachmentCleanupInterval)
	go attachmentCleanup.Start(ctx)

	go episodicTTL.Start(ctx)

	go episodicIdx.Start(ctx)

	// Set up knowledge clustering (if enabled). Clustering runs inside the
	// BackgroundIndexer after each embedding batch — no separate goroutine.
	if cfg.KnowledgeClusteringEnabled && cfg.DatastoreType == "postgres" && cfg.DBURL != "" && cfg.VectorType == "pgvector" && vectorStore != nil && vectorStore.IsEnabled() {
		knowledgeStore, err := knowledge.OpenPostgresKnowledgeStore(cfg.DBURL)
		if err != nil {
			log.Warn("Knowledge clustering: failed to open store", "err", err)
		} else {
			clusterer := knowledge.NewClusterer(
				knowledgeStore,
				cfg.KnowledgeClusteringDecay,
				10, // keywords per cluster
				knowledge.DBSCANConfig{
					Epsilon:   cfg.KnowledgeClusteringEpsilon,
					MinPoints: cfg.KnowledgeClusteringMinPts,
				},
			)
			indexer.SetClusterer(clusterer)

			// Register knowledge REST routes.
			knowledgeHandler := &routeknowledge.Handler{
				Store:     knowledgeStore,
				Clusterer: clusterer,
			}
			knowledgeHandler.RegisterRoutes(router, auth, authenticatedRateLimit)

			log.Info("Knowledge clustering enabled",
				"epsilon", cfg.KnowledgeClusteringEpsilon,
				"minPts", cfg.KnowledgeClusteringMinPts,
			)
		}
	}

	// Set up gRPC server with auth interceptors.
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			security.GRPCRequestIDUnaryInterceptor(),
			security.GRPCSourceRateLimitUnaryInterceptor(rateLimiter),
			security.GRPCUnaryInterceptorWithRateLimiter(resolver, rateLimiter),
			userIDAsserter.GRPCUnaryInterceptor(),
			security.GRPCIdentityRateLimitUnaryInterceptor(rateLimiter),
			maxPageSizeUnaryInterceptor(cfg),
		),
		grpc.ChainStreamInterceptor(
			security.GRPCRequestIDStreamInterceptor(),
			security.GRPCSourceRateLimitStreamInterceptor(rateLimiter),
			security.GRPCStreamInterceptorWithRateLimiter(resolver, rateLimiter),
			userIDAsserter.GRPCStreamInterceptor(),
			security.GRPCIdentityRateLimitStreamInterceptor(rateLimiter),
		),
	)
	pb.RegisterSystemServiceServer(grpcServer, &grpcserver.SystemServer{Config: cfg})
	pb.RegisterConversationsServiceServer(grpcServer, &grpcserver.ConversationsServer{Store: store, EventBus: eventBus})
	pb.RegisterEntriesServiceServer(grpcServer, &grpcserver.EntriesServer{Store: store, EventBus: eventBus})
	pb.RegisterAdminEntriesServiceServer(grpcServer, &grpcserver.AdminEntriesServer{Store: store})
	pb.RegisterAdminConversationsServiceServer(grpcServer, &grpcserver.AdminConversationsServer{Store: store, Config: cfg})
	pb.RegisterConversationMembershipsServiceServer(grpcServer, &grpcserver.MembershipsServer{Store: store, EventBus: eventBus})
	pb.RegisterOwnershipTransfersServiceServer(grpcServer, &grpcserver.TransfersServer{Store: store})
	pb.RegisterSearchServiceServer(grpcServer, &grpcserver.SearchServer{Store: store, Config: cfg, Embedder: embedder, VectorStore: vectorStore})
	pb.RegisterMemoriesServiceServer(grpcServer, &grpcserver.MemoriesServer{
		Store:    episodicStore,
		Policy:   episodicPolicy,
		Config:   cfg,
		Embedder: embedder,
	})
	pb.RegisterAdminMemoriesServiceServer(grpcServer, &grpcserver.AdminMemoriesServer{
		Store:    episodicStore,
		Policy:   episodicPolicy,
		Config:   cfg,
		Embedder: embedder,
	})
	pb.RegisterAttachmentsServiceServer(grpcServer, &grpcserver.AttachmentsServer{
		Store:       store,
		AttachStore: attachStore,
		MaxBodySize: cfg.AttachmentMaxSize,
		Config:      cfg,
		SigningKeys: attachSigningKeys,
	})
	pb.RegisterResponseRecorderServiceServer(grpcServer, &grpcserver.ResponseRecorderServer{
		Resumer:  resumer,
		Store:    store,
		Config:   cfg,
		Enabled:  resumerEnabled,
		EventBus: eventBus,
	})
	pb.RegisterEventStreamServiceServer(grpcServer, &grpcserver.EventStreamServer{
		Store:          store,
		EventBus:       eventBus,
		Config:         cfg,
		UserIDAsserter: userIDAsserter,
		RateLimiter:    rateLimiter,
	})
	pb.RegisterAdminCheckpointServiceServer(grpcServer, &grpcserver.AdminCheckpointServer{Store: store})

	builtServer.Config = cfg
	builtServer.Store = store
	builtServer.Router = router
	builtServer.GRPCServer = grpcServer
	return &builtServer, nil
}

type grpcPageRequest interface {
	GetPage() *pb.PageRequest
}

type grpcLimitRequest interface {
	GetLimit() int32
}

func maxPageSizeUnaryInterceptor(cfg *config.Config) grpc.UnaryServerInterceptor {
	maxPageSize := cfg.EffectiveMaxPageSize()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = config.WithContext(ctx, cfg)
		if paged, ok := req.(grpcPageRequest); ok && paged.GetPage() != nil {
			requested := paged.GetPage().GetPageSize()
			if requested < 0 || int64(requested) > int64(maxPageSize) {
				return nil, status.Errorf(codes.InvalidArgument, "page size must be between 1 and %d", maxPageSize)
			}
		}
		if limited, ok := req.(grpcLimitRequest); ok && limited.GetLimit() != 0 {
			requested := limited.GetLimit()
			if requested < 0 || int64(requested) > int64(maxPageSize) {
				return nil, status.Errorf(codes.InvalidArgument, "page size must be between 1 and %d", maxPageSize)
			}
		}
		return handler(ctx, req)
	}
}

// StartServer initializes all subsystems and starts HTTP+gRPC on a single port.
// Use cfg.HTTPPort=0 for a random port. Actual port: Server.Running.Port.
func StartServer(ctx context.Context, cfg *config.Config) (*Server, error) {
	srv, err := BuildServer(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := validateManagementRouteExposure(cfg); err != nil {
		return nil, err
	}
	if err := validateNetworkTransportConfig(cfg); err != nil {
		return nil, err
	}

	if cfg.ManagementListenerEnabled {
		closeManagement, err := startManagementRoutes(cfg)
		if err != nil {
			return nil, err
		}
		srv.closeManagement = closeManagement
	} else {
		if err := loadManagementRoutes(srv.Router); err != nil {
			return nil, err
		}
	}

	// Start single-port HTTP+gRPC
	running, err := StartSinglePortHTTPAndGRPC(ctx, cfg.Listener, srv.Router, srv.GRPCServer)
	if err != nil {
		return nil, err
	}
	srv.Running = running

	log.Info("Server listening",
		"addr", srv.Running.Endpoint,
		"network", srv.Running.Network,
		"port", srv.Running.Port,
		"plaintext", cfg.Listener.EnablePlainText,
		"tls", cfg.Listener.EnableTLS,
	)

	routesystem.MarkReady()
	return srv, nil
}

func startManagementRoutes(cfg *config.Config) (func(context.Context) error, error) {
	if !cfg.ManagementListenerEnabled {
		return nil, nil
	}

	mgmtRouter := newGinRouter()
	if err := mgmtRouter.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("failed to configure management trusted proxies: %w", err)
	}
	mgmtRouter.Use(security.RequestIDMiddleware())
	mgmtRouter.Use(security.ErrorEnvelopeMiddleware())
	mgmtRouter.Use(gin.Recovery())
	mgmtRouter.Use(securityHeadersMiddleware())
	if cfg.ManagementAccessLog {
		mgmtRouter.Use(security.AccessLogMiddleware())
	}
	if err := loadManagementRoutes(mgmtRouter); err != nil {
		return nil, err
	}
	// Management listener shares TLS cert/key with the main listener.
	mgmtCfg := cfg.ManagementListener
	mgmtCfg.TLSCertFile = cfg.Listener.TLSCertFile
	mgmtCfg.TLSKeyFile = cfg.Listener.TLSKeyFile
	mgmtCfg.TLSSelfSigned = cfg.Listener.TLSSelfSigned
	_, closeManagement, err := startManagementServer(mgmtCfg, mgmtRouter)
	if err != nil {
		return nil, fmt.Errorf("failed to start management server: %w", err)
	}
	return closeManagement, nil
}

func loadManagementRoutes(router *gin.Engine) error {
	for _, loader := range registryroute.ManagementRouteLoaders() {
		if err := loader(router); err != nil {
			return fmt.Errorf("failed to load management routes: %w", err)
		}
	}
	return nil
}

// initEpisodic initializes the episodic memory store and OPA policy engine.
// Returns nil, nil, nil when the episodic store is not available for the configured datastore.
func initEpisodic(ctx context.Context, cfg *config.Config) (registryepisodic.EpisodicStore, *episodic.PolicyEngine, error) {
	loader, err := registryepisodic.Select(cfg.DatastoreType)
	if err != nil {
		log.Warn("Episodic store not available for datastore", "datastore", cfg.DatastoreType, "err", err)
		return nil, nil, nil
	}
	eStore, err := loader(ctx)
	if err != nil {
		log.Warn("Failed to initialize episodic store", "err", err)
		return nil, nil, nil
	}

	policy, err := episodic.NewPolicyEngine(ctx, cfg.EpisodicPolicyDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize episodic OPA policy engine: %w", err)
	}
	return eStore, policy, nil
}
