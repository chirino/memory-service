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
	routeknowledge "github.com/chirino/memory-service/internal/plugin/route/knowledge"
	routememories "github.com/chirino/memory-service/internal/plugin/route/memories"
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
		"httpSocket", strings.TrimSpace(cfg.Listener.UnixSocket),
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
	if cfg.DatastoreType == "sqlite" {
		switch strings.TrimSpace(attachStoreName) {
		case "", "db":
			if cfg.AttachTypeExplicit {
				return nil, fmt.Errorf("attachments-kind=%q is not supported with db-kind=sqlite; use --attachments-kind=fs", cfg.AttachType)
			}
			attachStoreName = "fs"
		case "fs":
			// explicit, supported
		default:
			return nil, fmt.Errorf("attachments-kind=%q is not supported with db-kind=sqlite; use --attachments-kind=fs", cfg.AttachType)
		}
		if _, err := cfg.ResolvedAttachmentsFSDir(); err != nil {
			return nil, err
		}
	} else if attachStoreName == "db" {
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
	// Wrap with encryption when the primary provider is real (not plain) and attachment
	// encryption is not explicitly disabled.
	if attachStore != nil && encSvc.IsPrimaryReal() && !cfg.EncryptionAttachmentsDisabled {
		attachStore, err = encrypt.Wrap(attachStore, encSvc)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize attachment encryption: %w", err)
		}
	}

	// Initialize embedder and vector store (optional, for semantic search)
	var embedder registryembed.Embedder
	var vectorStore registryvector.VectorStore
	if cfg.VectorType == "sqlite" && cfg.DatastoreType != "sqlite" {
		return nil, fmt.Errorf("vector-kind=%q requires db-kind=sqlite", cfg.VectorType)
	}
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

	memoriesAdapter := routememories.NewAPIServerAdapter(episodicStore, episodicPolicy, cfg, embedder)

	// Register generated wrappers on the public router.
	registerAPIRoutes(router, auth, cfg, store, attachStore, attachSigningKeys, embedder, vectorStore, resumer, resumerEnabled, episodicStore, episodicPolicy, episodicIdx, memoriesAdapter, eventBus)

	// Start background services
	indexer := service.NewBackgroundIndexer(store, embedder, vectorStore, cfg.VectorIndexerBatchSize)
	go indexer.Start(ctx)

	evictionSvc := service.NewEvictionService(store, cfg.EvictionBatchSize, cfg.EvictionBatchDelay)
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
			knowledgeHandler.RegisterRoutes(router, auth)

			log.Info("Knowledge clustering enabled",
				"epsilon", cfg.KnowledgeClusteringEpsilon,
				"minPts", cfg.KnowledgeClusteringMinPts,
			)
		}
	}

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
	pb.RegisterMemoriesServiceServer(grpcServer, &grpcserver.MemoriesServer{
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
	})
	pb.RegisterResponseRecorderServiceServer(grpcServer, &grpcserver.ResponseRecorderServer{
		Resumer:  resumer,
		Store:    store,
		Config:   cfg,
		Enabled:  resumerEnabled,
		EventBus: eventBus,
	})
	pb.RegisterEventStreamServiceServer(grpcServer, &grpcserver.EventStreamServer{
		Store:    store,
		EventBus: eventBus,
		Config:   cfg,
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
		"addr", running.Endpoint,
		"network", running.Network,
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
