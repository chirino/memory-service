package memories

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	generatedapi "github.com/chirino/memory-service/internal/generated/api"
	"github.com/chirino/memory-service/internal/plugin/route/routetx"
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
	g.PATCH("/memories", func(c *gin.Context) { updateMemory(c, store, policy, cfg) })
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
	index := map[string]string(nil)
	if req.Index != nil {
		index = *req.Index
	}
	if index == nil {
		index = map[string]string{}
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
		decision, err := policy.EvaluateAuthz(c.Request.Context(), "write", req.Namespace, req.Key, req.Value, index, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "policy evaluation error"})
			return
		}
		if !decision.Allow {
			resp := gin.H{"error": "access denied"}
			if decision.Reason != "" {
				resp["reason"] = decision.Reason
			}
			c.JSON(http.StatusForbidden, resp)
			return
		}

		// OPA attribute extraction.
		extracted, err := policy.ExtractAttributes(c.Request.Context(), req.Namespace, req.Key, req.Value, index, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "attribute extraction error"})
			return
		}
		if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
			result, err := store.PutMemory(ctx, registryepisodic.PutMemoryRequest{
				Namespace:        req.Namespace,
				Key:              req.Key,
				Value:            req.Value,
				Index:            index,
				TTLSeconds:       ttlSeconds,
				PolicyAttributes: extracted,
			})
			if err != nil {
				return err
			}
			c.JSON(http.StatusOK, toAPIMemoryWriteResult(result))
			return nil
		}); err != nil {
			handleError(c, err)
		}
		return
	}

	// No OPA: store without policy attributes.
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		result, err := store.PutMemory(ctx, registryepisodic.PutMemoryRequest{
			Namespace:  req.Namespace,
			Key:        req.Key,
			Value:      req.Value,
			Index:      index,
			TTLSeconds: ttlSeconds,
		})
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, toAPIMemoryWriteResult(result))
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func getMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	params := generatedapi.GetMemoryParams{
		Ns:  c.QueryArray("ns"),
		Key: c.Query("key"),
	}
	getMemoryWithParams(c, store, policy, cfg, params)
}

func getMemoryWithParams(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, params generatedapi.GetMemoryParams) {
	ns := params.Ns
	key := params.Key
	includeUsage := queryBool(c, "include_usage", false)
	archived, err := registryepisodic.ParseArchiveFilter(c.DefaultQuery("archived", string(registryepisodic.ArchiveFilterExclude)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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
		decision, err := policy.EvaluateAuthz(c.Request.Context(), "read", ns, key, nil, nil, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "policy evaluation error"})
			return
		}
		if !decision.Allow {
			resp := gin.H{"error": "access denied"}
			if decision.Reason != "" {
				resp["reason"] = decision.Reason
			}
			c.JSON(http.StatusForbidden, resp)
			return
		}
	}

	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		item, err := store.GetMemory(ctx, ns, key, archived)
		if err != nil {
			return err
		}
		if item == nil {
			c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "memory not found"})
			return nil
		}

		fetchedAt := time.Now().UTC()
		if err := store.IncrementMemoryLoads(ctx, []registryepisodic.MemoryKey{{
			Namespace: ns,
			Key:       key,
		}}, fetchedAt); err != nil {
			log.Warn("failed to increment memory usage counters", "namespace", ns, "key", key, "err", err)
		}
		if includeUsage {
			usage, err := store.GetMemoryUsage(ctx, ns, key)
			if err != nil {
				log.Warn("failed to load memory usage counters", "namespace", ns, "key", key, "err", err)
			} else {
				item.Usage = usage
			}
		}

		c.JSON(http.StatusOK, toAPIMemoryItem(*item))
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func updateMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	params := generatedapi.UpdateMemoryParams{
		Ns:  c.QueryArray("ns"),
		Key: c.Query("key"),
	}
	updateMemoryWithParams(c, store, policy, cfg, params)
}

func updateMemoryWithParams(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, params generatedapi.UpdateMemoryParams) {
	ns := params.Ns
	key := params.Key
	var req generatedapi.UpdateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Archived == nil || !*req.Archived {
		c.JSON(http.StatusBadRequest, gin.H{"error": "archived must be true"})
		return
	}

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
		decision, err := policy.EvaluateAuthz(c.Request.Context(), "update", ns, key, nil, nil, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "policy evaluation error"})
			return
		}
		if !decision.Allow {
			resp := gin.H{"error": "access denied"}
			if decision.Reason != "" {
				resp["reason"] = decision.Reason
			}
			c.JSON(http.StatusForbidden, resp)
			return
		}
	}

	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		if err := store.ArchiveMemory(ctx, ns, key, nil); err != nil {
			return err
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
	}
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
	includeUsage := req.IncludeUsage != nil && *req.IncludeUsage
	if err := validateNamespace(req.NamespacePrefix, cfg.EpisodicMaxDepth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filter := map[string]interface{}{}
	if req.Filter != nil {
		filter = *req.Filter
	}
	archived := registryepisodic.ArchiveFilterExclude
	if req.Archived != nil {
		parsedArchived, err := registryepisodic.ParseArchiveFilter(string(*req.Archived))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		archived = parsedArchived
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
		if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
			items, err := semanticSearch(c, store, embedder, effectivePrefix, filter, query, limit, archived)
			if err != nil {
				return err
			}
			if includeUsage {
				enrichMemoryItemsWithUsage(ctx, store, items)
			}
			if len(items) > 0 {
				respItems := toAPIMemoryItems(items)
				c.JSON(http.StatusOK, generatedapi.SearchMemoriesResponse{Items: &respItems})
			}
			return nil
		}); err != nil {
			handleError(c, err)
			return
		}
		if c.Writer.Written() {
			return
		}
	}

	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		items, err := store.SearchMemories(ctx, effectivePrefix, filter, limit, offset, archived)
		if err != nil {
			return err
		}
		if includeUsage {
			enrichMemoryItemsWithUsage(ctx, store, items)
		}

		respItems := toAPIMemoryItems(items)
		c.JSON(http.StatusOK, generatedapi.SearchMemoriesResponse{Items: &respItems})
		return nil
	}); err != nil {
		handleError(c, err)
	}
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
	listNamespacesWithParams(c, store, policy, cfg, params)
}

func listNamespacesWithParams(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, params generatedapi.ListMemoryNamespacesParams) {
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
	archived, err := registryepisodic.ParseArchiveFilter(c.DefaultQuery("archived", string(registryepisodic.ArchiveFilterExclude)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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

	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		namespaces, err := store.ListNamespaces(ctx, registryepisodic.ListNamespacesRequest{
			Prefix:   prefix,
			Suffix:   suffix,
			MaxDepth: maxDepth,
			Archived: archived,
		})
		if err != nil {
			return err
		}
		if namespaces == nil {
			namespaces = [][]string{}
		}
		c.JSON(http.StatusOK, generatedapi.ListMemoryNamespacesResponse{Namespaces: &namespaces})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func listMemoryEvents(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	after, err := queryTime(c, "after")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'after' timestamp; use RFC 3339 format"})
		return
	}
	before, err := queryTime(c, "before")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'before' timestamp; use RFC 3339 format"})
		return
	}
	params := generatedapi.ListMemoryEventsParams{
		After:       after,
		Before:      before,
		AfterCursor: queryPtr(c, "after_cursor"),
	}
	if ns := c.QueryArray("ns"); len(ns) > 0 {
		params.Ns = &ns
	}
	if kindsRaw := c.QueryArray("kinds"); len(kindsRaw) > 0 {
		kinds := make([]generatedapi.ListMemoryEventsParamsKinds, 0, len(kindsRaw))
		for _, k := range kindsRaw {
			kinds = append(kinds, generatedapi.ListMemoryEventsParamsKinds(k))
		}
		params.Kinds = &kinds
	}
	if limitRaw := c.Query("limit"); limitRaw != "" {
		limit := queryInt(c, "limit", 50)
		params.Limit = &limit
	}
	listMemoryEventsWithParams(c, store, policy, cfg, params)
}

func listMemoryEventsWithParams(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, params generatedapi.ListMemoryEventsParams) {
	var nsPrefix []string
	if params.Ns != nil {
		nsPrefix = *params.Ns
		if len(nsPrefix) > 0 {
			if err := validateNamespace(nsPrefix, cfg.EpisodicMaxDepth); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
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

	kinds := []string{}
	if params.Kinds != nil {
		for _, kind := range *params.Kinds {
			kinds = append(kinds, string(kind))
		}
	}

	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	req := registryepisodic.ListEventsRequest{
		NamespacePrefix: nsPrefix,
		Kinds:           kinds,
		Limit:           limit,
	}
	if params.AfterCursor != nil {
		req.AfterCursor = *params.AfterCursor
	}
	if params.After != nil {
		after := params.After.UTC()
		req.After = &after
	}
	if params.Before != nil {
		before := params.Before.UTC()
		req.Before = &before
	}

	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		page, err := store.ListMemoryEvents(ctx, req)
		if err != nil {
			return err
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
		return nil
	}); err != nil {
		handleError(c, err)
	}
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
		HandleAdminGetMemoryPolicies(c, policy)
	})

	g.PUT("/memories/policies", func(c *gin.Context) {
		HandleAdminPutMemoryPolicies(c, policy, cfg)
	})

	g.DELETE("/memories/:id", func(c *gin.Context) {
		HandleAdminDeleteMemory(c, store)
	})

	g.GET("/memories/index/status", func(c *gin.Context) {
		HandleAdminGetMemoryIndexStatus(c, store)
	})

	g.POST("/memories/index/trigger", func(c *gin.Context) {
		HandleAdminTriggerMemoryIndex(c, indexer)
	})

	g.GET("/memories/usage", func(c *gin.Context) {
		HandleAdminGetMemoryUsage(c, store, cfg)
	})

	g.GET("/memories/usage/top", func(c *gin.Context) {
		HandleAdminListTopMemoryUsage(c, store, cfg)
	})
}

func ensureAdmin(c *gin.Context) bool {
	security.RequireAdminRole()(c)
	return !c.IsAborted()
}

// HandleAdminGetMemoryPolicies exposes memory policy retrieval for wrapper-native adapters.
func HandleAdminGetMemoryPolicies(c *gin.Context, policy *episodic.PolicyEngine) {
	if !ensureAdmin(c) {
		return
	}
	if policy == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic policy engine is not configured"})
		return
	}
	c.JSON(http.StatusOK, policy.Bundle())
}

// HandleAdminPutMemoryPolicies exposes memory policy updates for wrapper-native adapters.
func HandleAdminPutMemoryPolicies(c *gin.Context, policy *episodic.PolicyEngine, cfg *config.Config) {
	if !ensureAdmin(c) {
		return
	}
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
}

// HandleAdminDeleteMemory exposes forced memory deletes for wrapper-native adapters.
func HandleAdminDeleteMemory(c *gin.Context, store registryepisodic.EpisodicStore) {
	if !ensureAdmin(c) {
		return
	}
	rawID := c.Param("id")
	memID, err := uuid.Parse(rawID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid memory ID"})
		return
	}
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		if err := store.AdminForceDeleteMemory(ctx, memID); err != nil {
			return err
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminGetMemoryIndexStatus exposes pending-index count for wrapper-native adapters.
func HandleAdminGetMemoryIndexStatus(c *gin.Context, store registryepisodic.EpisodicStore) {
	if !ensureAdmin(c) {
		return
	}
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		count, err := store.AdminCountPendingIndexing(ctx)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"pending": count})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminTriggerMemoryIndex exposes index trigger for wrapper-native adapters.
func HandleAdminTriggerMemoryIndex(c *gin.Context, indexer *service.EpisodicIndexer) {
	if !ensureAdmin(c) {
		return
	}
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
}

// HandleAdminGetMemoryUsage exposes memory usage fetch for wrapper-native adapters.
func HandleAdminGetMemoryUsage(c *gin.Context, store registryepisodic.EpisodicStore, cfg *config.Config) {
	if !ensureAdmin(c) {
		return
	}
	ns := c.QueryArray("ns")
	key := c.Query("key")
	if err := validateNamespace(ns, cfg.EpisodicMaxDepth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		usage, err := store.GetMemoryUsage(ctx, ns, key)
		if err != nil {
			return err
		}
		if usage == nil {
			c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "memory usage not found"})
			return nil
		}
		c.JSON(http.StatusOK, toAPIMemoryUsage(*usage))
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminListTopMemoryUsage exposes top usage listing for wrapper-native adapters.
func HandleAdminListTopMemoryUsage(c *gin.Context, store registryepisodic.EpisodicStore, cfg *config.Config) {
	if !ensureAdmin(c) {
		return
	}
	prefix := c.QueryArray("prefix")
	if len(prefix) > 0 {
		if err := validateNamespace(prefix, cfg.EpisodicMaxDepth); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	sortBy := registryepisodic.MemoryUsageSort(strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort", string(registryepisodic.MemoryUsageSortFetchCount)))))
	switch sortBy {
	case registryepisodic.MemoryUsageSortFetchCount, registryepisodic.MemoryUsageSortLastFetchedAt:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "sort must be one of: fetch_count, last_fetched_at"})
		return
	}

	limit := queryInt(c, "limit", 100)
	if limit <= 0 || limit > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 1000"})
		return
	}

	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		items, err := store.ListTopMemoryUsage(ctx, registryepisodic.ListTopMemoryUsageRequest{
			Prefix: prefix,
			Sort:   sortBy,
			Limit:  limit,
		})
		if err != nil {
			return err
		}

		respItems := make([]gin.H, 0, len(items))
		for _, item := range items {
			respItems = append(respItems, gin.H{
				"namespace": item.Namespace,
				"key":       item.Key,
				"usage":     toAPIMemoryUsage(item.Usage),
			})
		}
		c.JSON(http.StatusOK, gin.H{"items": respItems})
		return nil
	}); err != nil {
		handleError(c, err)
	}
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
	archived := item.ArchivedAt != nil
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
		Usage:      toAPIMemoryUsageRef(item.Usage),
		Score:      item.Score,
		CreatedAt:  &createdAt,
		ExpiresAt:  expiresAt,
		Archived:   &archived,
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

func toAPIMemoryUsage(usage registryepisodic.MemoryUsage) generatedapi.MemoryUsage {
	fetchCount := usage.FetchCount
	lastFetchedAt := usage.LastFetchedAt.UTC()
	return generatedapi.MemoryUsage{
		FetchCount:    &fetchCount,
		LastFetchedAt: &lastFetchedAt,
	}
}

func toAPIMemoryUsageRef(usage *registryepisodic.MemoryUsage) *generatedapi.MemoryUsage {
	if usage == nil {
		return nil
	}
	v := toAPIMemoryUsage(*usage)
	return &v
}

func enrichMemoryItemsWithUsage(ctx context.Context, store registryepisodic.EpisodicStore, items []registryepisodic.MemoryItem) {
	for i := range items {
		usage, err := store.GetMemoryUsage(ctx, items[i].Namespace, items[i].Key)
		if err != nil {
			log.Warn("failed to load memory usage counters", "namespace", items[i].Namespace, "key", items[i].Key, "err", err)
			continue
		}
		items[i].Usage = usage
	}
}

func semanticSearch(c *gin.Context, store registryepisodic.EpisodicStore, embedder registryembed.Embedder, namespacePrefix []string, filter map[string]interface{}, query string, limit int, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryItem, error) {
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
	vectorResults, err := store.SearchMemoryVectors(c.Request.Context(), nsEncoded, embeddings[0], filter, limit, archived)
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

	items, err := store.GetMemoriesByIDs(c.Request.Context(), orderedIDs, archived)
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

func queryBool(c *gin.Context, key string, def bool) bool {
	v := strings.TrimSpace(c.Query(key))
	if v == "" {
		return def
	}
	if strings.EqualFold(v, "1") || strings.EqualFold(v, "true") {
		return true
	}
	if strings.EqualFold(v, "0") || strings.EqualFold(v, "false") {
		return false
	}
	return def
}

func queryPtr(c *gin.Context, key string) *string {
	if v := c.Query(key); v != "" {
		return &v
	}
	return nil
}

func queryTime(c *gin.Context, key string) (*time.Time, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
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
