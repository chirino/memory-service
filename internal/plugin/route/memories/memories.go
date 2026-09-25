package memories

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	generatedadmin "github.com/chirino/memory-service/internal/generated/admin"
	generatedapi "github.com/chirino/memory-service/internal/generated/api"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/route/routetx"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
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

	kindSel := ""
	if req.Kind != nil {
		kindSel = *req.Kind
	}

	var responseStatus int
	var responseBody interface{}
	// Resolve write kind, authorize, project, and persist — all inside one InWriteTx so the kind
	// resolved for authz is the same kind projected and persisted. No second ResolveKindForWrite.
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		// 1. Load and authorize the active row before replacing it, then carry its
		// identity/revision into the store for optimistic predecessor validation.
		var predecessorExpectation *registryepisodic.MemoryPredecessorExpectation
		if policy != nil {
			predecessor, lookupErr := store.GetMemoryPredecessor(ctx, req.Namespace, req.Key)
			if lookupErr != nil {
				return lookupErr
			}
			predecessorExpectation = &registryepisodic.MemoryPredecessorExpectation{}
			if predecessor != nil {
				predecessorExpectation.Exists = true
				predecessorExpectation.ID = predecessor.ID
				predecessorExpectation.Revision = predecessor.Revision
				decision, authzErr := policy.EvaluateAuthz(c.Request.Context(), "write", req.Namespace, req.Key, req.Value, index, predecessor.MemoryKind, pc)
				if authzErr != nil {
					return fmt.Errorf("policy evaluation error: %w", authzErr)
				}
				if !decision.Allow {
					reason := decision.Reason
					if reason == "" {
						reason = "access denied"
					}
					responseStatus = http.StatusForbidden
					responseBody = gin.H{"error": "access denied", "reason": reason}
					return nil
				}
			}
		}
		// 2. Resolve exact canonical target kind once.
		resolvedKind, resolveErr := store.ResolveKindForWrite(ctx, kindSel)
		if resolveErr != nil {
			if errors.Is(resolveErr, registryepisodic.ErrMemoryKindInvalid) ||
				errors.Is(resolveErr, registryepisodic.ErrMemoryKindNotFound) ||
				errors.Is(resolveErr, registryepisodic.ErrMemoryKindNotWritable) {
				return &kindClientError{resolveErr}
			}
			return fmt.Errorf("resolve schema: %w", resolveErr)
		}
		// 3. OPA authz with the exact resolved target kind.
		if policy != nil {
			decision, authzErr := policy.EvaluateAuthz(c.Request.Context(), "write", req.Namespace, req.Key, req.Value, index, resolvedKind, pc)
			if authzErr != nil {
				return fmt.Errorf("policy evaluation error: %w", authzErr)
			}
			if !decision.Allow {
				reason := decision.Reason
				if reason == "" {
					reason = "access denied"
				}
				responseStatus = http.StatusForbidden
				responseBody = gin.H{"error": "access denied", "reason": reason}
				return nil
			}
		}
		// 4. Project with the already-resolved kind (no second resolve).
		policyAttrs, _, err := projectResolvedKind(ctx, store, resolvedKind, req.Namespace, req.Key, req.Value, index)
		if err != nil {
			return err
		}
		result, err := store.PutMemory(ctx, registryepisodic.PutMemoryRequest{
			Namespace:             req.Namespace,
			Key:                   req.Key,
			Value:                 req.Value,
			Index:                 index,
			TTLSeconds:            ttlSeconds,
			PolicyAttributes:      policyAttrs,
			MemoryKind:            resolvedKind,
			ExpectedRevision:      req.ExpectedRevision,
			AuthorizedPredecessor: predecessorExpectation,
		})
		if err != nil {
			return err
		}
		responseStatus = http.StatusOK
		responseBody = toAPIMemoryWriteResult(result)
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	c.JSON(responseStatus, responseBody)
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

	// Authz with actual row kind: look up kind, check authz, read — all in one write tx
	// so the row we authorize matches the row we read.
	pc := policyContext(c)
	var responseStatus int
	var responseBody interface{}
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		// 1. Look up exact kind without loading/decrypting the value.
		// If the row does not exist we still run authz with kind="" so that
		// callers without access get 403 (not 404), preventing existence leakage.
		rowKind := ""
		rowExists := true
		if policy != nil {
			kind, found, kindErr := store.GetMemoryRowKind(ctx, ns, key, archived)
			if kindErr != nil {
				return kindErr
			}
			rowExists = found
			rowKind = kind // empty when not found; authz sees "" kind
			// 2. Authz with the exact row kind (empty string when row not found).
			decision, authzErr := policy.EvaluateAuthz(c.Request.Context(), "read", ns, key, nil, nil, rowKind, pc)
			if authzErr != nil {
				return fmt.Errorf("policy evaluation error: %w", authzErr)
			}
			if !decision.Allow {
				reason := decision.Reason
				if reason == "" {
					reason = "access denied"
				}
				responseStatus = http.StatusForbidden
				responseBody = gin.H{"error": "access denied", "reason": reason}
				return nil
			}
			// Authz passed but row doesn't exist — return 404 now.
			if !rowExists {
				responseStatus = http.StatusNotFound
				responseBody = gin.H{"code": "not_found", "error": "memory not found"}
				return nil
			}
			_ = rowKind
		}
		// 3. Read full item (value is now authorized to be loaded).
		item, err := store.GetMemory(ctx, ns, key, archived)
		if err != nil {
			return err
		}
		if item == nil {
			responseStatus = http.StatusNotFound
			responseBody = gin.H{"code": "not_found", "error": "memory not found"}
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

		responseStatus = http.StatusOK
		responseBody = toAPIMemoryItem(*item)
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	c.JSON(responseStatus, responseBody)
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

	// Authz with actual row kind: look up kind, check authz, archive — all in one write tx.
	// Per Enhancement 115: run authz with kind="" when row absent, return 404 only if authz passes.
	pc := policyContext(c)
	responseStatus := 0
	var responseBody interface{}
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		if policy != nil {
			// 1. Look up exact kind without loading value. Use ArchiveFilterExclude so archived rows
			// are treated as absent for update purposes (consistent with ArchiveMemory semantics).
			rowKind, found, kindErr := store.GetMemoryRowKind(ctx, ns, key, registryepisodic.ArchiveFilterExclude)
			if kindErr != nil {
				return kindErr
			}
			// 2. Authz with exact row kind (empty string when row not found — prevents existence leakage).
			authzKind := rowKind // empty when not found
			decision, authzErr := policy.EvaluateAuthz(c.Request.Context(), "update", ns, key, nil, nil, authzKind, pc)
			if authzErr != nil {
				return fmt.Errorf("policy evaluation error: %w", authzErr)
			}
			if !decision.Allow {
				reason := decision.Reason
				if reason == "" {
					reason = "access denied"
				}
				responseStatus = http.StatusForbidden
				responseBody = gin.H{"error": "access denied", "reason": reason}
				return nil
			}
			// Authz passed but row does not exist — return 404 now.
			if !found {
				responseStatus = http.StatusNotFound
				responseBody = gin.H{"code": "not_found", "error": "memory not found"}
				return nil
			}
		}
		// 3. Archive (idempotent when no active row).
		if err := store.ArchiveMemory(ctx, ns, key, req.ExpectedRevision); err != nil {
			return err
		}
		responseStatus = http.StatusNoContent
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if responseBody != nil {
		c.JSON(responseStatus, responseBody)
	} else {
		c.Status(responseStatus)
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
	if req.Sort != nil && (hasQuery || hasQueries) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sort is not supported with semantic search"})
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
	// Parse and validate schema selector before the semantic-search branch so it applies to all paths.
	memoryKind := ""
	if req.Kind != nil {
		memoryKind = strings.TrimSpace(*req.Kind)
	}
	if err := episodic.ValidateKindSelector(memoryKind); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid kind selector: " + err.Error()})
		return
	}

	effectivePrefix := req.NamespacePrefix
	policyFilter := map[string]interface{}{}
	ki := episodic.KindIntersection{Selector: memoryKind}

	// OPA: inject filter constraints and apply kind intersection.
	if policy != nil {
		pc := policyContext(c)
		var err error
		effectivePrefix, policyFilter, ki, err = policy.InjectFilterPartsWithKind(c.Request.Context(), req.NamespacePrefix, filter, memoryKind, pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
	}
	memoryKind = ki.Selector

	// Empty intersection: caller and policy kind selectors are incompatible → return 200 empty immediately,
	// before any schema/filter validation, embedding, or store query.
	if ki.Empty {
		emptyItems := []generatedapi.MemoryItem{}
		c.JSON(http.StatusOK, generatedapi.SearchMemoriesResponse{Items: &emptyItems})
		return
	}

	// Validate and normalize caller filter fields against selected schema versions.
	// Policy-injected filter fields are built-in and must not be schema-validated.
	// The normalized caller filter is then combined with the normalized policy filter
	// so that canonical values (timestamps, numbers) reach the store.
	// Wrap in a read tx so kind-version lookups use the scoped SQLite handle.
	rawCallerFilter, err := registryepisodic.NormalizeAttributeFilters(filter)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var normalizedFilter registryepisodic.AttributeFilter
	if validationErr := routetx.EpisodicRead(c, store, func(validCtx context.Context) error {
		normalizedCallerFilter, vErr := validateAndNormalizeCallerFilter(validCtx, store, memoryKind, rawCallerFilter)
		if vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "filter validation: " + vErr.Error()})
			return nil
		}
		normalizedPolicyFilter, vErr2 := registryepisodic.NormalizeAttributeFilters(policyFilter)
		if vErr2 != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "policy filter normalization error"})
			return nil
		}
		var mErr error
		normalizedFilter, mErr = registryepisodic.MergeAttributeFilters(normalizedCallerFilter, normalizedPolicyFilter)
		if mErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": mErr.Error()})
			return nil
		}
		return nil
	}); validationErr != nil {
		handleError(c, validationErr)
		return
	}
	// If any validation step already wrote the response, stop here.
	if c.Writer.Written() {
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
			items, err := multiQuerySemanticSearch(c.Request.Context(), store, embedder, effectivePrefix, normalizedFilter, queries, perQueryLimit, limit, archived, memoryKind)
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
			items, err := semanticSearch(c, store, embedder, effectivePrefix, normalizedFilter, query, limit, archived, memoryKind)
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

	// Prepare sort spec (field/direction validated here; type resolution inside tx).
	type pendingSort struct {
		field string
		dir   string
	}
	var reqSort *pendingSort
	if req.Sort != nil {
		if req.Sort.Field == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "sort.field is required"})
			return
		}
		if err := registryepisodic.ValidateAttributeFilterField(req.Sort.Field); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort field: " + err.Error()})
			return
		}
		sortDir := string(generatedapi.Asc)
		if req.Sort.Direction != nil {
			sortDir = string(*req.Sort.Direction)
		}
		reqSort = &pendingSort{field: req.Sort.Field, dir: sortDir}
	}

	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		// Resolve sort field type inside tx so kind-version lookups use scoped handle.
		var sort *registryepisodic.MemoryAttributeSort
		if reqSort != nil {
			attrType, kindErr := resolveKindSortFieldType(ctx, store, memoryKind, reqSort.field)
			if kindErr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort: " + kindErr.Error()})
				return nil
			}
			if attrType == string(episodic.AttributeTypeStringArr) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "sort on string[] attributes is not supported"})
				return nil
			}
			sort = &registryepisodic.MemoryAttributeSort{
				Field:     reqSort.field,
				Direction: reqSort.dir,
				Type:      attrType,
			}
		}
		items, err := store.SearchMemories(ctx, registryepisodic.MemorySearchQuery{
			NamespacePrefix: effectivePrefix,
			Filter:          normalizedFilter,
			Limit:           limit,
			Archived:        archived,
			MemoryKind:      memoryKind,
			Sort:            sort,
		})
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

	// OPA filter injection (narrows prefix and memory kind based on caller identity).
	effectiveKind := ""
	var effectiveFilter registryepisodic.AttributeFilter
	if policy != nil {
		pc := policyContext(c)
		var err error
		var ki episodic.KindIntersection
		var policyFilter map[string]interface{}
		prefix, policyFilter, ki, err = policy.InjectFilterPartsWithKind(c.Request.Context(), prefix, map[string]interface{}{}, "", pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
		if ki.Empty {
			empty := [][]string{}
			c.JSON(http.StatusOK, generatedapi.ListMemoryNamespacesResponse{Namespaces: &empty})
			return
		}
		effectiveKind = ki.Selector
		effectiveFilter, err = registryepisodic.NormalizeAttributeFilters(policyFilter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
	}

	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		namespaces, err := store.ListNamespaces(ctx, registryepisodic.ListNamespacesRequest{
			Prefix:     prefix,
			Suffix:     suffix,
			MaxDepth:   maxDepth,
			Archived:   archived,
			MemoryKind: effectiveKind,
			Filter:     effectiveFilter,
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

	// OPA filter injection (narrows namespace prefix and memory kind).
	effectiveKind := ""
	var effectiveFilter registryepisodic.AttributeFilter
	if policy != nil {
		pc := policyContext(c)
		var err error
		var ki episodic.KindIntersection
		var policyFilter map[string]interface{}
		nsPrefix, policyFilter, ki, err = policy.InjectFilterPartsWithKind(c.Request.Context(), nsPrefix, map[string]interface{}{}, "", pc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter injection error"})
			return
		}
		if ki.Empty {
			events := []generatedapi.MemoryEventItem{}
			c.JSON(http.StatusOK, generatedapi.ListMemoryEventsResponse{Events: &events})
			return
		}
		effectiveKind = ki.Selector
		effectiveFilter, err = registryepisodic.NormalizeAttributeFilters(policyFilter)
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
		MemoryKind:      effectiveKind,
		Filter:          effectiveFilter,
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
		MemoryKind: e.MemoryKind,
	}
}

// ensureAdmin must be called by every /admin/v1/memories write handler. The generated
// proxy permission/scope checks alone let non-OIDC fixture users reach admin writes
// when no OIDC scope gate is configured.
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
		MemoryKind:      strings.TrimSpace(c.Query("kind")),
	}
	if err := episodic.ValidateKindSelector(query.MemoryKind); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid kind selector: " + err.Error()})
		return
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
	// Capture raw filter string for deferred validation inside the read transaction.
	rawFilter := c.Query("filter")
	var rawNormalizedFilter registryepisodic.AttributeFilter
	if rawFilter != "" {
		var filterMap map[string]interface{}
		if err := json.Unmarshal([]byte(rawFilter), &filterMap); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filter: " + err.Error()})
			return
		}
		normalizedFilter, err := registryepisodic.NormalizeAttributeFilters(filterMap)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		rawNormalizedFilter = normalizedFilter
	}
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		// Validate filter inside transaction so kind-version lookups use the scoped handle.
		if rawFilter != "" {
			validatedFilter, vErr := validateAndNormalizeCallerFilter(ctx, store, query.MemoryKind, rawNormalizedFilter)
			if vErr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "filter validation: " + vErr.Error()})
				return nil
			}
			query.Filter = validatedFilter
		}
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
	if req.Sort != nil && (hasAdminQuery || hasAdminQueries) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sort is not supported with semantic search"})
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
	adminMemoryKind := ""
	if req.Kind != nil {
		adminMemoryKind = strings.TrimSpace(*req.Kind)
	}
	if err := episodic.ValidateKindSelector(adminMemoryKind); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid kind selector: " + err.Error()})
		return
	}
	adminKI := episodic.KindIntersection{Selector: adminMemoryKind}
	if req.AsUserId != nil && strings.TrimSpace(*req.AsUserId) != "" {
		if policy == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "memory policy is not configured"})
			return
		}
		var err error
		effectivePrefix, policyFilter, adminKI, err = policy.InjectFilterPartsWithKind(c.Request.Context(), prefix, filter, adminMemoryKind, episodic.PolicyContext{
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
	adminMemoryKind = adminKI.Selector

	// Empty intersection: return 200 empty immediately before schema/filter/store work.
	if adminKI.Empty {
		c.JSON(http.StatusOK, gin.H{"items": []interface{}{}})
		return
	}

	// Validate and normalize caller filter; combine with policy filter.
	// Wrap in a read tx so kind-version lookups use the scoped SQLite handle.
	rawAdminCallerFilter, err := registryepisodic.NormalizeAttributeFilters(filter)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var normalizedFilter registryepisodic.AttributeFilter
	if validationErr := routetx.EpisodicRead(c, store, func(validCtx context.Context) error {
		normalizedAdminCallerFilter, vErr := validateAndNormalizeCallerFilter(validCtx, store, adminMemoryKind, rawAdminCallerFilter)
		if vErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "filter validation: " + vErr.Error()})
			return nil
		}
		normalizedAdminPolicyFilter, vErr2 := registryepisodic.NormalizeAttributeFilters(policyFilter)
		if vErr2 != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "policy filter normalization error"})
			return nil
		}
		var mErr error
		normalizedFilter, mErr = registryepisodic.MergeAttributeFilters(normalizedAdminCallerFilter, normalizedAdminPolicyFilter)
		if mErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": mErr.Error()})
			return nil
		}
		return nil
	}); validationErr != nil {
		handleError(c, validationErr)
		return
	}
	if c.Writer.Written() {
		return
	}

	keyPrefix := ""
	if req.KeyPrefix != nil {
		keyPrefix = *req.KeyPrefix
	}
	includeUsage := req.IncludeUsage != nil && *req.IncludeUsage

	// Prepare sort spec (field/direction validated here; type resolution inside tx).
	type adminPendingSort struct {
		field string
		dir   string
	}
	var reqAdminSort *adminPendingSort
	if req.Sort != nil {
		if req.Sort.Field == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "sort.field is required"})
			return
		}
		if err := registryepisodic.ValidateAttributeFilterField(req.Sort.Field); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort field: " + err.Error()})
			return
		}
		adminSortDir := string(generatedadmin.Asc)
		if req.Sort.Direction != nil {
			adminSortDir = string(*req.Sort.Direction)
		}
		reqAdminSort = &adminPendingSort{field: req.Sort.Field, dir: adminSortDir}
	}

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
			items, err := multiQuerySemanticSearch(c.Request.Context(), store, embedder, effectivePrefix, normalizedFilter, queries, perQueryLimit, limit, archived, adminMemoryKind)
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
		// Resolve sort field type inside tx so kind-version lookups use scoped handle.
		var adminSort *registryepisodic.MemoryAttributeSort
		if reqAdminSort != nil {
			attrType, kindErr := resolveKindSortFieldType(ctx, store, adminMemoryKind, reqAdminSort.field)
			if kindErr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sort: " + kindErr.Error()})
				return nil
			}
			if attrType == string(episodic.AttributeTypeStringArr) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "sort on string[] attributes is not supported"})
				return nil
			}
			adminSort = &registryepisodic.MemoryAttributeSort{
				Field:     reqAdminSort.field,
				Direction: reqAdminSort.dir,
				Type:      attrType,
			}
		}
		var items []registryepisodic.MemoryItem
		var err error
		if query != "" {
			if embedder == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "semantic search unavailable"})
				return nil
			}
			items, err = semanticSearch(c, store, embedder, effectivePrefix, normalizedFilter, query, limit, archived, adminMemoryKind)
		} else {
			items, err = store.AdminSearchMemories(ctx, registryepisodic.AdminMemorySearchQuery{
				NamespacePrefix: effectivePrefix,
				KeyPrefix:       keyPrefix,
				Filter:          normalizedFilter,
				Archived:        archived,
				Limit:           limit,
				IncludeUsage:    includeUsage,
				MemoryKind:      adminMemoryKind,
				Sort:            adminSort,
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

// HandleAdminCreateMemoryKindVersion creates an immutable schema version.
func HandleAdminCreateMemoryKindVersion(c *gin.Context, store registryepisodic.EpisodicStore) {
	if !ensureAdmin(c) {
		return
	}
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic store is not configured"})
		return
	}
	var req generatedadmin.CreateMemoryKindVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Parse and validate the canonical name.
	if _, _, err := episodic.ParseCanonicalKindName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	attributes := map[string]string{}
	if req.Attributes != nil {
		attributes = req.Attributes
	}
	if err := episodic.ValidateKindAttributeTypes(attributes); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Compile the Rego source if provided; empty means no attribute projection.
	version := model.MemoryKindVersion{
		Name:           req.Name,
		AttributeTypes: attributes,
		Writable:       true,
		CreatedAt:      time.Now().UTC(),
	}
	if req.Writable != nil {
		version.Writable = *req.Writable
	}
	if req.ProjectionRego != "" {
		if _, err := episodic.CompileKindProjection(c.Request.Context(), req.ProjectionRego); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		version.AttributesRego = &req.ProjectionRego
	}
	var result *model.MemoryKindVersion
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		var err error
		result, err = store.CreateMemoryKindVersion(ctx, version)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		if errors.Is(err, registryepisodic.ErrMemoryKindVersionConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, kindVersionToAPI(result, true))
}

// HandleAdminListMemoryKindVersions lists schema versions.
func HandleAdminListMemoryKindVersions(c *gin.Context, store registryepisodic.EpisodicStore) {
	if !ensureAdmin(c) {
		return
	}
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic store is not configured"})
		return
	}
	family := c.Query("family")
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		versions, err := store.ListMemoryKindVersions(ctx, family)
		if err != nil {
			return err
		}
		items := make([]generatedadmin.MemoryKindVersion, 0, len(versions))
		for i := range versions {
			items = append(items, kindVersionToAPI(&versions[i], false))
		}
		c.JSON(http.StatusOK, generatedadmin.ListMemoryKindVersionsResponse{Items: &items})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminGetMemoryKindVersion retrieves a schema version by family/version.
func HandleAdminGetMemoryKindVersion(c *gin.Context, store registryepisodic.EpisodicStore, family, version string) {
	if !ensureAdmin(c) {
		return
	}
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic store is not configured"})
		return
	}
	name := family + "/" + version
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		v, err := store.GetMemoryKindVersion(ctx, name)
		if err != nil {
			return err
		}
		if v == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "schema version not found"})
			return nil
		}
		c.JSON(http.StatusOK, kindVersionToAPI(v, true))
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminCreateMemoryKindMigration creates a migration job.
func HandleAdminCreateMemoryKindMigration(c *gin.Context, store registryepisodic.EpisodicStore, _ registrystore.MemoryStore) {
	if !ensureAdmin(c) {
		return
	}
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic store is not configured"})
		return
	}
	var req generatedadmin.CreateMemoryKindMigrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Validate source and target names.
	srcFamily, _, parseErr := episodic.ParseCanonicalKindName(req.Source)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid source: " + parseErr.Error()})
		return
	}
	tgtFamily, _, parseErr2 := episodic.ParseCanonicalKindName(req.Target)
	if parseErr2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target: " + parseErr2.Error()})
		return
	}
	if req.Source == req.Target {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and target must differ"})
		return
	}
	if srcFamily != tgtFamily {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and target must belong to the same family"})
		return
	}
	// Verify source and target exist within a read tx, then create migration in a write tx.
	var src, target *model.MemoryKindVersion
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		var err error
		src, err = store.GetMemoryKindVersion(ctx, req.Source)
		if err != nil {
			return err
		}
		if src == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "source schema version not found"})
			return nil
		}
		target, err = store.GetMemoryKindVersion(ctx, req.Target)
		if err != nil {
			return err
		}
		if target == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "target schema version not found"})
			return nil
		}
		if !target.Writable {
			c.JSON(http.StatusBadRequest, gin.H{"error": "target schema version is not writable"})
			return nil
		}
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if src == nil || target == nil {
		return // response already written (404/400 written inside the read tx)
	}
	if !target.Writable {
		return // response already written (400 written inside the read tx)
	}
	var nsPrefix []string
	if req.NamespacePrefix != nil {
		for i, seg := range *req.NamespacePrefix {
			if seg == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("namespace_prefix[%d] must not be empty", i)})
				return
			}
		}
		nsPrefix = *req.NamespacePrefix
	}
	m := model.MemoryKindMigration{
		ID:              uuid.New(),
		Source:          req.Source,
		Target:          req.Target,
		NamespacePrefix: nsPrefix,
		State:           model.MigrationStateQueued,
		CreatedAt:       time.Now().UTC(),
	}
	var result *model.MemoryKindMigration
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		var err error
		result, err = store.CreateMemoryKindMigrationAndTask(ctx, m)
		return err
	}); err != nil {
		if errors.Is(err, registryepisodic.ErrMemoryKindMigrationActiveForSource) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, kindMigrationToAPI(c.Request.Context(), store, result))
}

// HandleAdminListMemoryKindMigrations lists schema migrations.
func HandleAdminListMemoryKindMigrations(c *gin.Context, store registryepisodic.EpisodicStore) {
	if !ensureAdmin(c) {
		return
	}
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic store is not configured"})
		return
	}
	state := c.Query("state")
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		migrations, err := store.ListMemoryKindMigrations(ctx, state)
		if err != nil {
			return err
		}
		items := make([]generatedadmin.MemoryKindMigration, 0, len(migrations))
		for i := range migrations {
			items = append(items, kindMigrationToAPI(ctx, store, &migrations[i]))
		}
		c.JSON(http.StatusOK, generatedadmin.ListMemoryKindMigrationsResponse{Items: &items})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminGetMemoryKindMigration retrieves a migration by ID.
func HandleAdminGetMemoryKindMigration(c *gin.Context, store registryepisodic.EpisodicStore, id openapi_types.UUID) {
	if !ensureAdmin(c) {
		return
	}
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic store is not configured"})
		return
	}
	if err := routetx.EpisodicRead(c, store, func(ctx context.Context) error {
		m, err := store.GetMemoryKindMigration(ctx, uuid.UUID(id))
		if err != nil {
			return err
		}
		if m == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "migration not found"})
			return nil
		}
		c.JSON(http.StatusOK, kindMigrationToAPI(ctx, store, m))
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// HandleAdminCancelMemoryKindMigration requests cancellation of a migration.
func HandleAdminCancelMemoryKindMigration(c *gin.Context, store registryepisodic.EpisodicStore, id openapi_types.UUID) {
	if !ensureAdmin(c) {
		return
	}
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "episodic store is not configured"})
		return
	}
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		return store.UpdateMemoryKindMigrationCancelRequested(ctx, uuid.UUID(id))
	}); err != nil {
		if errors.Is(err, registryepisodic.ErrMemoryKindMigrationNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "migration not found"})
			return
		}
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- helpers ---

func kindVersionToAPI(v *model.MemoryKindVersion, includeRego bool) generatedadmin.MemoryKindVersion {
	var attributes *map[string]string
	if v.AttributeTypes != nil {
		t := map[string]string(v.AttributeTypes)
		attributes = &t
	}
	res := generatedadmin.MemoryKindVersion{
		Name:       &v.Name,
		Attributes: attributes,
		Writable:   &v.Writable,
		CreatedAt:  &v.CreatedAt,
	}
	if includeRego && v.AttributesRego != nil {
		res.ProjectionRego = v.AttributesRego
	}
	return res
}

// kindMigrationVectorPending returns a live count of rows awaiting re-index for
// the given migration.  The count is always computed dynamically so convergence
// is visible for ALL states (including terminal) as the indexer processes rows.
//
// Item 7 fix: wrap CountMemoriesPendingIndexByKind in InReadTx (required by SQLite)
// and remove the State guard so terminal states also show live count.
func kindMigrationVectorPending(ctx context.Context, store registryepisodic.EpisodicStore, m *model.MemoryKindMigration) int64 {
	if store == nil {
		return m.VectorPendingCount
	}
	var count int64
	if err := store.InReadTx(ctx, func(rCtx context.Context) error {
		var err error
		count, err = store.CountMemoriesPendingIndexByKind(rCtx, m.Target, m.NamespacePrefix)
		return err
	}); err != nil {
		log.Warn("kindMigrationVectorPending: count failed, falling back to stored value",
			"migration", m.ID, "err", err)
		return m.VectorPendingCount
	}
	return count
}

func kindMigrationToAPI(ctx context.Context, store registryepisodic.EpisodicStore, m *model.MemoryKindMigration) generatedadmin.MemoryKindMigration {
	id := openapi_types.UUID(m.ID)
	state := generatedadmin.MemoryKindMigrationState(m.State)
	vectorPending := kindMigrationVectorPending(ctx, store, m)
	return generatedadmin.MemoryKindMigration{
		Id:                    &id,
		Source:                &m.Source,
		Target:                &m.Target,
		NamespacePrefix:       &m.NamespacePrefix,
		State:                 &state,
		CancelRequested:       &m.CancelRequested,
		MigratedCount:         &m.MigratedCount,
		SkippedTombstoneCount: &m.SkippedTombstoneCount,
		VectorPendingCount:    &vectorPending,
		RetryCount:            &m.RetryCount,
		LastErrorCode:         m.LastErrorCode,
		CreatedAt:             &m.CreatedAt,
		StartedAt:             m.StartedAt,
		CompletedAt:           m.CompletedAt,
	}
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
		return store.AdminForceDeleteMemory(ctx, memID)
	}); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
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

// kindClientError wraps a schema resolution error that should be reported as
// a 400 Bad Request rather than an internal server error.
type kindClientError struct{ cause error }

func (e *kindClientError) Error() string { return e.cause.Error() }
func (e *kindClientError) Unwrap() error { return e.cause }

func handleError(c *gin.Context, err error) {
	_ = c.Error(err)
	if errors.Is(err, registryepisodic.ErrMemoryRevisionConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "memory revision conflict"})
		return
	}
	var kindErr *kindClientError
	if errors.As(err, &kindErr) {
		c.JSON(http.StatusBadRequest, gin.H{"error": kindErr.Error()})
		return
	}
	log.Error("episodic route error", "err", err, "stack", string(debug.Stack()))
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

// kindVersionList loads schema versions for a given kind selector and returns
// them as episodic.KindVersionList for use with ResolveSortFieldType and
// ValidateCallerFilterField.  For an exact selector it returns a single-element
// list; for family/empty selectors it lists all versions in the family (or all
// versions when empty).
func kindVersionList(ctx context.Context, store registryepisodic.EpisodicStore, memoryKind string) (episodic.KindVersionList, string, error) {
	if store == nil {
		return nil, "", nil
	}
	_, exactCanonical, isExact := episodic.ParseKindSelector(memoryKind)
	if isExact {
		sv, err := store.GetMemoryKindVersion(ctx, exactCanonical)
		if err != nil {
			return nil, exactCanonical, err
		}
		if sv == nil {
			return nil, exactCanonical, fmt.Errorf("schema version %q not found", exactCanonical)
		}
		return episodic.KindVersionList{{Name: sv.Name, AttributeTypes: sv.AttributeTypes}}, exactCanonical, nil
	}
	// Family or all-schemas selector.
	family, _, _ := episodic.ParseKindSelector(memoryKind)
	if family == episodic.DefaultKindFamily && memoryKind == "" {
		family = "" // empty = all families
	}
	all, err := store.ListMemoryKindVersions(ctx, family)
	if err != nil {
		return nil, "", err
	}
	list := make(episodic.KindVersionList, 0, len(all))
	for _, sv := range all {
		v := sv
		list = append(list, struct {
			Name           string
			AttributeTypes map[string]string
		}{Name: v.Name, AttributeTypes: v.AttributeTypes})
	}
	return list, "", nil
}

// resolveKindSortFieldType looks up the declared attribute type for a sort field
// using the shared ResolveSortFieldType helper across all selected schema versions.
// Defect 7 fix: family/empty selectors now resolve against all matching versions.
// Call inside a routetx.EpisodicRead/EpisodicWrite scope; the SQLite store panics
// on dbFor outside a scope.
func resolveKindSortFieldType(ctx context.Context, store registryepisodic.EpisodicStore, memoryKind, field string) (string, error) {
	if store == nil {
		return "", nil
	}
	versions, exactName, err := kindVersionList(ctx, store, memoryKind)
	if err != nil {
		return "", fmt.Errorf("load schema versions: %w", err)
	}
	return episodic.ResolveSortFieldType(field, exactName, versions)
}

// validateAndNormalizeCallerFilter validates and normalizes caller-supplied filter conditions
// against the selected schema versions.  It validates field declarations/operators and
// rewrites condition values (timestamp canonicalization, numeric normalization, type-check).
// Returns a new AttributeFilter with normalized values for use in store queries.
// Policy-injected filters are NOT passed here.
// Call inside a routetx.EpisodicRead/EpisodicWrite scope; the SQLite store panics
// on dbFor outside a scope.
func validateAndNormalizeCallerFilter(ctx context.Context, store registryepisodic.EpisodicStore, memoryKind string, callerFilter registryepisodic.AttributeFilter) (registryepisodic.AttributeFilter, error) {
	if store == nil || callerFilter.Empty() {
		return callerFilter, nil
	}
	versions, exactName, err := kindVersionList(ctx, store, memoryKind)
	if err != nil {
		return registryepisodic.AttributeFilter{}, fmt.Errorf("load schema versions for filter validation: %w", err)
	}
	if len(versions) == 0 {
		// No schema versions at all → untyped, permit any filter; return as-is.
		return callerFilter, nil
	}
	out := registryepisodic.AttributeFilter{
		Conditions: make([]registryepisodic.AttributeFilterCondition, 0, len(callerFilter.Conditions)),
	}
	for _, cond := range callerFilter.Conditions {
		// Validate field declaration + operator.
		if err := episodic.ValidateCallerFilterField(cond.Field, string(cond.Op), exactName, versions); err != nil {
			return registryepisodic.AttributeFilter{}, fmt.Errorf("filter on %q: %w", cond.Field, err)
		}
		// Resolve the field type; for $exists there are no values to normalize.
		fieldType, _ := episodic.ResolveSortFieldType(cond.Field, exactName, versions)
		rawValues := make([]interface{}, 0, len(cond.Values))
		for _, v := range cond.Values {
			rawValues = append(rawValues, v.Raw)
		}
		normalizedRaw, err := episodic.ValidateAndNormalizeCallerFilterValues(cond.Field, string(cond.Op), fieldType, rawValues)
		if err != nil {
			return registryepisodic.AttributeFilter{}, fmt.Errorf("filter on %q values: %w", cond.Field, err)
		}
		// Rebuild the condition with normalized values.
		normalizedCond := registryepisodic.AttributeFilterCondition{
			Field:     cond.Field,
			Op:        cond.Op,
			RangeKind: normalizedRangeKindForType(fieldType, cond.RangeKind),
		}
		for _, rv := range normalizedRaw {
			normalizedCond.Values = append(normalizedCond.Values, registryepisodic.AttributeFilterValue{
				Raw:  rv,
				Text: fmt.Sprintf("%v", rv),
			})
		}
		out.Conditions = append(out.Conditions, normalizedCond)
	}
	return out, nil
}

// normalizedRangeKindForType returns the RangeKind appropriate for the declared field type.
// For schema-typed fields this overrides the request-shape-inferred kind.
// For untyped fields (legacy/empty), the existing inferred kind is preserved.
func normalizedRangeKindForType(fieldType string, existing registryepisodic.AttributeFilterRangeKind) registryepisodic.AttributeFilterRangeKind {
	switch episodic.AttributeType(fieldType) {
	case episodic.AttributeTypeNumber:
		return registryepisodic.AttributeFilterRangeNumber
	case episodic.AttributeTypeTimestamp:
		return registryepisodic.AttributeFilterRangeTime
	default:
		return existing
	}
}

// resolveKindProjection resolves the canonical schema for a write, loads the schema version,
// evaluates the Rego projection, and returns the policy attributes and resolved schema name.
// It must be called inside a write transaction scope (ctx already holds the tx handle).
func resolveKindProjection(ctx context.Context, store registryepisodic.EpisodicStore, kindSel string, namespace []string, key string, value map[string]interface{}, index map[string]string) (map[string]interface{}, string, error) {
	canonicalName, err := store.ResolveKindForWrite(ctx, kindSel)
	if err != nil {
		if errors.Is(err, registryepisodic.ErrMemoryKindInvalid) || errors.Is(err, registryepisodic.ErrMemoryKindNotFound) || errors.Is(err, registryepisodic.ErrMemoryKindNotWritable) {
			return nil, "", &kindClientError{err}
		}
		return nil, "", fmt.Errorf("resolve schema: %w", err)
	}
	return projectResolvedKind(ctx, store, canonicalName, namespace, key, value, index)
}

// projectResolvedKind loads and evaluates the Rego projection for an already-resolved canonical
// kind name. It must be called inside a write transaction scope. Use this after resolving the
// kind once (e.g. for authz) to avoid a second ResolveKindForWrite call.
func projectResolvedKind(ctx context.Context, store registryepisodic.EpisodicStore, canonicalName string, namespace []string, key string, value map[string]interface{}, index map[string]string) (map[string]interface{}, string, error) {
	sv, err := store.GetMemoryKindVersion(ctx, canonicalName)
	if err != nil {
		return nil, canonicalName, fmt.Errorf("load schema version %q: %w", canonicalName, err)
	}
	if sv == nil || sv.AttributesRego == nil || *sv.AttributesRego == "" {
		return map[string]interface{}{}, canonicalName, nil
	}
	pq, err := episodic.CompileKindProjection(ctx, *sv.AttributesRego)
	if err != nil {
		return nil, canonicalName, fmt.Errorf("compile schema %q: %w", canonicalName, err)
	}
	raw, err := episodic.EvaluateKindProjection(ctx, pq, namespace, key, value, index)
	if err != nil {
		return nil, canonicalName, fmt.Errorf("evaluate schema %q: %w", canonicalName, err)
	}
	attrs, err := episodic.ValidateAndNormalizeKindProjection(raw, sv.AttributeTypes)
	if err != nil {
		// Projection type violation is a client error (bad value for the chosen schema).
		return nil, canonicalName, &kindClientError{fmt.Errorf("schema %q projection invalid: %w", canonicalName, err)}
	}
	return attrs, canonicalName, nil
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
	resp := generatedapi.MemoryWriteResult{
		Id:         &id,
		Namespace:  &namespace,
		Key:        &key,
		Attributes: mapRef(result.Attributes),
		CreatedAt:  &createdAt,
		ExpiresAt:  expiresAt,
		Revision:   &revision,
		Kind:       result.MemoryKind,
	}
	return resp
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
		Kind:       item.MemoryKind,
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
		Kind:       item.MemoryKind,
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

// semanticSearchMaxOverfetch is the maximum vector-fetch limit used by the
// bounded iterative overfetch loop (defect 6 fix). This is a best-effort
// heuristic: if all kind-matching rows rank beyond this position in the full
// similarity ranking, the result count may be silently below the requested
// limit. See WORKAROUNDS.md "Semantic search kind post-filter" for details.
const semanticSearchMaxOverfetch = 1000

// semanticSearch performs single-query semantic search with bounded iterative
// overfetch so that kind-selector post-filtering never silently drops results
// below the requested limit while higher-ranked non-matching candidates exist.
//
// Defect 6 fix: the previous implementation called SearchMemoryVectors(limit)
// then filtered by kind, returning fewer than limit even when more candidates
// remained.  Now we overfetch in increasing rounds until we have at least limit
// matching results or the vector backend has no more candidates.
func semanticSearch(c *gin.Context, store registryepisodic.EpisodicStore, embedder registryembed.Embedder, namespacePrefix []string, filter registryepisodic.AttributeFilter, query string, limit int, archived registryepisodic.ArchiveFilter, memoryKind string) ([]registryepisodic.MemoryItem, error) {
	embeddings, err := embedder.EmbedTexts(c.Request.Context(), []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, nil
	}
	nsEncoded := ""
	if len(namespacePrefix) > 0 {
		nsEncoded, err = episodic.EncodeNamespace(namespacePrefix, 0)
		if err != nil {
			return nil, err
		}
	}
	return semanticSearchWithKindFilter(c.Request.Context(), store, nsEncoded, embeddings[0], filter, limit, archived, memoryKind)
}

// semanticSearchWithKindFilter is the implementation shared by REST and gRPC paths.
// It performs bounded iterative overfetch until the requested limit is filled or
// the vector backend has no more candidates.
//
// Qdrant stale-candidate overfetch, ordering, dedupe and limit are preserved.
func semanticSearchWithKindFilter(ctx context.Context, store registryepisodic.EpisodicStore, nsEncoded string, embedding []float32, filter registryepisodic.AttributeFilter, limit int, archived registryepisodic.ArchiveFilter, memoryKind string) ([]registryepisodic.MemoryItem, error) {
	// Overfetch multiplier: each round doubles the fetch window until we either
	// fill limit matching results or reach the hard ceiling.
	fetchLimit := limit * 4
	if fetchLimit < limit+10 {
		fetchLimit = limit + 10
	}
	if fetchLimit > semanticSearchMaxOverfetch {
		fetchLimit = semanticSearchMaxOverfetch
	}

	// Tracks all vector hits accumulated across rounds (ID → best hit).
	type vectorHit struct {
		score            float64
		revision         int64
		schema           string
		primaryValidated bool // true = SQL JOIN already enforced freshness
	}
	bestByID := make(map[uuid.UUID]vectorHit)
	orderedIDs := make([]uuid.UUID, 0)

	// Item 4 fix: no hard iteration ceiling — loop until limit is filled or
	// the backend is exhausted (returns fewer than fetchLimit rows).
	// The hard ceiling is semanticSearchMaxOverfetch applied per round.
	var results []registryepisodic.MemoryItem
	for {
		vectorResults, err := store.SearchMemoryVectors(ctx, nsEncoded, embedding, filter, memoryKind, fetchLimit, archived)
		if err != nil {
			return nil, fmt.Errorf("search memory vectors: %w", err)
		}
		if len(vectorResults) == 0 {
			break
		}

		// Accumulate deduplicated hits.
		for _, vr := range vectorResults {
			if prev, exists := bestByID[vr.MemoryID]; !exists {
				bestByID[vr.MemoryID] = vectorHit{score: vr.Score, revision: vr.MemoryRevision, schema: vr.MemoryKind, primaryValidated: vr.PrimaryValidated}
				orderedIDs = append(orderedIDs, vr.MemoryID)
			} else if vr.Score > prev.score {
				bestByID[vr.MemoryID] = vectorHit{score: vr.Score, revision: prev.revision, schema: prev.schema, primaryValidated: prev.primaryValidated}
			}
		}

		// Fetch primary rows.
		items, err := store.GetMemoriesByIDs(ctx, orderedIDs, archived)
		if err != nil {
			return nil, fmt.Errorf("get memories by ids: %w", err)
		}
		itemByID := make(map[uuid.UUID]registryepisodic.MemoryItem, len(items))
		for _, item := range items {
			hit := bestByID[item.ID]
			// Item 3: when PrimaryValidated==true the SQL JOIN already enforced both
			// revision and schema freshness — accept the row unconditionally.
			if !hit.primaryValidated {
				// Schema freshness check (only for unvalidated/Qdrant hits).
				effectiveItemKind := item.MemoryKind
				if hit.schema != "" {
					// Vector hit has a schema — primary row must match exactly.
					if hit.schema != effectiveItemKind {
						continue // stale vector
					}
				} else {
					// Vector hit has NO schema — reject as stale (every fresh row has a kind).
					continue
				}
				// Revision freshness check (only for unvalidated/Qdrant hits).
				if hit.revision <= 0 || item.Revision != hit.revision {
					continue
				}
			}
			itemByID[item.ID] = item
		}

		// Collect kind-matching results in score order.
		results = results[:0]
		for _, id := range orderedIDs {
			item, ok := itemByID[id]
			if !ok {
				continue
			}
			if !matchesKindSelector(item.MemoryKind, memoryKind) {
				continue
			}
			score := bestByID[id].score
			item.Score = &score
			results = append(results, item)
		}
		sort.SliceStable(results, func(i, j int) bool {
			return *results[i].Score > *results[j].Score
		})

		// Have we filled the limit, or did the backend return fewer than we asked for
		// (meaning no more candidates exist)?
		if len(results) >= limit || len(vectorResults) < fetchLimit {
			break
		}

		// Double the fetch window for the next round (bounded by the hard ceiling).
		prevFetchLimit := fetchLimit
		fetchLimit = fetchLimit * 2
		if fetchLimit > semanticSearchMaxOverfetch {
			fetchLimit = semanticSearchMaxOverfetch
		}
		// Bug 3: if fetchLimit did not grow (already at hard cap), the backend
		// is returning exactly the cap and we cannot make further progress.
		// Break to prevent an infinite loop.
		if fetchLimit == prevFetchLimit {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// matchesKindSelector returns true if the item's schema matches the requested selector.
//
// sel == ""  → all schemas match (empty sel = no restriction)
// sel == "family/version" → exact canonical match
// sel == "family" → prefix match (item schema starts with "family/")
func matchesKindSelector(itemSchema, sel string) bool {
	if sel == "" {
		return true
	}
	if strings.Contains(sel, "/") {
		return itemSchema == sel
	}
	return strings.HasPrefix(itemSchema, sel+"/")
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

// mqCandidateEligible reports whether a multi-query vector hit is eligible for RRF.
//
// Rules (Defect B fix):
//   - A primary row must exist.
//   - Kind selector must always match regardless of PrimaryValidated.
//   - PrimaryValidated=true only skips freshness (schema/revision) checks.
//   - PrimaryValidated=false: schema must match (or legacy rules apply) and revision must match.
//
// vectorSchema / vectorRevision are from the vector index entry.
// primaryKind / primaryRevision are from the live primary row.
func mqCandidateEligible(
	vectorSchema string,
	vectorRevision int64,
	primaryValidated bool,
	primaryKind string,
	primaryRevision int64,
	memoryKind string,
) bool {
	// Kind must always match, regardless of PrimaryValidated.
	if !matchesKindSelector(primaryKind, memoryKind) {
		return false
	}
	if primaryValidated {
		// SQL JOIN already validated freshness and schema; skip further checks.
		return true
	}
	// Freshness: schema and revision checks.
	if vectorSchema != "" {
		if vectorSchema != primaryKind {
			return false // stale schema
		}
	} else {
		// Empty vector schema — reject as stale (every fresh row carries a kind).
		return false
	}
	if vectorRevision <= 0 || primaryRevision != vectorRevision {
		return false // stale revision
	}
	return true
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
	memoryKind string,
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

	// revByID, kindByID, and validatedByID track vector-indexed metadata for stale-check.
	// Per-ID values are set on first encounter; the single-query path does the same.
	// validatedByID[id]==true means SQL JOIN already enforced freshness.
	revByID := make(map[uuid.UUID]int64)
	kindByID := make(map[uuid.UUID]string)
	validatedByID := make(map[uuid.UUID]bool)

	// For each query, iteratively overfetch to collect perQueryLimit eligible hits.
	// Bugs 4+5: single-shot SearchMemoryVectors with perQueryLimit lets stale/wrong-kind
	// hits consume the per-query top-k before any primary validation.  We must validate
	// primary rows first, then use only eligible hits for RRF.
	for qi, q := range queries {
		if qi >= len(embeddings) {
			break
		}
		// Overfetch per query: start at perQueryLimit*4, double until enough eligible hits
		// found or backend is exhausted or hard cap reached.
		qFetchLimit := perQueryLimit * 4
		if qFetchLimit < perQueryLimit+10 {
			qFetchLimit = perQueryLimit + 10
		}
		if qFetchLimit > semanticSearchMaxOverfetch {
			qFetchLimit = semanticSearchMaxOverfetch
		}

		type qHit struct {
			score            float64
			revision         int64
			schema           string
			primaryValidated bool
		}
		qBestByID := make(map[uuid.UUID]qHit)
		qOrder := make([]uuid.UUID, 0) // score-descending encounter order

		for {
			vectorResults, err := store.SearchMemoryVectors(ctx, nsEncoded, embeddings[qi], filter, memoryKind, qFetchLimit, archived)
			if err != nil {
				return nil, fmt.Errorf("search memory vectors (query %d): %w", qi, err)
			}
			if len(vectorResults) == 0 {
				break
			}
			for _, vr := range vectorResults {
				if _, dup := qBestByID[vr.MemoryID]; !dup {
					qBestByID[vr.MemoryID] = qHit{score: vr.Score, revision: vr.MemoryRevision, schema: vr.MemoryKind, primaryValidated: vr.PrimaryValidated}
					qOrder = append(qOrder, vr.MemoryID)
				}
			}
			// Fetch primary rows for all accumulated candidates.
			allIDs := make([]uuid.UUID, 0, len(qBestByID))
			for id := range qBestByID {
				allIDs = append(allIDs, id)
			}
			primaryItems, err := store.GetMemoriesByIDs(ctx, allIDs, archived)
			if err != nil {
				return nil, fmt.Errorf("get memories by ids (query %d): %w", qi, err)
			}
			primaryByID := make(map[uuid.UUID]registryepisodic.MemoryItem, len(primaryItems))
			for _, item := range primaryItems {
				primaryByID[item.ID] = item
			}
			// Count eligible hits using the shared predicate.
			// Kind must always match regardless of PrimaryValidated (Defect B fix).
			eligibleCount := 0
			for _, id := range qOrder {
				hit := qBestByID[id]
				primary, primaryFound := primaryByID[id]
				if !primaryFound {
					continue // no primary row at all
				}
				if mqCandidateEligible(hit.schema, hit.revision, hit.primaryValidated, primary.MemoryKind, primary.Revision, memoryKind) {
					eligibleCount++
				}
			}
			// Stop if we have enough eligible hits or backend is exhausted or at cap.
			prevQFetchLimit := qFetchLimit
			if eligibleCount >= perQueryLimit || len(vectorResults) < qFetchLimit {
				break
			}
			qFetchLimit = qFetchLimit * 2
			if qFetchLimit > semanticSearchMaxOverfetch {
				qFetchLimit = semanticSearchMaxOverfetch
			}
			if qFetchLimit == prevQFetchLimit {
				break
			}
		}

		// Compute eligible hits in score-descending order; assign RRF ranks.
		// Fetch primary rows once more for the final selection.
		allIDs := make([]uuid.UUID, 0, len(qBestByID))
		for id := range qBestByID {
			allIDs = append(allIDs, id)
		}
		primaryItems, err := store.GetMemoriesByIDs(ctx, allIDs, archived)
		if err != nil {
			return nil, fmt.Errorf("get memories by ids (query %d final): %w", qi, err)
		}
		primaryByID := make(map[uuid.UUID]registryepisodic.MemoryItem, len(primaryItems))
		for _, item := range primaryItems {
			primaryByID[item.ID] = item
		}

		rank := 1
		for _, id := range qOrder {
			if rank > perQueryLimit {
				break
			}
			hit := qBestByID[id]
			primary, primaryFound := primaryByID[id]
			if !primaryFound {
				continue // no primary row
			}
			if !mqCandidateEligible(hit.schema, hit.revision, hit.primaryValidated, primary.MemoryKind, primary.Revision, memoryKind) {
				continue
			}
			// Record for RRF.
			entry, exists := accum[id]
			if !exists {
				entry = &rrfEntry{
					purposeSet: make(map[string]struct{}),
					firstSeen:  len(seenOrder),
				}
				accum[id] = entry
				seenOrder = append(seenOrder, id)
				revByID[id] = hit.revision
				kindByID[id] = hit.schema
				validatedByID[id] = hit.primaryValidated
			}
			entry.rrfScore += 1.0 / (rrfK + float64(rank))
			if hit.score > entry.bestRaw {
				entry.bestRaw = hit.score
			}
			if _, already := entry.purposeSet[q.Purpose]; !already {
				entry.purposeSet[q.Purpose] = struct{}{}
				entry.purposes = append(entry.purposes, q.Purpose)
			}
			rank++
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

	// Item 4 fix: fetch ALL scored IDs, filter, then apply limit.
	// Truncating topIDs before GetMemoriesByIDs lets stale hits consume quota.
	items, err := store.GetMemoriesByIDs(ctx, seenOrder, archived)
	if err != nil {
		return nil, fmt.Errorf("get memories by ids: %w", err)
	}

	itemByID := make(map[uuid.UUID]registryepisodic.MemoryItem, len(items))
	for _, item := range items {
		// Final guard: apply full eligibility including kind (Defect B fix).
		// validatedByID/revByID/kindByID reflect the first-encounter vector metadata.
		if !mqCandidateEligible(kindByID[item.ID], revByID[item.ID], validatedByID[item.ID], item.MemoryKind, item.Revision, memoryKind) {
			continue
		}
		itemByID[item.ID] = item
	}
	// Assemble results preserving RRF order, attach scores and attribution.
	// Apply limit AFTER filtering so stale hits don't consume quota.
	results := make([]registryepisodic.MemoryItem, 0, limit)
	for _, id := range seenOrder {
		if len(results) >= limit {
			break
		}
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

// HandleAdminPutMemory handles PUT /admin/v1/memories for admin clients.
// Admin writes are authorized by admin role/scope/justification and intentionally
// bypass episodic OPA authz (policy is unused on purpose); kind projection uses only
// the persisted namespace/key/value/index inputs.
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

	kindSel := ""
	if req.Kind != nil {
		kindSel = *req.Kind
	}
	var result *registryepisodic.MemoryWriteResult
	if err := routetx.EpisodicWrite(c, store, func(ctx context.Context) error {
		policyAttrs, resolvedKind, err := resolveKindProjection(ctx, store, kindSel, req.Namespace, req.Key, req.Value, index)
		if err != nil {
			return err
		}
		result, err = store.PutMemory(ctx, registryepisodic.PutMemoryRequest{
			Namespace:        req.Namespace,
			Key:              req.Key,
			Value:            req.Value,
			Index:            index,
			TTLSeconds:       ttlSeconds,
			PolicyAttributes: policyAttrs,
			MemoryKind:       resolvedKind,
			ExpectedRevision: req.ExpectedRevision,
		})
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAPIMemoryWriteResult(result))
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
