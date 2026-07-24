package memories

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	generatedadmin "github.com/chirino/memory-service/internal/generated/admin"
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
	if ttlSeconds < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ttl_seconds must be >= 0"})
		return
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
				ExpectedRevision: req.ExpectedRevision,
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
			Namespace:        req.Namespace,
			Key:              req.Key,
			Value:            req.Value,
			Index:            index,
			TTLSeconds:       ttlSeconds,
			ExpectedRevision: req.ExpectedRevision,
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

// HandlePutMemory handles the generated public put-memory operation.
func HandlePutMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	putMemory(c, store, policy, cfg)
}

// HandleGetMemory handles the generated public get-memory operation.
func HandleGetMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, params generatedapi.GetMemoryParams) {
	getMemoryWithParams(c, store, policy, cfg, params)
}

// HandleUpdateMemory handles the generated public update-memory operation.
func HandleUpdateMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, params generatedapi.UpdateMemoryParams) {
	updateMemoryWithParams(c, store, policy, cfg, params)
}

// HandleSearchMemories handles the generated public memory-search operation.
func HandleSearchMemories(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, embedder registryembed.Embedder) {
	searchMemories(c, store, policy, cfg, embedder)
}

// HandleListMemoryNamespaces handles the generated public namespace-list operation.
func HandleListMemoryNamespaces(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, params generatedapi.ListMemoryNamespacesParams) {
	listNamespacesWithParams(c, store, policy, cfg, params)
}

// HandleListMemoryEvents handles the generated public memory-event-list operation.
func HandleListMemoryEvents(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, params generatedapi.ListMemoryEventsParams) {
	listMemoryEventsWithParams(c, store, policy, cfg, params)
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
		if err := store.ArchiveMemory(ctx, ns, key, req.ExpectedRevision); err != nil {
			return err
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func searchMemories(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, embedder registryembed.Embedder) {
	if err := rejectObsoleteSearchFields(c); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var req generatedapi.SearchMemoriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.NamespacePrefix) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "namespace_prefix is required"})
		return
	}
	// Validate mutual exclusivity of query and queries.
	hasQuery := req.Query != nil && strings.TrimSpace(*req.Query) != ""
	hasQueries := req.Queries != nil && len(*req.Queries) > 0
	if hasQuery && hasQueries {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query and queries are mutually exclusive"})
		return
	}
	if req.Queries != nil && len(*req.Queries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "queries must not be empty when present"})
		return
	}

	limit := config.ClampPageSize(c.Request.Context(), 10)
	if req.Limit != nil {
		if err := config.ValidatePageSize(c.Request.Context(), *req.Limit); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		limit = *req.Limit
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
	policyFilter := map[string]interface{}{}

	// OPA: inject filter constraints.
	if policy != nil {
		pc := policyContext(c)
		var err error
		effectivePrefix, policyFilter, err = policy.InjectFilterParts(c.Request.Context(), req.NamespacePrefix, filter, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
	}
	normalizedFilter, err := registryepisodic.NormalizeAttributeFilters(filter, policyFilter)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Multi-query semantic search.
	if hasQueries {
		queries, err := toSearchQuerySpecs(*req.Queries)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		perQueryLimit, err := effectivePerQueryLimit(limit, req.PerQueryLimit)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if embedder == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
			return
		}
		if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
			items, err := multiQuerySemanticSearch(c.Request.Context(), store, embedder, effectivePrefix, normalizedFilter, queries, perQueryLimit, limit, archived)
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
			if errors.Is(err, registryepisodic.ErrSemanticSearchUnavailable) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
				return
			}
			handleError(c, err)
			return
		}
		return
	}

	// Single-query semantic search.
	query := ""
	if req.Query != nil {
		query = strings.TrimSpace(*req.Query)
	}
	if query != "" {
		if embedder == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
			return
		}
		if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
			items, err := semanticSearch(c, store, embedder, effectivePrefix, normalizedFilter, query, limit, archived)
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
			if errors.Is(err, registryepisodic.ErrSemanticSearchUnavailable) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
				return
			}
			handleError(c, err)
			return
		}
		return
	}

	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		items, err := store.SearchMemories(ctx, effectivePrefix, normalizedFilter, limit, archived)
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

func rejectObsoleteSearchFields(c *gin.Context) error {
	body, err := c.GetRawData()
	if err != nil {
		return err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	for _, key := range []string{"offset", "order", "after_cursor"} {
		if _, ok := raw[key]; ok {
			return fmt.Errorf("%s is not supported for memory search", key)
		}
	}
	return nil
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

	limit := config.ClampPageSize(c.Request.Context(), 50)
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

func ensureAdmin(c *gin.Context) bool {
	security.RequireAdminRole()(c)
	return !c.IsAborted()
}

func ensureAdminOrAuditor(c *gin.Context) bool {
	if security.EffectiveAdminRole(c) == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin or auditor role required"})
		return false
	}
	return true
}

// HandleAdminListMemories exposes memory listing for admin/auditor exploration.
func HandleAdminListMemories(c *gin.Context, store registryepisodic.EpisodicStore, cfg *config.Config) {
	if !ensureAdminOrAuditor(c) {
		return
	}
	archived, err := registryepisodic.ParseArchiveFilter(c.DefaultQuery("archived", string(registryepisodic.ArchiveFilterExclude)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	prefix := c.QueryArray("namespacePrefix")
	if len(prefix) > 0 {
		if err := validateNamespace(prefix, cfg.EpisodicMaxDepth); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	query := registryepisodic.AdminMemoryQuery{
		NamespacePrefix: prefix,
		KeyPrefix:       c.Query("keyPrefix"),
		Archived:        archived,
		Limit:           queryInt(c, "limit", 50),
		AfterCursor:     c.Query("afterCursor"),
		IncludeUsage:    queryBool(c, "includeUsage", false),
	}
	if query.CreatedAfter, err = queryTimePtr(c, "createdAfter"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if query.CreatedBefore, err = queryTimePtr(c, "createdBefore"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if query.ExpiresBefore, err = queryTimePtr(c, "expiresBefore"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := config.ValidatePageSize(c.Request.Context(), query.Limit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		page, err := store.AdminListMemories(ctx, query)
		if err != nil {
			return err
		}
		if query.IncludeUsage {
			enrichMemoryItemsWithUsage(ctx, store, page.Items)
		}
		respItems := toAdminMemoryItems(page.Items)
		afterCursor := nullableString(page.AfterCursor)
		c.JSON(http.StatusOK, generatedadmin.AdminListMemoriesResponse{Items: &respItems, AfterCursor: afterCursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminGetMemory exposes direct memory-by-ID reads for admin/auditor exploration.
func HandleAdminGetMemory(c *gin.Context, store registryepisodic.EpisodicStore) {
	if !ensureAdminOrAuditor(c) {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid memory id"})
		return
	}
	includeUsage := queryBool(c, "includeUsage", false)
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		item, err := store.AdminGetMemoryByID(ctx, id)
		if err != nil {
			return err
		}
		if item == nil {
			c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "memory not found"})
			return nil
		}
		if includeUsage {
			items := []registryepisodic.MemoryItem{*item}
			enrichMemoryItemsWithUsage(ctx, store, items)
			item = &items[0]
		}
		c.JSON(http.StatusOK, toAdminMemoryItem(*item))
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminSearchMemories exposes bounded memory search for admin/auditor exploration.
func HandleAdminSearchMemories(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config, embedder registryembed.Embedder) {
	if !ensureAdminOrAuditor(c) {
		return
	}
	var req generatedadmin.AdminSearchMemoriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	prefix := []string{}
	if req.NamespacePrefix != nil {
		prefix = *req.NamespacePrefix
	}
	if len(prefix) > 0 {
		if err := validateNamespace(prefix, cfg.EpisodicMaxDepth); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	limit := config.ClampPageSize(c.Request.Context(), 10)
	if req.Limit != nil {
		limit = *req.Limit
	}
	if err := config.ValidatePageSize(c.Request.Context(), limit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Validate mutual exclusivity of query and queries.
	hasAdminQuery := req.Query != nil && strings.TrimSpace(*req.Query) != ""
	hasAdminQueries := req.Queries != nil && len(*req.Queries) > 0
	if hasAdminQuery && hasAdminQueries {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query and queries are mutually exclusive"})
		return
	}
	if req.Queries != nil && len(*req.Queries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "queries must not be empty when present"})
		return
	}

	archived := registryepisodic.ArchiveFilterExclude
	if req.Archived != nil {
		parsed, err := registryepisodic.ParseArchiveFilter(string(*req.Archived))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		archived = parsed
	}
	filter := map[string]interface{}{}
	if req.Filter != nil {
		filter = *req.Filter
	}
	effectivePrefix := prefix
	policyFilter := map[string]interface{}{}
	if req.AsUserId != nil && strings.TrimSpace(*req.AsUserId) != "" {
		if policy == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "memory policy is not configured"})
			return
		}
		var err error
		effectivePrefix, policyFilter, err = policy.InjectFilterParts(c.Request.Context(), prefix, filter, episodic.PolicyContext{
			UserID:   strings.TrimSpace(*req.AsUserId),
			ClientID: security.GetClientID(c),
			JWTClaims: map[string]interface{}{
				"roles": []string{},
			},
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
	}
	normalizedFilter, err := registryepisodic.NormalizeAttributeFilters(filter, policyFilter)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	keyPrefix := ""
	if req.KeyPrefix != nil {
		keyPrefix = *req.KeyPrefix
	}
	includeUsage := req.IncludeUsage != nil && *req.IncludeUsage

	// Multi-query semantic search.
	if hasAdminQueries {
		queries, err := toAdminSearchQuerySpecs(*req.Queries)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		perQueryLimit, err := effectivePerQueryLimit(limit, req.PerQueryLimit)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if embedder == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
			return
		}
		if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
			items, err := multiQuerySemanticSearch(c.Request.Context(), store, embedder, effectivePrefix, normalizedFilter, queries, perQueryLimit, limit, archived)
			if err != nil {
				return err
			}
			if keyPrefix != "" {
				items = filterMemoryItemsByKeyPrefix(items, keyPrefix)
			}
			if includeUsage {
				enrichMemoryItemsWithUsage(ctx, store, items)
			}
			respItems := toAdminMemoryItems(items)
			c.JSON(http.StatusOK, generatedadmin.AdminSearchMemoriesResponse{Items: &respItems})
			return nil
		}); err != nil {
			if errors.Is(err, registryepisodic.ErrSemanticSearchUnavailable) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
				return
			}
			handleError(c, err)
			return
		}
		return
	}

	query := ""
	if req.Query != nil {
		query = strings.TrimSpace(*req.Query)
	}
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		var items []registryepisodic.MemoryItem
		var err error
		if query != "" {
			if embedder == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
				return nil
			}
			items, err = semanticSearch(c, store, embedder, effectivePrefix, normalizedFilter, query, limit, archived)
		} else {
			items, err = store.AdminSearchMemories(ctx, registryepisodic.AdminMemorySearchQuery{
				NamespacePrefix: effectivePrefix,
				KeyPrefix:       keyPrefix,
				Filter:          normalizedFilter,
				Archived:        archived,
				Limit:           limit,
				IncludeUsage:    includeUsage,
			})
		}
		if err != nil {
			return err
		}
		if keyPrefix != "" && query != "" {
			items = filterMemoryItemsByKeyPrefix(items, keyPrefix)
		}
		if includeUsage {
			enrichMemoryItemsWithUsage(ctx, store, items)
		}
		respItems := toAdminMemoryItems(items)
		c.JSON(http.StatusOK, generatedadmin.AdminSearchMemoriesResponse{Items: &respItems})
		return nil
	}); err != nil {
		if errors.Is(err, registryepisodic.ErrSemanticSearchUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
			return
		}
		handleError(c, err)
	}
}

// HandleAdminListMemoryNamespaces exposes namespace browsing for admin/auditor exploration.
func HandleAdminListMemoryNamespaces(c *gin.Context, store registryepisodic.EpisodicStore, cfg *config.Config) {
	if !ensureAdminOrAuditor(c) {
		return
	}
	archived, err := registryepisodic.ParseArchiveFilter(c.DefaultQuery("archived", string(registryepisodic.ArchiveFilterExclude)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	prefix := c.QueryArray("namespacePrefix")
	if len(prefix) > 0 {
		if err := validateNamespace(prefix, cfg.EpisodicMaxDepth); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	limit := queryInt(c, "limit", 200)
	if err := config.ValidatePageSize(c.Request.Context(), limit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	query := registryepisodic.AdminNamespaceQuery{
		NamespacePrefix: prefix,
		Suffix:          c.QueryArray("suffix"),
		MaxDepth:        queryInt(c, "maxDepth", 0),
		Archived:        archived,
		Limit:           limit,
		AfterCursor:     c.Query("afterCursor"),
	}
	if query.MaxDepth < 0 || query.MaxDepth > cfg.EpisodicMaxDepth {
		c.JSON(http.StatusBadRequest, gin.H{"error": "maxDepth out of range"})
		return
	}
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		page, err := store.AdminListNamespaces(ctx, query)
		if err != nil {
			return err
		}
		namespaces := make([]generatedadmin.AdminMemoryNamespace, 0, len(page.Namespaces))
		for _, ns := range page.Namespaces {
			segments := append([]string(nil), ns...)
			namespaces = append(namespaces, generatedadmin.AdminMemoryNamespace{Segments: &segments})
		}
		afterCursor := nullableString(page.AfterCursor)
		c.JSON(http.StatusOK, generatedadmin.AdminListMemoryNamespacesResponse{Namespaces: &namespaces, AfterCursor: afterCursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
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
	if err := config.ValidatePageSize(c.Request.Context(), limit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

func adminMemoryPolicyContext(c *gin.Context) episodic.PolicyContext {
	id := security.GetIdentity(c)
	return episodic.PolicyContext{
		UserID:   "",
		ClientID: id.ClientID,
		JWTClaims: map[string]interface{}{
			"roles": []string{security.RoleAdmin},
		},
	}
}

func handleError(c *gin.Context, err error) {
	_ = c.Error(err)
	if errors.Is(err, registryepisodic.ErrMemoryRevisionConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "memory revision conflict"})
		return
	}
	log.Error("episodic route error", "err", err, "stack", string(debug.Stack()))
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func toAPIMemoryWriteResult(result *registryepisodic.MemoryWriteResult) generatedapi.MemoryWriteResult {
	id := openapi_types.UUID(result.ID)
	namespace := append([]string(nil), result.Namespace...)
	key := result.Key
	createdAt := result.CreatedAt.UTC()
	revision := result.Revision
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
		Revision:   &revision,
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
	out := generatedapi.MemoryItem{
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
	if len(item.MatchedQueries) > 0 {
		mq := append([]string(nil), item.MatchedQueries...)
		out.MatchedQueries = &mq
	}
	return out
}

func toAPIMemoryItems(items []registryepisodic.MemoryItem) []generatedapi.MemoryItem {
	out := make([]generatedapi.MemoryItem, 0, len(items))
	for _, item := range items {
		out = append(out, toAPIMemoryItem(item))
	}
	return out
}

func toAdminMemoryItem(item registryepisodic.MemoryItem) generatedadmin.AdminMemoryItem {
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
	var archivedAt *time.Time
	if item.ArchivedAt != nil {
		t := item.ArchivedAt.UTC()
		archivedAt = &t
	}
	revision := item.Revision
	out := generatedadmin.AdminMemoryItem{
		Id:         &id,
		Namespace:  &namespace,
		Key:        &key,
		Value:      mapRef(item.Value),
		Attributes: mapRef(item.Attributes),
		Usage:      toAdminMemoryUsageRef(item.Usage),
		Score:      item.Score,
		CreatedAt:  &createdAt,
		ExpiresAt:  expiresAt,
		ArchivedAt: archivedAt,
		Archived:   &archived,
		Revision:   &revision,
	}
	if len(item.MatchedQueries) > 0 {
		mq := append([]string(nil), item.MatchedQueries...)
		out.MatchedQueries = &mq
	}
	return out
}

func toAdminMemoryItems(items []registryepisodic.MemoryItem) []generatedadmin.AdminMemoryItem {
	out := make([]generatedadmin.AdminMemoryItem, 0, len(items))
	for _, item := range items {
		out = append(out, toAdminMemoryItem(item))
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

func toAdminMemoryUsage(usage registryepisodic.MemoryUsage) generatedadmin.MemoryUsageResponse {
	fetchCount := usage.FetchCount
	lastFetchedAt := usage.LastFetchedAt.UTC()
	return generatedadmin.MemoryUsageResponse{
		FetchCount:    &fetchCount,
		LastFetchedAt: &lastFetchedAt,
	}
}

func toAdminMemoryUsageRef(usage *registryepisodic.MemoryUsage) *generatedadmin.MemoryUsageResponse {
	if usage == nil {
		return nil
	}
	v := toAdminMemoryUsage(*usage)
	return &v
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func queryTimePtr(c *gin.Context, name string) (*time.Time, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be RFC3339", name)
	}
	return &t, nil
}

func filterMemoryItemsByKeyPrefix(items []registryepisodic.MemoryItem, keyPrefix string) []registryepisodic.MemoryItem {
	if keyPrefix == "" {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if strings.HasPrefix(item.Key, keyPrefix) {
			out = append(out, item)
		}
	}
	return out
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

func semanticSearch(c *gin.Context, store registryepisodic.EpisodicStore, embedder registryembed.Embedder, namespacePrefix []string, filter registryepisodic.AttributeFilter, query string, limit int, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryItem, error) {
	embeddings, err := embedder.EmbedTexts(c.Request.Context(), []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, nil
	}

	nsEncoded := ""
	if len(namespacePrefix) > 0 {
		var err error
		nsEncoded, err = episodic.EncodeNamespace(namespacePrefix, 0)
		if err != nil {
			return nil, err
		}
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

// searchQuerySpec carries text and attribution label for one query in a multi-query search.
type searchQuerySpec struct {
	Text    string
	Purpose string // attribution label; equals Text when not explicitly provided
}

// toSearchQuerySpecs converts the API query array to internal specs.
func toSearchQuerySpecs(queries []generatedapi.MemorySearchQuery) ([]searchQuerySpec, error) {
	out := make([]searchQuerySpec, 0, len(queries))
	for i, q := range queries {
		text := strings.TrimSpace(q.Text)
		if text == "" {
			return nil, fmt.Errorf("queries[%d].text must not be empty", i)
		}
		purpose := text
		if q.Purpose != nil && strings.TrimSpace(*q.Purpose) != "" {
			purpose = strings.TrimSpace(*q.Purpose)
		}
		out = append(out, searchQuerySpec{Text: text, Purpose: purpose})
	}
	return out, nil
}

// toAdminSearchQuerySpecs converts the admin API query array to internal specs.
func toAdminSearchQuerySpecs(queries []generatedadmin.AdminMemorySearchQuery) ([]searchQuerySpec, error) {
	out := make([]searchQuerySpec, 0, len(queries))
	for i, q := range queries {
		text := strings.TrimSpace(q.Text)
		if text == "" {
			return nil, fmt.Errorf("queries[%d].text must not be empty", i)
		}
		purpose := text
		if q.Purpose != nil && strings.TrimSpace(*q.Purpose) != "" {
			purpose = strings.TrimSpace(*q.Purpose)
		}
		out = append(out, searchQuerySpec{Text: text, Purpose: purpose})
	}
	return out, nil
}

// multiQuerySemanticSearch executes vector search for each query independently,
// deduplicates results using Reciprocal Rank Fusion (RRF, k=60), and attaches
// query attribution to each result.
func multiQuerySemanticSearch(
	ctx context.Context,
	store registryepisodic.EpisodicStore,
	embedder registryembed.Embedder,
	namespacePrefix []string,
	filter registryepisodic.AttributeFilter,
	queries []searchQuerySpec,
	perQueryLimit int,
	limit int,
	archived registryepisodic.ArchiveFilter,
) ([]registryepisodic.MemoryItem, error) {
	// Validate all query texts are non-empty.
	texts := make([]string, 0, len(queries))
	for _, q := range queries {
		if q.Text == "" {
			return nil, fmt.Errorf("query text must not be empty")
		}
		texts = append(texts, q.Text)
	}

	// Embed all queries in one batched call.
	embeddings, err := embedder.EmbedTexts(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed queries: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, nil
	}
	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("embed queries: expected %d embeddings, got %d", len(texts), len(embeddings))
	}

	nsEncoded := ""
	if len(namespacePrefix) > 0 {
		nsEncoded, err = episodic.EncodeNamespace(namespacePrefix, 0)
		if err != nil {
			return nil, err
		}
	}

	const rrfK = 60.0

	// rrfAccum accumulates RRF scores and attribution per memory ID.
	type rrfEntry struct {
		rrfScore   float64
		bestRaw    float64
		purposes   []string
		purposeSet map[string]struct{}
		firstSeen  int // order of first encounter for stable tie-breaking
	}
	accum := make(map[uuid.UUID]*rrfEntry)
	seenOrder := make([]uuid.UUID, 0)

	// For each query, run vector search and accumulate RRF contributions.
	for qi, q := range queries {
		if qi >= len(embeddings) {
			break
		}
		vectorResults, err := store.SearchMemoryVectors(ctx, nsEncoded, embeddings[qi], filter, perQueryLimit, archived)
		if err != nil {
			return nil, fmt.Errorf("search memory vectors (query %d): %w", qi, err)
		}
		// Build rank from this query's results (1-based rank by result order).
		// SearchMemoryVectors returns results in score-descending order.
		seen := make(map[uuid.UUID]bool, len(vectorResults))
		rank := 1
		for _, vr := range vectorResults {
			if seen[vr.MemoryID] {
				continue
			}
			seen[vr.MemoryID] = true
			contrib := 1.0 / (rrfK + float64(rank))
			rank++
			entry, exists := accum[vr.MemoryID]
			if !exists {
				entry = &rrfEntry{
					purposeSet: make(map[string]struct{}),
					firstSeen:  len(seenOrder),
				}
				accum[vr.MemoryID] = entry
				seenOrder = append(seenOrder, vr.MemoryID)
			}
			entry.rrfScore += contrib
			if vr.Score > entry.bestRaw {
				entry.bestRaw = vr.Score
			}
			if _, already := entry.purposeSet[q.Purpose]; !already {
				entry.purposeSet[q.Purpose] = struct{}{}
				entry.purposes = append(entry.purposes, q.Purpose)
			}
		}
	}

	if len(seenOrder) == 0 {
		return nil, nil
	}

	// Sort IDs by RRF score descending; ties broken by best raw score then first-seen order.
	sort.SliceStable(seenOrder, func(i, j int) bool {
		ei, ej := accum[seenOrder[i]], accum[seenOrder[j]]
		if ei.rrfScore != ej.rrfScore {
			return ei.rrfScore > ej.rrfScore
		}
		if ei.bestRaw != ej.bestRaw {
			return ei.bestRaw > ej.bestRaw
		}
		return ei.firstSeen < ej.firstSeen
	})

	// Apply overall limit before fetching full items.
	topIDs := seenOrder
	if len(topIDs) > limit {
		topIDs = topIDs[:limit]
	}

	// Fetch full memory items.
	items, err := store.GetMemoriesByIDs(ctx, topIDs, archived)
	if err != nil {
		return nil, fmt.Errorf("get memories by ids: %w", err)
	}
	itemByID := make(map[uuid.UUID]registryepisodic.MemoryItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}

	// Assemble results preserving RRF order, attach scores and attribution.
	results := make([]registryepisodic.MemoryItem, 0, len(topIDs))
	for _, id := range topIDs {
		item, ok := itemByID[id]
		if !ok {
			continue
		}
		entry := accum[id]
		rrfScore := entry.rrfScore
		item.Score = &rrfScore
		item.MatchedQueries = append([]string(nil), entry.purposes...)
		results = append(results, item)
	}
	return results, nil
}

const maxPerQueryLimit = 100

func effectivePerQueryLimit(limit int, requested *int) (int, error) {
	if requested != nil {
		if *requested <= 0 || *requested > maxPerQueryLimit {
			return 0, fmt.Errorf("per_query_limit must be between 1 and %d", maxPerQueryLimit)
		}
		return *requested, nil
	}
	return min(limit, maxPerQueryLimit), nil
}

func queryInt(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return config.ClampPageSize(c.Request.Context(), def)
	}
	var i int
	if _, err := fmt.Sscanf(v, "%d", &i); err != nil {
		return config.ClampPageSize(c.Request.Context(), def)
	}
	return config.ClampPageSize(c.Request.Context(), i)
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

// HandleAdminPutMemory handles PUT /admin/v1/memories for admin clients
func HandleAdminPutMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	if !ensureAdmin(c) {
		return
	}
	var req generatedadmin.PutMemoryRequest
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
	if ttlSeconds < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ttl_seconds must be >= 0"})
		return
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

	policyAttrs := map[string]interface{}{}
	if policy != nil {
		// Admin memory writes are authorized by admin role/scope/justification
		// before this handler. Do not run user OPA authz here; only run
		// attribute extraction with a neutral admin context for indexing/search.
		pc := adminMemoryPolicyContext(c)
		extracted, err := policy.ExtractAttributes(c.Request.Context(), req.Namespace, req.Key, req.Value, index, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "attribute extraction error"})
			return
		}
		policyAttrs = extracted
	}

	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		result, err := store.PutMemory(ctx, registryepisodic.PutMemoryRequest{
			Namespace:        req.Namespace,
			Key:              req.Key,
			Value:            req.Value,
			Index:            index,
			TTLSeconds:       ttlSeconds,
			PolicyAttributes: policyAttrs,
			ExpectedRevision: req.ExpectedRevision,
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

// HandleAdminUpdateMemory handles PATCH /admin/v1/memories for admin clients
func HandleAdminUpdateMemory(c *gin.Context, store registryepisodic.EpisodicStore, policy *episodic.PolicyEngine, cfg *config.Config) {
	if !ensureAdmin(c) {
		return
	}
	params := generatedapi.UpdateMemoryParams{
		Ns:  c.QueryArray("ns"),
		Key: c.Query("key"),
	}
	if len(params.Ns) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "namespace is required"})
		return
	}
	if params.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	if err := validateNamespace(params.Ns, cfg.EpisodicMaxDepth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req generatedadmin.UpdateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Archived == nil || !*req.Archived {
		c.JSON(http.StatusBadRequest, gin.H{"error": "archived must be true"})
		return
	}

	// Admin memory updates are authorized by admin role/scope/justification
	// before this handler. They intentionally bypass user OPA authz because
	// archive is an administrative operation across namespaces.
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		return store.ArchiveMemory(ctx, params.Ns, params.Key, req.ExpectedRevision)
	}); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
