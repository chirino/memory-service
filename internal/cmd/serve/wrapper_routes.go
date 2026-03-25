package serve

import (
	"net/http"
	"strings"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	generatedadmin "github.com/chirino/memory-service/internal/generated/admin"
	generatedapi "github.com/chirino/memory-service/internal/generated/api"
	routeadmin "github.com/chirino/memory-service/internal/plugin/route/admin"
	routeagentevents "github.com/chirino/memory-service/internal/plugin/route/agent"
	routeattachments "github.com/chirino/memory-service/internal/plugin/route/attachments"
	routecapabilities "github.com/chirino/memory-service/internal/plugin/route/capabilities"
	routeconversations "github.com/chirino/memory-service/internal/plugin/route/conversations"
	routeentries "github.com/chirino/memory-service/internal/plugin/route/entries"
	routememberships "github.com/chirino/memory-service/internal/plugin/route/memberships"
	routememories "github.com/chirino/memory-service/internal/plugin/route/memories"
	routesearch "github.com/chirino/memory-service/internal/plugin/route/search"
	routetransfers "github.com/chirino/memory-service/internal/plugin/route/transfers"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	internalresumer "github.com/chirino/memory-service/internal/resumer"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/service"
	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func registerAPIRoutes(router *gin.Engine, auth gin.HandlerFunc, cfg *config.Config, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, signingKeys [][]byte, embedder registryembed.Embedder, vectorStore registryvector.VectorStore, resumer *internalresumer.Store, resumerEnabled bool, episodicStore registryepisodic.EpisodicStore, episodicPolicy *episodic.PolicyEngine, episodicIndexer *service.EpisodicIndexer, memoriesAdapter generatedapi.ServerInterface, eventBus registryeventbus.EventBus) {
	authMiddleware := func(c *gin.Context) { auth(c) }

	apiWrapper := &generatedapi.ServerInterfaceWrapper{
		Handler: &proxyAPIServer{
			cfg:            cfg,
			store:          store,
			attachStore:    attachStore,
			signingKeys:    signingKeys,
			embedder:       embedder,
			vectorStore:    vectorStore,
			resumer:        resumer,
			resumerEnabled: resumerEnabled,
			eventBus:       eventBus,
		},
		ErrorHandler: func(c *gin.Context, err error, statusCode int) {
			// Keep legacy parity for invalid UUID conversation ids on entry listing.
			if statusCode == http.StatusBadRequest &&
				c.Request.Method == http.MethodGet &&
				c.FullPath() == "/v1/conversations/:conversationId/entries" &&
				strings.Contains(err.Error(), "Invalid format for parameter conversationId") {
				c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
				return
			}
			c.JSON(statusCode, gin.H{"error": err.Error()})
		},
		HandlerMiddlewares: []generatedapi.MiddlewareFunc{authMiddleware},
	}
	publicAPIWrapper := &generatedapi.ServerInterfaceWrapper{
		Handler: apiWrapper.Handler,
		ErrorHandler: func(c *gin.Context, err error, statusCode int) {
			// Keep wrapper error payload shape on public endpoints.
			c.JSON(statusCode, gin.H{"error": err.Error()})
		},
	}

	clientIDMiddleware := func(c *gin.Context) { security.ClientIDMiddleware()(c) }
	memoriesWrapper := &generatedapi.ServerInterfaceWrapper{
		Handler: memoriesAdapter,
		ErrorHandler: func(c *gin.Context, err error, statusCode int) {
			c.JSON(statusCode, gin.H{"error": err.Error()})
		},
		HandlerMiddlewares: []generatedapi.MiddlewareFunc{authMiddleware, clientIDMiddleware},
	}

	adminWrapper := &generatedadmin.ServerInterfaceWrapper{
		Handler: &proxyAdminServer{
			auth:            auth,
			cfg:             cfg,
			store:           store,
			attachStore:     attachStore,
			episodicStore:   episodicStore,
			episodicPolicy:  episodicPolicy,
			episodicIndexer: episodicIndexer,
			eventBus:        eventBus,
		},
		ErrorHandler: func(c *gin.Context, err error, statusCode int) {
			c.JSON(statusCode, gin.H{"error": err.Error()})
		},
	}

	register := func(method string, path string, wrapper gin.HandlerFunc) {
		switch method {
		case http.MethodGet:
			router.GET(path, wrapper)
		case http.MethodPost:
			router.POST(path, wrapper)
		case http.MethodPut:
			router.PUT(path, wrapper)
		case http.MethodPatch:
			router.PATCH(path, wrapper)
		case http.MethodDelete:
			router.DELETE(path, wrapper)
		}
	}

	register(http.MethodPost, "/v1/attachments", apiWrapper.UploadAttachment)
	register(http.MethodGet, "/v1/attachments/download/:token/:filename", publicAPIWrapper.DownloadAttachmentByToken)
	register(http.MethodDelete, "/v1/attachments/:id", apiWrapper.DeleteAttachment)
	register(http.MethodGet, "/v1/attachments/:id", apiWrapper.GetAttachment)
	register(http.MethodGet, "/v1/attachments/:id/download-url", apiWrapper.GetAttachmentDownloadUrl)
	register(http.MethodGet, "/v1/capabilities", apiWrapper.GetCapabilities)
	register(http.MethodGet, "/v1/conversations", apiWrapper.ListConversations)
	register(http.MethodPost, "/v1/conversations", apiWrapper.CreateConversation)
	register(http.MethodPost, "/v1/conversations/index", apiWrapper.IndexConversations)
	register(http.MethodPost, "/v1/conversations/search", apiWrapper.SearchConversations)
	register(http.MethodGet, "/v1/conversations/unindexed", apiWrapper.ListUnindexedEntries)
	register(http.MethodDelete, "/v1/conversations/:conversationId", apiWrapper.DeleteConversation)
	register(http.MethodGet, "/v1/conversations/:conversationId", apiWrapper.GetConversation)
	register(http.MethodPatch, "/v1/conversations/:conversationId", apiWrapper.UpdateConversation)
	register(http.MethodGet, "/v1/conversations/:conversationId/children", apiWrapper.ListConversationChildren)
	register(http.MethodGet, "/v1/conversations/:conversationId/entries", apiWrapper.ListConversationEntries)
	register(http.MethodPost, "/v1/conversations/:conversationId/entries", apiWrapper.AppendConversationEntry)
	register(http.MethodPost, "/v1/conversations/:conversationId/entries/sync", apiWrapper.SyncConversationContext)
	register(http.MethodGet, "/v1/conversations/:conversationId/forks", apiWrapper.ListConversationForks)
	register(http.MethodGet, "/v1/conversations/:conversationId/memberships", apiWrapper.ListConversationMemberships)
	register(http.MethodPost, "/v1/conversations/:conversationId/memberships", apiWrapper.ShareConversation)
	register(http.MethodDelete, "/v1/conversations/:conversationId/memberships/:userId", apiWrapper.DeleteConversationMembership)
	register(http.MethodPatch, "/v1/conversations/:conversationId/memberships/:userId", apiWrapper.UpdateConversationMembership)
	register(http.MethodDelete, "/v1/conversations/:conversationId/response", apiWrapper.DeleteConversationResponse)
	register(http.MethodDelete, "/v1/memories", memoriesWrapper.DeleteMemory)
	register(http.MethodGet, "/v1/memories", memoriesWrapper.GetMemory)
	register(http.MethodPut, "/v1/memories", memoriesWrapper.PutMemory)
	register(http.MethodGet, "/v1/memories/events", memoriesWrapper.ListMemoryEvents)
	register(http.MethodGet, "/v1/memories/namespaces", memoriesWrapper.ListMemoryNamespaces)
	register(http.MethodPost, "/v1/memories/search", memoriesWrapper.SearchMemories)
	register(http.MethodGet, "/v1/ownership-transfers", apiWrapper.ListPendingTransfers)
	register(http.MethodPost, "/v1/ownership-transfers", apiWrapper.CreateOwnershipTransfer)
	register(http.MethodDelete, "/v1/ownership-transfers/:transferId", apiWrapper.DeleteTransfer)
	register(http.MethodGet, "/v1/ownership-transfers/:transferId", apiWrapper.GetTransfer)
	register(http.MethodPost, "/v1/ownership-transfers/:transferId/accept", apiWrapper.AcceptTransfer)
	register(http.MethodGet, "/v1/admin/events", apiWrapper.AdminSubscribeEvents)
	register(http.MethodGet, "/v1/events", apiWrapper.SubscribeEvents)

	register(http.MethodGet, "/admin/v1/memories/index/status", adminWrapper.AdminGetMemoryIndexStatus)
	register(http.MethodPost, "/admin/v1/memories/index/trigger", adminWrapper.AdminTriggerMemoryIndex)
	register(http.MethodGet, "/admin/v1/memories/policies", adminWrapper.AdminGetMemoryPolicies)
	register(http.MethodPut, "/admin/v1/memories/policies", adminWrapper.AdminPutMemoryPolicies)
	register(http.MethodGet, "/admin/v1/memories/usage", adminWrapper.AdminGetMemoryUsage)
	register(http.MethodGet, "/admin/v1/memories/usage/top", adminWrapper.AdminListTopMemoryUsage)
	register(http.MethodDelete, "/admin/v1/memories/:id", adminWrapper.AdminDeleteMemory)
	register(http.MethodGet, "/v1/admin/attachments", adminWrapper.AdminListAttachments)
	register(http.MethodDelete, "/v1/admin/attachments/:id", adminWrapper.AdminDeleteAttachment)
	register(http.MethodGet, "/v1/admin/attachments/:id", adminWrapper.AdminGetAttachment)
	register(http.MethodGet, "/v1/admin/attachments/:id/content", adminWrapper.AdminGetAttachmentContent)
	register(http.MethodGet, "/v1/admin/attachments/:id/download-url", adminWrapper.AdminGetAttachmentDownloadUrl)
	register(http.MethodGet, "/v1/admin/conversations", adminWrapper.AdminListConversations)
	register(http.MethodPost, "/v1/admin/conversations/search", adminWrapper.AdminSearchConversations)
	register(http.MethodDelete, "/v1/admin/conversations/:id", adminWrapper.AdminDeleteConversation)
	register(http.MethodGet, "/v1/admin/conversations/:id", adminWrapper.AdminGetConversation)
	register(http.MethodGet, "/v1/admin/conversations/:id/children", adminWrapper.AdminListChildConversations)
	register(http.MethodGet, "/v1/admin/conversations/:id/entries", adminWrapper.AdminGetEntries)
	register(http.MethodGet, "/v1/admin/conversations/:id/forks", adminWrapper.AdminListForks)
	register(http.MethodGet, "/v1/admin/conversations/:id/memberships", adminWrapper.AdminGetMemberships)
	register(http.MethodPost, "/v1/admin/conversations/:id/restore", adminWrapper.AdminRestoreConversation)
	register(http.MethodPost, "/v1/admin/evict", adminWrapper.AdminEvict)
	register(http.MethodGet, "/v1/admin/stats/cache-hit-rate", adminWrapper.GetCacheHitRate)
	register(http.MethodGet, "/v1/admin/stats/db-pool-utilization", adminWrapper.GetDbPoolUtilization)
	register(http.MethodGet, "/v1/admin/stats/error-rate", adminWrapper.GetErrorRate)
	register(http.MethodGet, "/v1/admin/stats/latency-p95", adminWrapper.GetLatencyP95)
	register(http.MethodGet, "/v1/admin/stats/request-rate", adminWrapper.GetRequestRate)
	register(http.MethodGet, "/v1/admin/stats/store-latency-p95", adminWrapper.GetStoreLatencyP95)
	register(http.MethodGet, "/v1/admin/stats/store-throughput", adminWrapper.GetStoreThroughput)
	register(http.MethodGet, "/v1/health", adminWrapper.GetHealth)

}

type proxyAPIServer struct {
	cfg            *config.Config
	store          registrystore.MemoryStore
	attachStore    registryattach.AttachmentStore
	signingKeys    [][]byte
	embedder       registryembed.Embedder
	vectorStore    registryvector.VectorStore
	resumer        *internalresumer.Store
	resumerEnabled bool
	eventBus       registryeventbus.EventBus
}

func (p *proxyAPIServer) UploadAttachment(c *gin.Context, _ generatedapi.UploadAttachmentParams) {
	routeattachments.HandleUpload(c, p.store, p.attachStore, p.cfg)
}
func (p *proxyAPIServer) DownloadAttachmentByToken(c *gin.Context, _ string, _ string) {
	// Wrapper binding already validated token/filename as strings. Keep this guard for misconfigured routing.
	if c.Param("token") == "" || c.Param("filename") == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid format for parameter token or filename"})
		return
	}
	routeattachments.HandleDownloadByToken(c, p.store, p.attachStore, p.signingKeys)
}
func (p *proxyAPIServer) DeleteAttachment(c *gin.Context, _ openapi_types.UUID) {
	routeattachments.HandleDeleteAttachment(c, p.store, p.attachStore)
}
func (p *proxyAPIServer) GetAttachment(c *gin.Context, _ openapi_types.UUID) {
	routeattachments.HandleGetAttachment(c, p.store, p.attachStore, p.cfg)
}
func (p *proxyAPIServer) GetAttachmentDownloadUrl(c *gin.Context, _ openapi_types.UUID) {
	var primary []byte
	if len(p.signingKeys) > 0 {
		primary = p.signingKeys[0]
	}
	routeattachments.HandleDownloadURL(c, p.store, p.attachStore, p.cfg, primary)
}
func (p *proxyAPIServer) GetCapabilities(c *gin.Context) {
	routecapabilities.HandleGetCapabilities(c, p.cfg)
}
func (p *proxyAPIServer) ListConversations(c *gin.Context, _ generatedapi.ListConversationsParams) {
	routeconversations.HandleListConversations(c, p.store)
}
func (p *proxyAPIServer) CreateConversation(c *gin.Context) {
	routeconversations.HandleCreateConversation(c, p.store, p.eventBus)
}
func (p *proxyAPIServer) IndexConversations(c *gin.Context) {
	routesearch.HandleIndexConversations(c, p.store)
}
func (p *proxyAPIServer) SearchConversations(c *gin.Context) {
	routesearch.HandleSearchConversations(c, p.store, p.cfg, p.embedder, p.vectorStore)
}
func (p *proxyAPIServer) ListUnindexedEntries(c *gin.Context, _ generatedapi.ListUnindexedEntriesParams) {
	routesearch.HandleListUnindexed(c, p.store)
}
func (p *proxyAPIServer) DeleteConversation(c *gin.Context, _ openapi_types.UUID) {
	routeconversations.HandleDeleteConversation(c, p.store, p.eventBus)
}
func (p *proxyAPIServer) GetConversation(c *gin.Context, _ openapi_types.UUID) {
	routeconversations.HandleGetConversation(c, p.store)
}
func (p *proxyAPIServer) UpdateConversation(c *gin.Context, _ openapi_types.UUID) {
	routeconversations.HandleUpdateConversation(c, p.store, p.eventBus)
}
func (p *proxyAPIServer) ListConversationEntries(c *gin.Context, _ openapi_types.UUID, _ generatedapi.ListConversationEntriesParams) {
	routeentries.HandleListEntries(c, p.store)
}
func (p *proxyAPIServer) AppendConversationEntry(c *gin.Context, _ openapi_types.UUID) {
	routeentries.HandleAppendEntry(c, p.store, p.eventBus)
}
func (p *proxyAPIServer) SyncConversationContext(c *gin.Context, _ openapi_types.UUID) {
	routeentries.HandleSyncMemory(c, p.store)
}
func (p *proxyAPIServer) ListConversationForks(c *gin.Context, _ openapi_types.UUID, _ generatedapi.ListConversationForksParams) {
	routeconversations.HandleListForks(c, p.store)
}
func (p *proxyAPIServer) ListConversationChildren(c *gin.Context, _ openapi_types.UUID, _ generatedapi.ListConversationChildrenParams) {
	routeconversations.HandleListChildConversations(c, p.store)
}
func (p *proxyAPIServer) ListConversationMemberships(c *gin.Context, _ openapi_types.UUID, _ generatedapi.ListConversationMembershipsParams) {
	routememberships.HandleListMemberships(c, p.store)
}
func (p *proxyAPIServer) ShareConversation(c *gin.Context, _ openapi_types.UUID) {
	routememberships.HandleShareConversation(c, p.store, p.eventBus)
}
func (p *proxyAPIServer) DeleteConversationMembership(c *gin.Context, _ openapi_types.UUID, _ string) {
	routememberships.HandleDeleteMembership(c, p.store, p.eventBus)
}
func (p *proxyAPIServer) UpdateConversationMembership(c *gin.Context, _ openapi_types.UUID, _ string) {
	routememberships.HandleUpdateMembership(c, p.store, p.eventBus)
}
func (p *proxyAPIServer) DeleteConversationResponse(c *gin.Context, _ openapi_types.UUID) {
	routeconversations.HandleCancelResponse(c, p.store, p.resumer, p.resumerEnabled)
}
func (p *proxyAPIServer) DeleteMemory(c *gin.Context, _ generatedapi.DeleteMemoryParams) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "DeleteMemory is handled by memories wrapper"})
}
func (p *proxyAPIServer) GetMemory(c *gin.Context, _ generatedapi.GetMemoryParams) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "GetMemory is handled by memories wrapper"})
}
func (p *proxyAPIServer) PutMemory(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "PutMemory is handled by memories wrapper"})
}
func (p *proxyAPIServer) ListMemoryEvents(c *gin.Context, _ generatedapi.ListMemoryEventsParams) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "ListMemoryEvents is handled by memories wrapper"})
}
func (p *proxyAPIServer) ListMemoryNamespaces(c *gin.Context, _ generatedapi.ListMemoryNamespacesParams) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "ListMemoryNamespaces is handled by memories wrapper"})
}
func (p *proxyAPIServer) SearchMemories(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "SearchMemories is handled by memories wrapper"})
}
func (p *proxyAPIServer) ListPendingTransfers(c *gin.Context, _ generatedapi.ListPendingTransfersParams) {
	routetransfers.HandleListTransfers(c, p.store)
}
func (p *proxyAPIServer) CreateOwnershipTransfer(c *gin.Context) {
	routetransfers.HandleCreateTransfer(c, p.store)
}
func (p *proxyAPIServer) DeleteTransfer(c *gin.Context, _ openapi_types.UUID) {
	routetransfers.HandleDeleteTransfer(c, p.store)
}
func (p *proxyAPIServer) GetTransfer(c *gin.Context, _ openapi_types.UUID) {
	routetransfers.HandleGetTransfer(c, p.store)
}
func (p *proxyAPIServer) AcceptTransfer(c *gin.Context, _ openapi_types.UUID) {
	routetransfers.HandleAcceptTransfer(c, p.store)
}
func (p *proxyAPIServer) SubscribeEvents(c *gin.Context, _ generatedapi.SubscribeEventsParams) {
	routeagentevents.HandleSSEEvents(c, p.store, p.eventBus, p.cfg)
}
func (p *proxyAPIServer) AdminSubscribeEvents(c *gin.Context, _ generatedapi.AdminSubscribeEventsParams) {
	if security.EffectiveAdminRole(c) == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin or auditor role required"})
		return
	}
	routeadmin.HandleAdminSSEEvents(c, p.store, p.eventBus, p.cfg)
}

type proxyAdminServer struct {
	auth            gin.HandlerFunc
	cfg             *config.Config
	store           registrystore.MemoryStore
	attachStore     registryattach.AttachmentStore
	episodicStore   registryepisodic.EpisodicStore
	episodicPolicy  *episodic.PolicyEngine
	episodicIndexer *service.EpisodicIndexer
	eventBus        registryeventbus.EventBus
}

func (p *proxyAdminServer) authorize(c *gin.Context) bool {
	if p.auth == nil {
		return true
	}
	p.auth(c)
	return !c.IsAborted()
}

func (p *proxyAdminServer) AdminGetMemoryIndexStatus(c *gin.Context) {
	if !p.authorize(c) {
		return
	}
	routememories.HandleAdminGetMemoryIndexStatus(c, p.episodicStore)
}
func (p *proxyAdminServer) AdminSubscribeEvents(c *gin.Context, _ generatedadmin.AdminSubscribeEventsParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminSSEEvents(c, p.store, p.eventBus, p.cfg)
}
func (p *proxyAdminServer) AdminTriggerMemoryIndex(c *gin.Context) {
	if !p.authorize(c) {
		return
	}
	routememories.HandleAdminTriggerMemoryIndex(c, p.episodicIndexer)
}
func (p *proxyAdminServer) AdminGetMemoryPolicies(c *gin.Context) {
	if !p.authorize(c) {
		return
	}
	routememories.HandleAdminGetMemoryPolicies(c, p.episodicPolicy)
}
func (p *proxyAdminServer) AdminPutMemoryPolicies(c *gin.Context) {
	if !p.authorize(c) {
		return
	}
	routememories.HandleAdminPutMemoryPolicies(c, p.episodicPolicy, p.cfg)
}
func (p *proxyAdminServer) AdminGetMemoryUsage(c *gin.Context, _ generatedadmin.AdminGetMemoryUsageParams) {
	if !p.authorize(c) {
		return
	}
	routememories.HandleAdminGetMemoryUsage(c, p.episodicStore, p.cfg)
}
func (p *proxyAdminServer) AdminListTopMemoryUsage(c *gin.Context, _ generatedadmin.AdminListTopMemoryUsageParams) {
	if !p.authorize(c) {
		return
	}
	routememories.HandleAdminListTopMemoryUsage(c, p.episodicStore, p.cfg)
}
func (p *proxyAdminServer) AdminDeleteMemory(c *gin.Context, _ openapi_types.UUID) {
	if !p.authorize(c) {
		return
	}
	routememories.HandleAdminDeleteMemory(c, p.episodicStore)
}
func (p *proxyAdminServer) AdminListAttachments(c *gin.Context, _ generatedadmin.AdminListAttachmentsParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminListAttachments(c, p.store)
}
func (p *proxyAdminServer) AdminDeleteAttachment(c *gin.Context, _ openapi_types.UUID) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminDeleteAttachment(c, p.store, p.attachStore)
}
func (p *proxyAdminServer) AdminGetAttachment(c *gin.Context, _ openapi_types.UUID, _ generatedadmin.AdminGetAttachmentParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminGetAttachment(c, p.store)
}
func (p *proxyAdminServer) AdminGetAttachmentContent(c *gin.Context, _ openapi_types.UUID, _ generatedadmin.AdminGetAttachmentContentParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminGetAttachmentContent(c, p.store, p.attachStore, p.cfg)
}
func (p *proxyAdminServer) AdminGetAttachmentDownloadUrl(c *gin.Context, _ openapi_types.UUID, _ generatedadmin.AdminGetAttachmentDownloadUrlParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminGetAttachmentDownloadURL(c, p.store, p.attachStore, p.cfg)
}
func (p *proxyAdminServer) AdminListConversations(c *gin.Context, _ generatedadmin.AdminListConversationsParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminListConversations(c, p.store)
}
func (p *proxyAdminServer) AdminSearchConversations(c *gin.Context, _ generatedadmin.AdminSearchConversationsParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminSearchConversations(c, p.store)
}
func (p *proxyAdminServer) AdminDeleteConversation(c *gin.Context, _ openapi_types.UUID) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminDeleteConversation(c, p.store)
}
func (p *proxyAdminServer) AdminGetConversation(c *gin.Context, _ openapi_types.UUID, _ generatedadmin.AdminGetConversationParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminGetConversation(c, p.store)
}
func (p *proxyAdminServer) AdminGetEntries(c *gin.Context, _ openapi_types.UUID, _ generatedadmin.AdminGetEntriesParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminGetEntries(c, p.store)
}
func (p *proxyAdminServer) AdminListForks(c *gin.Context, _ openapi_types.UUID, _ generatedadmin.AdminListForksParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminListForks(c, p.store)
}
func (p *proxyAdminServer) AdminListChildConversations(c *gin.Context, _ openapi_types.UUID, _ generatedadmin.AdminListChildConversationsParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminListChildConversations(c, p.store)
}
func (p *proxyAdminServer) AdminGetMemberships(c *gin.Context, _ openapi_types.UUID, _ generatedadmin.AdminGetMembershipsParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminGetMemberships(c, p.store)
}
func (p *proxyAdminServer) AdminRestoreConversation(c *gin.Context, _ openapi_types.UUID) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminRestoreConversation(c, p.store)
}
func (p *proxyAdminServer) AdminEvict(c *gin.Context, _ generatedadmin.AdminEvictParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminEvict(c, p.store)
}
func (p *proxyAdminServer) GetCacheHitRate(c *gin.Context, _ generatedadmin.GetCacheHitRateParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminStatsCacheHitRate(c, p.cfg)
}
func (p *proxyAdminServer) GetDbPoolUtilization(c *gin.Context, _ generatedadmin.GetDbPoolUtilizationParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminStatsDbPoolUtilization(c, p.cfg)
}
func (p *proxyAdminServer) GetErrorRate(c *gin.Context, _ generatedadmin.GetErrorRateParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminStatsErrorRate(c, p.cfg)
}
func (p *proxyAdminServer) GetLatencyP95(c *gin.Context, _ generatedadmin.GetLatencyP95Params) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminStatsLatencyP95(c, p.cfg)
}
func (p *proxyAdminServer) GetRequestRate(c *gin.Context, _ generatedadmin.GetRequestRateParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminStatsRequestRate(c, p.cfg)
}
func (p *proxyAdminServer) GetStoreLatencyP95(c *gin.Context, _ generatedadmin.GetStoreLatencyP95Params) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminStatsStoreLatencyP95(c, p.cfg)
}
func (p *proxyAdminServer) GetStoreThroughput(c *gin.Context, _ generatedadmin.GetStoreThroughputParams) {
	if !p.authorize(c) {
		return
	}
	routeadmin.HandleAdminStatsStoreThroughput(c, p.cfg)
}
func (p *proxyAdminServer) GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
