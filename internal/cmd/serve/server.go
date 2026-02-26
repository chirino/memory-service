package serve

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	grpcserver "github.com/chirino/memory-service/internal/grpc"
	"github.com/chirino/memory-service/internal/plugin/attach/encrypt"
	"github.com/chirino/memory-service/internal/plugin/route/admin"
	"github.com/chirino/memory-service/internal/plugin/route/attachments"
	"github.com/chirino/memory-service/internal/plugin/route/conversations"
	"github.com/chirino/memory-service/internal/plugin/route/entries"
	"github.com/chirino/memory-service/internal/plugin/route/memberships"
	"github.com/chirino/memory-service/internal/plugin/route/search"
	routesystem "github.com/chirino/memory-service/internal/plugin/route/system"
	"github.com/chirino/memory-service/internal/plugin/route/transfers"
	storemetrics "github.com/chirino/memory-service/internal/plugin/store/metrics"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registryroute "github.com/chirino/memory-service/internal/registry/route"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	internalresumer "github.com/chirino/memory-service/internal/resumer"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/service"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// Server holds the running server and its subsystems.
type Server struct {
	Config          *config.Config
	Store           registrystore.MemoryStore
	Router          *gin.Engine
	GRPCServer      *grpc.Server
	Running         *RunningServers
	closeManagement func(context.Context) error
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.closeManagement != nil {
		_ = s.closeManagement(ctx)
	}
	return s.Running.Close(ctx)
}

// StartServer initializes all subsystems and starts HTTP+gRPC on a single port.
// Use cfg.HTTPPort=0 for a random port. Actual port: Server.Running.Port.
func StartServer(ctx context.Context, cfg *config.Config) (*Server, error) {
	log.Info("Starting memory service",
		"httpPort", cfg.Listener.Port,
		"db", cfg.DatastoreType,
		"cache", cfg.CacheType,
		"vector", cfg.VectorType,
		"embedding", cfg.EmbedType,
	)

	// Initialize Prometheus metrics with configured constant labels.
	metricsLabels, err := security.ParseMetricsLabels(cfg.MetricsLabels)
	if err != nil {
		return nil, fmt.Errorf("invalid --metrics-labels: %w", err)
	}
	security.InitMetrics(metricsLabels)

	// Run migrations
	if err := registrymigrate.RunAll(ctx); err != nil {
		return nil, fmt.Errorf("migrations failed: %w", err)
	}

	// Initialize cache and inject into context so store loaders can read it.
	if cacheLoader, err := registrycache.Select(cfg.CacheType); err != nil {
		log.Warn("Cache not available", "cache", cfg.CacheType, "err", err)
	} else if entriesCache, err := cacheLoader(ctx); err != nil {
		log.Warn("Failed to initialize cache", "cache", cfg.CacheType, "err", err)
	} else {
		ctx = registrycache.WithEntriesCacheContext(ctx, entriesCache)
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
	router := gin.New()
	router.Use(gin.Recovery())
	if cfg.ManagementAccessLog {
		router.Use(security.AccessLogMiddleware())
	} else {
		router.Use(security.AccessLogMiddleware("/health", "/ready", "/metrics"))
	}
	router.Use(security.MetricsMiddleware())
	router.Use(security.AdminAuditMiddleware(cfg.RequireJustification))
	router.Use(maxBodySizeMiddleware(cfg.MaxBodySize))
	if cfg.CORSEnabled {
		router.Use(corsMiddleware(cfg.CORSOrigins))
	}

	// Mount main route plugins on the main router.
	for _, loader := range registryroute.MainRouteLoaders() {
		if err := loader(router); err != nil {
			return nil, fmt.Errorf("failed to load routes: %w", err)
		}
	}

	// Initialize attachment store (optional).
	// "db" is a Java-parity alias: resolve to the store matching the configured datastore.
	var attachStore registryattach.AttachmentStore
	attachStoreName := cfg.AttachType
	if attachStoreName == "db" {
		switch cfg.DatastoreType {
		case "mongo":
			attachStoreName = "mongo"
		default:
			attachStoreName = "postgres"
		}
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
	// Wrap with encryption if an encryption key is configured.
	if attachStore != nil && cfg.EncryptionKey != "" {
		attachStore, err = encrypt.Wrap(attachStore, cfg.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize attachment encryption: %w", err)
		}
	}

	// Initialize embedder and vector store (optional, for semantic search)
	var embedder registryembed.Embedder
	var vectorStore registryvector.VectorStore
	if cfg.SearchSemanticEnabled && cfg.EmbedType != "" && cfg.EmbedType != "none" {
		embedLoader, err := registryembed.Select(cfg.EmbedType)
		if err != nil {
			log.Warn("Embedder not available", "err", err)
		} else {
			embedder, err = embedLoader(ctx)
			if err != nil {
				log.Warn("Failed to initialize embedder", "err", err)
			}
		}
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
	resolver := security.NewTokenResolver(cfg)
	auth := security.AuthMiddleware(resolver)

	// Mount Agent API routes
	conversations.MountRoutes(router, store, cfg, auth, resumer, resumerEnabled)
	entries.MountRoutes(router, store, auth)
	memberships.MountRoutes(router, store, auth)
	transfers.MountRoutes(router, store, auth)
	search.MountRoutes(router, store, cfg, auth, embedder, vectorStore)
	attachments.MountRoutes(router, store, attachStore, cfg, auth)

	// Mount Admin API routes
	admin.MountRoutes(router, store, attachStore, cfg, auth)

	// Start background services
	indexer := service.NewBackgroundIndexer(store, embedder, vectorStore, cfg.VectorIndexerBatchSize)
	go indexer.Start(ctx)

	evictionSvc := service.NewEvictionService(store, cfg.EvictionBatchSize, cfg.EvictionBatchDelay)
	go evictionSvc.Start(ctx)

	taskProc := service.NewTaskProcessor(store, vectorStore)
	go taskProc.Start(ctx)

	attachmentCleanup := service.NewAttachmentCleanupService(store, attachStore, cfg.AttachmentCleanupInterval)
	go attachmentCleanup.Start(ctx)

	// Set up gRPC server with auth interceptors.
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(security.GRPCUnaryInterceptor(resolver)),
		grpc.ChainStreamInterceptor(security.GRPCStreamInterceptor(resolver)),
	)
	pb.RegisterSystemServiceServer(grpcServer, &grpcserver.SystemServer{})
	pb.RegisterConversationsServiceServer(grpcServer, &grpcserver.ConversationsServer{Store: store})
	pb.RegisterEntriesServiceServer(grpcServer, &grpcserver.EntriesServer{Store: store})
	pb.RegisterConversationMembershipsServiceServer(grpcServer, &grpcserver.MembershipsServer{Store: store})
	pb.RegisterOwnershipTransfersServiceServer(grpcServer, &grpcserver.TransfersServer{Store: store})
	pb.RegisterSearchServiceServer(grpcServer, &grpcserver.SearchServer{Store: store, Config: cfg})
	pb.RegisterAttachmentsServiceServer(grpcServer, &grpcserver.AttachmentsServer{
		Store:       store,
		AttachStore: attachStore,
		MaxBodySize: cfg.AttachmentMaxSize,
		Config:      cfg,
	})
	pb.RegisterResponseRecorderServiceServer(grpcServer, &grpcserver.ResponseRecorderServer{
		Resumer: resumer,
		Store:   store,
		Config:  cfg,
		Enabled: resumerEnabled,
	})

	// Mount management route plugins. If a dedicated management port is configured,
	// run them on a bare gin engine served by the management server. Otherwise,
	// mount them on the main router so existing single-port behaviour is unchanged.
	var closeManagement func(context.Context) error
	if cfg.ManagementListenerEnabled {
		mgmtRouter := gin.New()
		mgmtRouter.Use(gin.Recovery())
		if cfg.ManagementAccessLog {
			mgmtRouter.Use(security.AccessLogMiddleware())
		}
		for _, loader := range registryroute.ManagementRouteLoaders() {
			if err := loader(mgmtRouter); err != nil {
				return nil, fmt.Errorf("failed to load management routes: %w", err)
			}
		}
		// Management listener shares TLS cert/key with the main listener.
		mgmtCfg := cfg.ManagementListener
		mgmtCfg.TLSCertFile = cfg.Listener.TLSCertFile
		mgmtCfg.TLSKeyFile = cfg.Listener.TLSKeyFile
		_, closeManagement, err = startManagementServer(mgmtCfg, mgmtRouter)
		if err != nil {
			return nil, fmt.Errorf("failed to start management server: %w", err)
		}
	} else {
		for _, loader := range registryroute.ManagementRouteLoaders() {
			if err := loader(router); err != nil {
				return nil, fmt.Errorf("failed to load management routes: %w", err)
			}
		}
	}

	// Start single-port HTTP+gRPC
	running, err := StartSinglePortHTTPAndGRPC(ctx, cfg.Listener, router, grpcServer)
	if err != nil {
		return nil, err
	}

	log.Info("Server listening",
		"port", running.Port,
		"plaintext", cfg.Listener.EnablePlainText,
		"tls", cfg.Listener.EnableTLS,
	)

	routesystem.MarkReady()
	return &Server{
		Config:          cfg,
		Store:           store,
		Router:          router,
		GRPCServer:      grpcServer,
		Running:         running,
		closeManagement: closeManagement,
	}, nil
}
