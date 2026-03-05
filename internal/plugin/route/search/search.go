package search

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	searchTypeAuto     = "auto"
	searchTypeSemantic = "semantic"
	searchTypeFulltext = "fulltext"
)

var errInvalidAfterCursor = errors.New("invalid afterCursor")

type cursorToken struct {
	Types   []string          `json:"types,omitempty"`
	Cursors map[string]string `json:"cursors,omitempty"`
}

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

// HandleSearchConversations exposes conversation search for wrapper-native adapters.
func HandleSearchConversations(c *gin.Context, store registrystore.MemoryStore, cfg *config.Config, embedder registryembed.Embedder, vectorStore registryvector.VectorStore) {
	searchConversations(c, store, cfg, embedder, vectorStore)
}

// HandleIndexConversations exposes conversation indexing for wrapper-native adapters.
func HandleIndexConversations(c *gin.Context, store registrystore.MemoryStore) {
	indexConversations(c, store)
}

// HandleListUnindexed exposes unindexed list for wrapper-native adapters.
func HandleListUnindexed(c *gin.Context, store registrystore.MemoryStore) {
	listUnindexed(c, store)
}

func searchConversations(c *gin.Context, store registrystore.MemoryStore, cfg *config.Config, embedder registryembed.Embedder, vectorStore registryvector.VectorStore) {
	userID := security.GetUserID(c)

	var req struct {
		Query               string  `json:"query"               binding:"required"`
		SearchType          any     `json:"searchType"`
		AfterCursor         *string `json:"afterCursor"`
		Limit               *int    `json:"limit"`
		IncludeEntry        *bool   `json:"includeEntry"`
		GroupByConversation *bool   `json:"groupByConversation"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
		return
	}

	searchTypes, err := normalizeSearchTypes(req.SearchType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
		return
	}

	limit := 20
	if req.Limit != nil {
		if *req.Limit <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "limit must be greater than 0"})
			return
		}
		if *req.Limit > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "limit must be less than or equal to 200"})
			return
		}
		limit = *req.Limit
	}
	includeEntry := true
	if req.IncludeEntry != nil {
		includeEntry = *req.IncludeEntry
	}
	groupByConversation := true
	if req.GroupByConversation != nil {
		groupByConversation = *req.GroupByConversation
	}

	semanticAvailable := cfg != nil && cfg.SearchSemanticEnabled && embedder != nil && vectorStore != nil && vectorStore.IsEnabled()
	fulltextAvailable := cfg == nil || cfg.SearchFulltextEnabled
	availableTypes := availableSearchTypes(semanticAvailable, fulltextAvailable)

	cursorMap, cursorTypes, err := decodeAfterCursor(req.AfterCursor, searchTypes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
		return
	}

	if len(searchTypes) == 1 && searchTypes[0] == searchTypeAuto {
		executeAutoSearch(c, store, embedder, vectorStore, userID, req.Query, includeEntry, groupByConversation, limit, semanticAvailable, fulltextAvailable, availableTypes, cursorMap, cursorTypes)
		return
	}

	missing := missingSearchTypes(searchTypes, semanticAvailable, fulltextAvailable)
	if len(missing) > 0 {
		searchTypeUnavailable(c, availableTypes)
		return
	}

	combined := make([]registrystore.SearchResult, 0, len(searchTypes)*limit)
	nextCursors := make(map[string]string, len(searchTypes))
	for _, t := range searchTypes {
		subCursor := cursorPtr(cursorMap[t])
		results, err := executeSearchType(c.Request.Context(), store, embedder, vectorStore, userID, req.Query, t, subCursor, limit, includeEntry, groupByConversation)
		if err != nil {
			if errors.Is(err, errInvalidAfterCursor) {
				c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
				return
			}
			handleError(c, err)
			return
		}
		combined = append(combined, results.Data...)
		if results.AfterCursor != nil {
			nextCursors[t] = *results.AfterCursor
		}
	}

	afterCursor := encodeAfterCursor(searchTypes, nextCursors)
	c.JSON(http.StatusOK, gin.H{"data": combined, "afterCursor": afterCursor})
}

func executeAutoSearch(
	c *gin.Context,
	store registrystore.MemoryStore,
	embedder registryembed.Embedder,
	vectorStore registryvector.VectorStore,
	userID, query string,
	includeEntry bool,
	groupByConversation bool,
	limit int,
	semanticAvailable bool,
	fulltextAvailable bool,
	availableTypes []string,
	cursorMap map[string]string,
	cursorTypes []string,
) {
	if len(cursorTypes) == 1 && cursorTypes[0] != searchTypeAuto {
		t := cursorTypes[0]
		if len(missingSearchTypes([]string{t}, semanticAvailable, fulltextAvailable)) > 0 {
			searchTypeUnavailable(c, availableTypes)
			return
		}
		results, err := executeSearchType(c.Request.Context(), store, embedder, vectorStore, userID, query, t, cursorPtr(cursorMap[t]), limit, includeEntry, groupByConversation)
		if err != nil {
			if errors.Is(err, errInvalidAfterCursor) {
				c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
				return
			}
			handleError(c, err)
			return
		}
		nextCursors := map[string]string{}
		if results.AfterCursor != nil {
			nextCursors[t] = *results.AfterCursor
		}
		c.JSON(http.StatusOK, gin.H{"data": results.Data, "afterCursor": encodeAfterCursor([]string{t}, nextCursors)})
		return
	}

	// For legacy/plain cursors or typed cursors without explicit `types`, continue
	// the backend that supplied the cursor instead of re-running auto selection.
	if cursorMap[searchTypeSemantic] != "" || cursorMap[searchTypeFulltext] != "" {
		preferred := searchTypeSemantic
		if cursorMap[searchTypeSemantic] == "" {
			preferred = searchTypeFulltext
		}
		if len(missingSearchTypes([]string{preferred}, semanticAvailable, fulltextAvailable)) > 0 {
			searchTypeUnavailable(c, availableTypes)
			return
		}
		results, err := executeSearchType(c.Request.Context(), store, embedder, vectorStore, userID, query, preferred, cursorPtr(cursorMap[preferred]), limit, includeEntry, groupByConversation)
		if err != nil {
			if errors.Is(err, errInvalidAfterCursor) {
				c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
				return
			}
			handleError(c, err)
			return
		}
		nextCursors := map[string]string{}
		if results.AfterCursor != nil {
			nextCursors[preferred] = *results.AfterCursor
		}
		c.JSON(http.StatusOK, gin.H{"data": results.Data, "afterCursor": encodeAfterCursor([]string{preferred}, nextCursors)})
		return
	}

	if !semanticAvailable && !fulltextAvailable {
		searchTypeUnavailable(c, availableTypes)
		return
	}

	if semanticAvailable {
		results, err := executeSearchType(c.Request.Context(), store, embedder, vectorStore, userID, query, searchTypeSemantic, cursorPtr(cursorMap[searchTypeSemantic]), limit, includeEntry, groupByConversation)
		if err != nil {
			log.Warn("Semantic search failed, falling back to fulltext", "err", err)
		} else if len(results.Data) > 0 || cursorMap[searchTypeSemantic] != "" {
			nextCursors := map[string]string{}
			if results.AfterCursor != nil {
				nextCursors[searchTypeSemantic] = *results.AfterCursor
			}
			c.JSON(http.StatusOK, gin.H{"data": results.Data, "afterCursor": encodeAfterCursor([]string{searchTypeSemantic}, nextCursors)})
			return
		}
	}

	if !fulltextAvailable {
		searchTypeUnavailable(c, availableTypes)
		return
	}

	results, err := executeSearchType(c.Request.Context(), store, embedder, vectorStore, userID, query, searchTypeFulltext, cursorPtr(cursorMap[searchTypeFulltext]), limit, includeEntry, groupByConversation)
	if err != nil {
		if errors.Is(err, errInvalidAfterCursor) {
			c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
			return
		}
		handleError(c, err)
		return
	}
	nextCursors := map[string]string{}
	if results.AfterCursor != nil {
		nextCursors[searchTypeFulltext] = *results.AfterCursor
	}
	c.JSON(http.StatusOK, gin.H{"data": results.Data, "afterCursor": encodeAfterCursor([]string{searchTypeFulltext}, nextCursors)})
}

func executeSearchType(
	ctx context.Context,
	store registrystore.MemoryStore,
	embedder registryembed.Embedder,
	vectorStore registryvector.VectorStore,
	userID, query, searchType string,
	afterCursor *string,
	limit int,
	includeEntry bool,
	groupByConversation bool,
) (*registrystore.SearchResults, error) {
	switch searchType {
	case searchTypeSemantic:
		return doSemanticSearch(ctx, store, embedder, vectorStore, userID, query, afterCursor, limit, includeEntry, groupByConversation)
	case searchTypeFulltext:
		if err := validateEntryCursor(afterCursor); err != nil {
			return nil, err
		}
		return store.SearchEntries(ctx, userID, query, afterCursor, limit, includeEntry, groupByConversation)
	default:
		return nil, fmt.Errorf("unsupported searchType: %s", searchType)
	}
}

func availableSearchTypes(semanticAvailable, fulltextAvailable bool) []string {
	available := make([]string, 0, 2)
	if semanticAvailable {
		available = append(available, searchTypeSemantic)
	}
	if fulltextAvailable {
		available = append(available, searchTypeFulltext)
	}
	return available
}

func missingSearchTypes(searchTypes []string, semanticAvailable, fulltextAvailable bool) []string {
	missing := make([]string, 0, len(searchTypes))
	for _, t := range searchTypes {
		switch t {
		case searchTypeSemantic:
			if !semanticAvailable {
				missing = append(missing, t)
			}
		case searchTypeFulltext:
			if !fulltextAvailable {
				missing = append(missing, t)
			}
		}
	}
	return missing
}

func searchTypeUnavailable(c *gin.Context, availableTypes []string) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error":          "search_type_unavailable",
		"message":        "One or more requested search types are not available on this server.",
		"availableTypes": availableTypes,
	})
}

func normalizeSearchTypes(raw any) ([]string, error) {
	if raw == nil {
		return []string{searchTypeAuto}, nil
	}

	valid := map[string]struct{}{
		searchTypeAuto:     {},
		searchTypeSemantic: {},
		searchTypeFulltext: {},
	}

	add := func(out []string, value string) ([]string, error) {
		v := strings.ToLower(strings.TrimSpace(value))
		if v == "" {
			return nil, fmt.Errorf("searchType cannot be empty")
		}
		if _, ok := valid[v]; !ok {
			return nil, fmt.Errorf("invalid searchType %q", value)
		}
		for _, e := range out {
			if e == v {
				return out, nil
			}
		}
		return append(out, v), nil
	}

	result := []string{}
	switch v := raw.(type) {
	case string:
		out, err := add(result, v)
		if err != nil {
			return nil, err
		}
		result = out
	case []any:
		if len(v) == 0 {
			return nil, fmt.Errorf("searchType array must contain at least one value")
		}
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("searchType array values must be strings")
			}
			out, err := add(result, s)
			if err != nil {
				return nil, err
			}
			result = out
		}
	default:
		return nil, fmt.Errorf("searchType must be a string or array of strings")
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("searchType cannot be empty")
	}
	if len(result) > 1 {
		for _, t := range result {
			if t == searchTypeAuto {
				return nil, fmt.Errorf("searchType 'auto' cannot be combined with other search types")
			}
		}
	}
	return result, nil
}

func decodeAfterCursor(afterCursor *string, requestedTypes []string) (map[string]string, []string, error) {
	cursorMap := map[string]string{}
	if afterCursor == nil {
		return cursorMap, nil, nil
	}

	raw := strings.TrimSpace(*afterCursor)
	if raw == "" {
		return cursorMap, nil, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err == nil {
		var token cursorToken
		if uErr := json.Unmarshal(decoded, &token); uErr == nil && len(token.Cursors) > 0 {
			for k, v := range token.Cursors {
				if strings.TrimSpace(v) != "" {
					cursorMap[k] = v
				}
			}
			return cursorMap, token.Types, nil
		}
	}

	if len(requestedTypes) == 1 {
		t := requestedTypes[0]
		if t == searchTypeAuto {
			// Legacy behavior prior to typed/opaque cursors was fulltext cursor only.
			cursorMap[searchTypeFulltext] = raw
			return cursorMap, nil, nil
		}
		cursorMap[t] = raw
		return cursorMap, nil, nil
	}

	return nil, nil, fmt.Errorf("%w: malformed multi-search cursor", errInvalidAfterCursor)
}

func encodeAfterCursor(searchTypes []string, cursors map[string]string) *string {
	if len(cursors) == 0 {
		return nil
	}
	token := cursorToken{
		Types:   searchTypes,
		Cursors: cursors,
	}
	data, err := json.Marshal(token)
	if err != nil {
		return nil
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return &encoded
}

func cursorPtr(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

func validateEntryCursor(afterCursor *string) error {
	if afterCursor == nil {
		return nil
	}
	if _, err := uuid.Parse(*afterCursor); err != nil {
		return fmt.Errorf("%w: must be a UUID", errInvalidAfterCursor)
	}
	return nil
}

func doSemanticSearch(ctx context.Context, store registrystore.MemoryStore, embedder registryembed.Embedder, vectorStore registryvector.VectorStore, userID, query string, afterCursor *string, limit int, includeEntry bool, groupByConversation bool) (*registrystore.SearchResults, error) {
	if err := validateEntryCursor(afterCursor); err != nil {
		return nil, err
	}
	groupIDs, err := store.ListConversationGroupIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list group IDs: %w", err)
	}
	if len(groupIDs) == 0 {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}

	embeddings, err := embedder.EmbedTexts(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	searchLimit := semanticCandidateLimit(limit, groupByConversation, afterCursor)
	vectorResults, err := vectorStore.Search(ctx, embeddings[0], groupIDs, searchLimit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	if len(vectorResults) == 0 {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
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
	sortSearchResults(details)
	if groupByConversation {
		details = groupResultsByConversation(details)
	}
	page, nextCursor, err := paginateSearchResults(details, afterCursor, limit)
	if err != nil {
		return nil, err
	}
	return &registrystore.SearchResults{Data: page, AfterCursor: nextCursor}, nil
}

func semanticCandidateLimit(limit int, groupByConversation bool, afterCursor *string) int {
	out := limit + 1
	if groupByConversation {
		out = limit*3 + 1
	}
	if afterCursor != nil && out < 1000 {
		out = 1000
	}
	if out < limit+1 {
		out = limit + 1
	}
	if out > 5000 {
		out = 5000
	}
	return out
}

func sortSearchResults(results []registrystore.SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].EntryID.String() < results[j].EntryID.String()
		}
		return results[i].Score > results[j].Score
	})
}

func groupResultsByConversation(results []registrystore.SearchResult) []registrystore.SearchResult {
	best := make([]registrystore.SearchResult, 0, len(results))
	seen := make(map[uuid.UUID]struct{}, len(results))
	for _, r := range results {
		if _, ok := seen[r.ConversationID]; ok {
			continue
		}
		seen[r.ConversationID] = struct{}{}
		best = append(best, r)
	}
	return best
}

func paginateSearchResults(results []registrystore.SearchResult, afterCursor *string, limit int) ([]registrystore.SearchResult, *string, error) {
	start := 0
	if afterCursor != nil {
		cursorID, err := uuid.Parse(*afterCursor)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: must be a UUID", errInvalidAfterCursor)
		}
		start = len(results)
		for i := range results {
			if results[i].EntryID == cursorID {
				start = i + 1
				break
			}
		}
	}
	if start >= len(results) {
		return []registrystore.SearchResult{}, nil, nil
	}

	end := start + limit
	if end > len(results) {
		end = len(results)
	}
	page := results[start:end]

	var nextCursor *string
	if end < len(results) && len(page) > 0 {
		v := page[len(page)-1].EntryID.String()
		nextCursor = &v
	}
	return page, nextCursor, nil
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
