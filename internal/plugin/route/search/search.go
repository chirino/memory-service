package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MountRoutes mounts search routes.
func MountRoutes(r *gin.Engine, store registrystore.MemoryStore, cfg *config.Config, auth gin.HandlerFunc, embedder registryembed.Embedder, vectorStore registryvector.VectorStore) {
	g := r.Group("/v1", auth)

	g.POST("/conversations/search", func(c *gin.Context) {
		searchConversations(c, store, cfg, embedder, vectorStore)
	})
	g.POST("/conversations/index", func(c *gin.Context) {
		indexConversations(c, store)
	})
	g.GET("/conversations/unindexed", func(c *gin.Context) {
		listUnindexed(c, store)
	})
}

func searchConversations(c *gin.Context, store registrystore.MemoryStore, cfg *config.Config, embedder registryembed.Embedder, vectorStore registryvector.VectorStore) {
	userID := security.GetUserID(c)

	var req struct {
		Query        string `json:"query"        binding:"required"`
		Limit        *int   `json:"limit"`
		IncludeEntry *bool  `json:"includeEntry"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
		return
	}

	limit := 20
	if req.Limit != nil {
		if *req.Limit <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "limit must be greater than 0"})
			return
		}
		limit = *req.Limit
	}
	includeEntry := true
	if req.IncludeEntry != nil {
		includeEntry = *req.IncludeEntry
	}

	// Try semantic search first (Java "auto" mode: semantic → fulltext fallback).
	if cfg.SearchSemanticEnabled && embedder != nil && vectorStore != nil && vectorStore.IsEnabled() {
		results, err := doSemanticSearch(c.Request.Context(), store, embedder, vectorStore, userID, req.Query, limit, includeEntry)
		if err != nil {
			log.Warn("Semantic search failed, falling back to fulltext", "err", err)
		} else if len(results) > 0 {
			c.JSON(http.StatusOK, gin.H{"data": results, "afterCursor": nil})
			return
		}
	}

	// Fulltext fallback.
	if cfg != nil && !cfg.SearchFulltextEnabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "search_disabled", "error": "full-text search is disabled"})
		return
	}

	results, err := store.SearchEntries(c.Request.Context(), userID, req.Query, limit, includeEntry)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": results.Data, "afterCursor": results.AfterCursor})
}

func doSemanticSearch(ctx context.Context, store registrystore.MemoryStore, embedder registryembed.Embedder, vectorStore registryvector.VectorStore, userID, query string, limit int, includeEntry bool) ([]registrystore.SearchResult, error) {
	groupIDs, err := store.ListConversationGroupIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list group IDs: %w", err)
	}
	if len(groupIDs) == 0 {
		return nil, nil
	}

	embeddings, err := embedder.EmbedTexts(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	vectorResults, err := vectorStore.Search(ctx, embeddings[0], groupIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	if len(vectorResults) == 0 {
		return nil, nil
	}

	scores := make(map[uuid.UUID]float64, len(vectorResults))
	entryIDs := make([]uuid.UUID, len(vectorResults))
	for i, r := range vectorResults {
		entryIDs[i] = r.EntryID
		scores[r.EntryID] = r.Score
	}

	details, err := store.FetchSearchResultDetails(ctx, userID, entryIDs, includeEntry)
	if err != nil {
		return nil, fmt.Errorf("fetch details: %w", err)
	}

	for i := range details {
		details[i].Score = scores[details[i].EntryID]
		details[i].Kind = vectorStore.Name()
	}
	sort.Slice(details, func(i, j int) bool {
		return details[i].Score > details[j].Score
	})
	return details, nil
}

func indexConversations(c *gin.Context, store registrystore.MemoryStore) {
	// Role check: indexer or admin required.
	if !security.HasRole(c, security.RoleIndexer) && !security.HasRole(c, security.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": "indexer or admin role required"})
		return
	}

	// Accept both bare array [{...}] and wrapped {"entries": [{...}]} formats.
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var entries []registrystore.IndexEntryRequest

	// Try bare array first.
	if err := json.Unmarshal(bodyBytes, &entries); err != nil {
		// Try wrapped format.
		var wrapped struct {
			Entries []registrystore.IndexEntryRequest `json:"entries"`
		}
		if err2 := json.Unmarshal(bodyBytes, &wrapped); err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		entries = wrapped.Entries
	}

	if len(entries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one entry required"})
		return
	}

	// Validate required fields.
	for _, entry := range entries {
		if entry.EntryID == uuid.Nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "entryId is required"})
			return
		}
		if entry.ConversationID == uuid.Nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "conversationId is required"})
			return
		}
		if entry.IndexedContent == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "indexedContent is required"})
			return
		}
	}

	result, err := store.IndexEntries(c.Request.Context(), entries)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func listUnindexed(c *gin.Context, store registrystore.MemoryStore) {
	// Role check: indexer or admin required.
	if !security.HasRole(c, security.RoleIndexer) && !security.HasRole(c, security.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": "indexer or admin role required"})
		return
	}

	limit := 20
	if v := c.Query("limit"); v != "" {
		var l int
		if _, err := fmt.Sscanf(v, "%d", &l); err == nil && l > 0 {
			limit = l
		}
	}
	afterCursor := queryPtr(c, "afterCursor")

	entries, cursor, err := store.ListUnindexedEntries(c.Request.Context(), limit, afterCursor)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": entries, "afterCursor": cursor})
}

func handleError(c *gin.Context, err error) {
	var notFound *registrystore.NotFoundError
	var forbidden *registrystore.ForbiddenError
	switch {
	case errors.As(err, &notFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": err.Error()})
	case errors.As(err, &forbidden):
		c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

func queryPtr(c *gin.Context, key string) *string {
	v := c.Query(key)
	if v == "" {
		return nil
	}
	return &v
}
