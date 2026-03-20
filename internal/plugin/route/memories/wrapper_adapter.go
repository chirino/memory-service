package memories

import (
	"net/http"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	generatedapi "github.com/chirino/memory-service/internal/generated/api"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// NewAPIServerAdapter returns a generated API handler adapter that directly
// delegates the /v1/memories* endpoints to the legacy in-package logic.
func NewAPIServerAdapter(store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, embedder registryembed.Embedder) generatedapi.ServerInterface {
	return &apiServerAdapter{
		store:    store,
		policy:   policy,
		cfg:      cfg,
		embedder: embedder,
	}
}

type apiServerAdapter struct {
	store    registryepisodic.EpisodicStore
	policy   *episodic.PolicyEngine
	cfg      *config.Config
	embedder registryembed.Embedder
}

func (a *apiServerAdapter) UploadAttachment(c *gin.Context, _ generatedapi.UploadAttachmentParams) {
	notImplemented(c)
}
func (a *apiServerAdapter) DownloadAttachmentByToken(c *gin.Context, _ string, _ string) {
	notImplemented(c)
}
func (a *apiServerAdapter) DeleteAttachment(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) GetAttachment(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) GetAttachmentDownloadUrl(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) ListConversations(c *gin.Context, _ generatedapi.ListConversationsParams) {
	notImplemented(c)
}
func (a *apiServerAdapter) CreateConversation(c *gin.Context) { notImplemented(c) }
func (a *apiServerAdapter) IndexConversations(c *gin.Context) { notImplemented(c) }
func (a *apiServerAdapter) SearchConversations(c *gin.Context) {
	notImplemented(c)
}
func (a *apiServerAdapter) ListUnindexedEntries(c *gin.Context, _ generatedapi.ListUnindexedEntriesParams) {
	notImplemented(c)
}
func (a *apiServerAdapter) DeleteConversation(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) GetConversation(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) UpdateConversation(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) ListConversationEntries(c *gin.Context, _ openapi_types.UUID, _ generatedapi.ListConversationEntriesParams) {
	notImplemented(c)
}
func (a *apiServerAdapter) AppendConversationEntry(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) SyncConversationContext(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) ListConversationForks(c *gin.Context, _ openapi_types.UUID, _ generatedapi.ListConversationForksParams) {
	notImplemented(c)
}
func (a *apiServerAdapter) ListConversationMemberships(c *gin.Context, _ openapi_types.UUID, _ generatedapi.ListConversationMembershipsParams) {
	notImplemented(c)
}
func (a *apiServerAdapter) ShareConversation(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) DeleteConversationMembership(c *gin.Context, _ openapi_types.UUID, _ string) {
	notImplemented(c)
}
func (a *apiServerAdapter) UpdateConversationMembership(c *gin.Context, _ openapi_types.UUID, _ string) {
	notImplemented(c)
}
func (a *apiServerAdapter) DeleteConversationResponse(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}

func (a *apiServerAdapter) DeleteMemory(c *gin.Context, params generatedapi.DeleteMemoryParams) {
	deleteMemoryWithParams(c, a.store, a.policy, a.cfg, params)
}

func (a *apiServerAdapter) GetMemory(c *gin.Context, params generatedapi.GetMemoryParams) {
	getMemoryWithParams(c, a.store, a.policy, a.cfg, params)
}

func (a *apiServerAdapter) PutMemory(c *gin.Context) {
	putMemory(c, a.store, a.policy, a.cfg)
}

func (a *apiServerAdapter) ListMemoryEvents(c *gin.Context, params generatedapi.ListMemoryEventsParams) {
	listMemoryEventsWithParams(c, a.store, a.policy, a.cfg, params)
}

func (a *apiServerAdapter) ListMemoryNamespaces(c *gin.Context, params generatedapi.ListMemoryNamespacesParams) {
	listNamespacesWithParams(c, a.store, a.policy, a.cfg, params)
}

func (a *apiServerAdapter) SearchMemories(c *gin.Context) {
	searchMemories(c, a.store, a.policy, a.cfg, a.embedder)
}

func (a *apiServerAdapter) ListPendingTransfers(c *gin.Context, _ generatedapi.ListPendingTransfersParams) {
	notImplemented(c)
}
func (a *apiServerAdapter) CreateOwnershipTransfer(c *gin.Context) { notImplemented(c) }
func (a *apiServerAdapter) DeleteTransfer(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) GetTransfer(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) AcceptTransfer(c *gin.Context, _ openapi_types.UUID) {
	notImplemented(c)
}
func (a *apiServerAdapter) SubscribeEvents(c *gin.Context, _ generatedapi.SubscribeEventsParams) {
	notImplemented(c)
}
func (a *apiServerAdapter) AdminSubscribeEvents(c *gin.Context, _ generatedapi.AdminSubscribeEventsParams) {
	notImplemented(c)
}

func notImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "endpoint is not implemented in this wrapper adapter"})
}
