package memories

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	generatedapi "github.com/chirino/memory-service/internal/generated/api"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// MountRoutes mounts the episodic memory REST endpoints on the given router.
func MountRoutes(r *gin.Engine, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, auth gin.HandlerFunc, embedder registryembed.Embedder) {
	if store == nil {
		return
	}
	clientID := security.ClientIDMiddleware()
	g := r.Group("/v1", auth, clientID)

	g.PUT("/memories", func(c *gin.Context) { putMemory(c, store, policy, cfg) })
	g.GET("/memories", func(c *gin.Context) { getMemory(c, store, policy, cfg) })
	g.DELETE("/memories", func(c *gin.Context) { deleteMemory(c, store, policy, cfg) })
	g.POST("/memories/search", func(c *gin.Context) { searchMemories(c, store, policy, cfg, embedder) })
	g.GET("/memories/namespaces", func(c *gin.Context) { listNamespaces(c, store, policy, cfg) })
	g.GET("/memories/events", func(c *gin.Context) { listMemoryEvents(c, store, policy, cfg) })
}

func putMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	var req generatedapi.PutMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	attributes := map[string]interface{}(nil)
	if req.Attributes != nil {
		attributes = *req.Attributes
	}
	indexFields := []string(nil)
	if req.IndexFields != nil {
		indexFields = *req.IndexFields
	}
	indexDisabled := false
	if req.IndexDisabled != nil {
		indexDisabled = *req.IndexDisabled
	}
	ttlSeconds := 0
	if req.TtlSeconds != nil {
		ttlSeconds = *req.TtlSeconds
	}

	if err := validateNamespace(req.Namespace, cfg.EpisodicMaxDepth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Key == "" || len(req.Key) > 1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key must be non-empty and at most 1024 bytes"})
		return
	}
	if req.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "value is required"})
		return
	}

	pc := policyContext(c)

	// OPA authz check.
	if policy != nil {
		allowed, err := policy.IsAllowed(c.Request.Context(), "write", req.Namespace, req.Key, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "policy evaluation error"})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": "access denied"})
			return
		}

		// OPA attribute extraction.
		extracted, err := policy.ExtractAttributes(c.Request.Context(), req.Namespace, req.Key, req.Value, attributes)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "attribute extraction error"})
			return
		}
		result, err := store.PutMemory(c.Request.Context(), registryepisodic.PutMemoryRequest{
			Namespace:        req.Namespace,
			Key:              req.Key,
			Value:            req.Value,
			Attributes:       attributes,
			TTLSeconds:       ttlSeconds,
			IndexFields:      indexFields,
			IndexDisabled:    indexDisabled,
			PolicyAttributes: extracted,
		})
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, toAPIMemoryWriteResult(result))
		return
	}

	// No OPA: store without policy attributes.
	result, err := store.PutMemory(c.Request.Context(), registryepisodic.PutMemoryRequest{
		Namespace:     req.Namespace,
		Key:           req.Key,
		Value:         req.Value,
		Attributes:    attributes,
		TTLSeconds:    ttlSeconds,
		IndexFields:   indexFields,
		IndexDisabled: indexDisabled,
	})
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAPIMemoryWriteResult(result))
}

func getMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	params := generatedapi.GetMemoryParams{
		Ns:  c.QueryArray("ns"),
		Key: c.Query("key"),
	}
	ns := params.Ns
	key := params.Key

	if err := validateNamespace(ns, cfg.EpisodicMaxDepth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	if policy != nil {
		pc := policyContext(c)
		allowed, err := policy.IsAllowed(c.Request.Context(), "read", ns, key, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "policy evaluation error"})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": "access denied"})
			return
		}
	}

	item, err := store.GetMemory(c.Request.Context(), ns, key)
	if err != nil {
		handleError(c, err)
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "memory not found"})
		return
	}
	c.JSON(http.StatusOK, toAPIMemoryItem(*item))
}

func deleteMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	params := generatedapi.DeleteMemoryParams{
		Ns:  c.QueryArray("ns"),
		Key: c.Query("key"),
	}
	ns := params.Ns
	key := params.Key

	if err := validateNamespace(ns, cfg.EpisodicMaxDepth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	if policy != nil {
		pc := policyContext(c)
		allowed, err := policy.IsAllowed(c.Request.Context(), "delete", ns, key, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "policy evaluation error"})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": "access denied"})
			return
		}
	}

	if err := store.DeleteMemory(c.Request.Context(), ns, key); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func searchMemories(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, embedder registryembed.Embedder) {
	var req generatedapi.SearchMemoriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.NamespacePrefix) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "namespace_prefix is required"})
		return
	}
	limit := 10
	if req.Limit != nil && *req.Limit > 0 && *req.Limit <= 100 {
		limit = *req.Limit
	}
	offset := 0
	if req.Offset != nil {
		offset = *req.Offset
	}
	if err := validateNamespace(req.NamespacePrefix, cfg.EpisodicMaxDepth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filter := map[string]interface{}{}
	if req.Filter != nil {
		filter = *req.Filter
	}

	effectivePrefix := req.NamespacePrefix

	// OPA: inject filter constraints.
	if policy != nil {
		pc := policyContext(c)
		var err error
		effectivePrefix, filter, err = policy.InjectFilter(c.Request.Context(), req.NamespacePrefix, filter, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
	}

	query := ""
	if req.Query != nil {
		query = *req.Query
	}
	if query != "" && embedder != nil {
		items, err := semanticSearch(c, store, embedder, effectivePrefix, filter, query, limit)
		if err != nil {
			handleError(c, err)
			return
		}
		if len(items) > 0 {
			respItems := toAPIMemoryItems(items)
			c.JSON(http.StatusOK, generatedapi.SearchMemoriesResponse{Items: &respItems})
			return
		}
	}

	items, err := store.SearchMemories(c.Request.Context(), effectivePrefix, filter, limit, offset)
	if err != nil {
		handleError(c, err)
		return
	}

	respItems := toAPIMemoryItems(items)
	c.JSON(http.StatusOK, generatedapi.SearchMemoriesResponse{Items: &respItems})
}

func listNamespaces(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	params := generatedapi.ListMemoryNamespacesParams{}
	if prefix := c.QueryArray("prefix"); len(prefix) > 0 {
		params.Prefix = &prefix
	}
	if suffix := c.QueryArray("suffix"); len(suffix) > 0 {
		params.Suffix = &suffix
	}
	if c.Query("max_depth") != "" {
		maxDepth := queryInt(c, "max_depth", 0)
		params.MaxDepth = &maxDepth
	}

	prefix := []string{}
	if params.Prefix != nil {
		prefix = *params.Prefix
	}
	suffix := []string{}
	if params.Suffix != nil {
		suffix = *params.Suffix
	}
	maxDepth := 0
	if params.MaxDepth != nil {
		maxDepth = *params.MaxDepth
	}

	if maxDepth < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_depth must be >= 0"})
		return
	}

	if len(prefix) == 0 {
		prefix = []string{}
	} else if err := validateNamespace(prefix, cfg.EpisodicMaxDepth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(suffix) > 0 {
		for i, seg := range suffix {
			if seg == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("suffix segment %d is empty", i)})
				return
			}
		}
	}

	// OPA filter injection (narrows prefix based on caller identity).
	if policy != nil {
		pc := policyContext(c)
		var err error
		prefix, _, err = policy.InjectFilter(c.Request.Context(), prefix, nil, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
	}

	namespaces, err := store.ListNamespaces(c.Request.Context(), registryepisodic.ListNamespacesRequest{
		Prefix:   prefix,
		Suffix:   suffix,
		MaxDepth: maxDepth,
	})
	if err != nil {
		handleError(c, err)
		return
	}
	if namespaces == nil {
		namespaces = [][]string{}
	}
	c.JSON(http.StatusOK, generatedapi.ListMemoryNamespacesResponse{Namespaces: &namespaces})
}

func listMemoryEvents(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	var nsPrefix []string
	if ns := c.QueryArray("ns"); len(ns) > 0 {
		if err := validateNamespace(ns, cfg.EpisodicMaxDepth); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		nsPrefix = ns
	}

	// OPA filter injection (narrows namespace prefix based on caller identity).
	if policy != nil {
		pc := policyContext(c)
		var err error
		nsPrefix, _, err = policy.InjectFilter(c.Request.Context(), nsPrefix, nil, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
	}

	var kinds []string
	for _, k := range c.QueryArray("kinds") {
		kinds = append(kinds, k)
	}

	req := registryepisodic.ListEventsRequest{
		NamespacePrefix: nsPrefix,
		Kinds:           kinds,
		Limit:           queryInt(c, "limit", 50),
		AfterCursor:     c.Query("after_cursor"),
	}

	if afterStr := c.Query("after"); afterStr != "" {
		t, err := time.Parse(time.RFC3339, afterStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'after' timestamp; use RFC 3339 format"})
			return
		}
		req.After = &t
	}
	if beforeStr := c.Query("before"); beforeStr != "" {
		t, err := time.Parse(time.RFC3339, beforeStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'before' timestamp; use RFC 3339 format"})
			return
		}
		req.Before = &t
	}

	page, err := store.ListMemoryEvents(c.Request.Context(), req)
	if err != nil {
		handleError(c, err)
		return
	}

	events := make([]generatedapi.MemoryEventItem, 0, len(page.Events))
	for _, e := range page.Events {
		ev := toAPIMemoryEventItem(e)
		events = append(events, ev)
	}

	var cursor *string
	if page.AfterCursor != "" {
		cursor = &page.AfterCursor
	}
	c.JSON(http.StatusOK, generatedapi.ListMemoryEventsResponse{
		Events:      &events,
		AfterCursor: cursor,
	})
}

func toAPIMemoryEventItem(e registryepisodic.MemoryEvent) generatedapi.MemoryEventItem {
	id := openapi_types.UUID(e.ID)
	ns := append([]string(nil), e.Namespace...)
	key := e.Key
	kind := generatedapi.MemoryEventItemKind(e.Kind)
	occurredAt := e.OccurredAt.UTC()
	var expiresAt *time.Time
	if e.ExpiresAt != nil {
		t := e.ExpiresAt.UTC()
		expiresAt = &t
	}
	return generatedapi.MemoryEventItem{
		Id:         &id,
		Namespace:  &ns,
		Key:        &key,
		Kind:       &kind,
		OccurredAt: &occurredAt,
		Value:      mapRef(e.Value),
		Attributes: mapRef(e.Attributes),
		ExpiresAt:  expiresAt,
	}
}

// --- Admin endpoints ---

// MountAdminRoutes mounts admin endpoints for episodic memories.
func MountAdminRoutes(r *gin.Engine, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, indexer *service.EpisodicIndexer, auth gin.HandlerFunc, requireAdmin gin.HandlerFunc) {
	if store == nil {
		return
	}
	g := r.Group("/admin/v1", auth, requireAdmin)

	g.GET("/memories/policies", func(c *gin.Context) {
		if policy == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic policy engine is not configured"})
			return
		}
		c.JSON(http.StatusOK, policy.Bundle())
	})

	g.PUT("/memories/policies", func(c *gin.Context) {
		if policy == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic policy engine is not configured"})
			return
		}
		var bundle episodic.PolicyBundle
		if err := c.ShouldBindJSON(&bundle); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := policy.ReplaceBundle(c.Request.Context(), bundle); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if cfg != nil && cfg.EpisodicPolicyDir != "" {
			if err := persistPolicyBundle(cfg.EpisodicPolicyDir, bundle); err != nil {
				handleError(c, err)
				return
			}
		}
		c.Status(http.StatusNoContent)
	})

	g.DELETE("/memories/:id", func(c *gin.Context) {
		rawID := c.Param("id")
		memID, err := uuid.Parse(rawID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid memory ID"})
			return
		}
		if err := store.AdminForceDeleteMemory(c.Request.Context(), memID); err != nil {
			handleError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	g.GET("/memories/index/status", func(c *gin.Context) {
		count, err := store.AdminCountPendingIndexing(c.Request.Context())
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"pending": count})
	})

	g.POST("/memories/index/trigger", func(c *gin.Context) {
		if indexer == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic indexer is not configured"})
			return
		}
		stats, err := indexer.Trigger(c.Request.Context())
		if err != nil {
			handleError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"triggered": true,
			"stats":     stats,
		})
	})
}

// --- Helpers ---

func validateNamespace(ns []string, maxDepth int) error {
	if len(ns) == 0 {
		return fmt.Errorf("namespace must have at least one segment")
	}
	for i, seg := range ns {
		if seg == "" {
			return fmt.Errorf("namespace segment %d is empty", i)
		}
	}
	if maxDepth > 0 && len(ns) > maxDepth {
		return fmt.Errorf("namespace depth %d exceeds configured limit %d", len(ns), maxDepth)
	}
	return nil
}

func policyContext(c *gin.Context) episodic.PolicyContext {
	rolesList := []string{}
	if security.IsAdmin(c) {
		rolesList = append(rolesList, "admin")
	}

	return episodic.PolicyContext{
		UserID:   security.GetUserID(c),
		ClientID: security.GetClientID(c),
		JWTClaims: map[string]interface{}{
			"roles": rolesList,
		},
	}
}

func handleError(c *gin.Context, err error) {
	log.Error("episodic route error", "err", err, "stack", string(debug.Stack()))
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func toAPIMemoryWriteResult(result *registryepisodic.MemoryWriteResult) generatedapi.MemoryWriteResult {
	id := openapi_types.UUID(result.ID)
	namespace := append([]string(nil), result.Namespace...)
	key := result.Key
	createdAt := result.CreatedAt.UTC()
	var expiresAt *time.Time
	if result.ExpiresAt != nil {
		t := result.ExpiresAt.UTC()
		expiresAt = &t
	}
	return generatedapi.MemoryWriteResult{
		Id:         &id,
		Namespace:  &namespace,
		Key:        &key,
		Attributes: mapRef(result.Attributes),
		CreatedAt:  &createdAt,
		ExpiresAt:  expiresAt,
	}
}

func toAPIMemoryItem(item registryepisodic.MemoryItem) generatedapi.MemoryItem {
	id := openapi_types.UUID(item.ID)
	namespace := append([]string(nil), item.Namespace...)
	key := item.Key
	createdAt := item.CreatedAt.UTC()
	var expiresAt *time.Time
	if item.ExpiresAt != nil {
		t := item.ExpiresAt.UTC()
		expiresAt = &t
	}
	return generatedapi.MemoryItem{
		Id:         &id,
		Namespace:  &namespace,
		Key:        &key,
		Value:      mapRef(item.Value),
		Attributes: mapRef(item.Attributes),
		Score:      item.Score,
		CreatedAt:  &createdAt,
		ExpiresAt:  expiresAt,
	}
}

func toAPIMemoryItems(items []registryepisodic.MemoryItem) []generatedapi.MemoryItem {
	out := make([]generatedapi.MemoryItem, 0, len(items))
	for _, item := range items {
		out = append(out, toAPIMemoryItem(item))
	}
	return out
}

func mapRef(in map[string]interface{}) *map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return &out
}

func semanticSearch(c *gin.Context, store registryepisodic.EpisodicStore, embedder registryembed.Embedder, namespacePrefix []string, filter map[string]interface{}, query string, limit int) ([]registryepisodic.MemoryItem, error) {
	embeddings, err := embedder.EmbedTexts(c.Request.Context(), []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, nil
	}

	nsEncoded, err := episodic.EncodeNamespace(namespacePrefix, 0)
	if err != nil {
		return nil, err
	}
	vectorResults, err := store.SearchMemoryVectors(c.Request.Context(), nsEncoded, embeddings[0], filter, limit)
	if err != nil {
		return nil, fmt.Errorf("search memory vectors: %w", err)
	}
	if len(vectorResults) == 0 {
		return nil, nil
	}

	scoreByID := make(map[uuid.UUID]float64, len(vectorResults))
	orderedIDs := make([]uuid.UUID, 0, len(vectorResults))
	for _, vr := range vectorResults {
		if prev, exists := scoreByID[vr.MemoryID]; !exists {
			scoreByID[vr.MemoryID] = vr.Score
			orderedIDs = append(orderedIDs, vr.MemoryID)
		} else if vr.Score > prev {
			scoreByID[vr.MemoryID] = vr.Score
		}
	}
	if len(orderedIDs) == 0 {
		return nil, nil
	}

	items, err := store.GetMemoriesByIDs(c.Request.Context(), orderedIDs)
	if err != nil {
		return nil, fmt.Errorf("get memories by ids: %w", err)
	}

	itemByID := make(map[uuid.UUID]registryepisodic.MemoryItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}

	results := make([]registryepisodic.MemoryItem, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		item, ok := itemByID[id]
		if !ok {
			continue
		}
		score := scoreByID[id]
		item.Score = &score
		results = append(results, item)
	}

	sort.SliceStable(results, func(i, j int) bool {
		return *results[i].Score > *results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func queryInt(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	var i int
	if _, err := fmt.Sscanf(v, "%d", &i); err != nil {
		return def
	}
	return i
}

func persistPolicyBundle(dir string, bundle episodic.PolicyBundle) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create policy directory: %w", err)
	}
	writes := map[string]string{
		"authz.rego":      bundle.Authz,
		"attributes.rego": bundle.Attributes,
		"filter.rego":     bundle.Filter,
	}
	for name, content := range writes {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write policy file %s: %w", name, err)
		}
	}
	return nil
}
