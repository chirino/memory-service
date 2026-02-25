package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MountRoutes mounts admin API routes.
func MountRoutes(r *gin.Engine, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, auth gin.HandlerFunc) {
	requireAuditor := security.RequireAuditorRole()
	requireAdmin := security.RequireAdminRole()

	g := r.Group("/v1/admin", auth, requireAuditor)

	// Conversations
	g.GET("/conversations", func(c *gin.Context) {
		adminListConversations(c, store)
	})
	g.GET("/conversations/:id", func(c *gin.Context) {
		adminGetConversation(c, store)
	})
	g.DELETE("/conversations/:id", requireAdmin, func(c *gin.Context) {
		adminDeleteConversation(c, store)
	})
	g.POST("/conversations/:id/restore", requireAdmin, func(c *gin.Context) {
		adminRestoreConversation(c, store)
	})
	g.GET("/conversations/:id/entries", func(c *gin.Context) {
		adminGetEntries(c, store)
	})
	g.GET("/conversations/:id/memberships", func(c *gin.Context) {
		adminGetMemberships(c, store)
	})
	g.GET("/conversations/:id/forks", func(c *gin.Context) {
		adminListForks(c, store)
	})

	// Search
	g.POST("/conversations/search", func(c *gin.Context) {
		adminSearchConversations(c, store)
	})

	// Attachments
	g.GET("/attachments", func(c *gin.Context) {
		adminListAttachments(c, store)
	})
	g.GET("/attachments/:id", func(c *gin.Context) {
		adminGetAttachment(c, store)
	})
	g.DELETE("/attachments/:id", requireAdmin, func(c *gin.Context) {
		adminDeleteAttachment(c, store, attachStore)
	})
	g.GET("/attachments/:id/content", func(c *gin.Context) {
		adminGetAttachmentContent(c, store, attachStore, cfg)
	})
	g.GET("/attachments/:id/download-url", func(c *gin.Context) {
		adminGetAttachmentDownloadURL(c, store, attachStore, cfg)
	})

	// Eviction
	g.POST("/evict", requireAdmin, func(c *gin.Context) {
		adminEvict(c, store)
	})

	// Stats (Prometheus-backed parity with Java admin stats behavior)
	stats := newPrometheusStatsHandler(cfg)
	g.GET("/stats/request-rate", stats.rangeHandler(requestRateQuery, "request_rate", "requests/sec"))
	g.GET("/stats/error-rate", stats.rangeHandler(errorRateQuery, "error_rate", "percent"))
	g.GET("/stats/latency-p95", stats.rangeHandler(latencyP95Query, "latency_p95", "seconds"))
	g.GET("/stats/cache-hit-rate", stats.rangeHandler(cacheHitRateQuery, "cache_hit_rate", "percent"))
	g.GET("/stats/db-pool-utilization", stats.rangeHandler(dbPoolUtilizationQuery, "db_pool_utilization", "percent"))
	g.GET("/stats/store-latency-p95", stats.multiSeriesHandler(storeLatencyP95Query, "store_latency_p95", "seconds", "operation"))
	g.GET("/stats/store-throughput", stats.multiSeriesHandler(storeThroughputQuery, "store_throughput", "operations/sec", "operation"))
}

func adminListConversations(c *gin.Context, store registrystore.MemoryStore) {
	query := registrystore.AdminConversationQuery{
		Mode:           model.ConversationListMode(c.DefaultQuery("mode", "latest-fork")),
		IncludeDeleted: c.Query("includeDeleted") == "true",
		OnlyDeleted:    c.Query("onlyDeleted") == "true",
		Limit:          queryInt(c, "limit", 20),
		AfterCursor:    queryPtr(c, "afterCursor"),
	}
	if uid := c.Query("userId"); uid != "" {
		query.UserID = &uid
	}
	if da := c.Query("deletedAfter"); da != "" {
		if t, err := time.Parse(time.RFC3339, da); err == nil {
			query.DeletedAfter = &t
		}
	}
	if db := c.Query("deletedBefore"); db != "" {
		if t, err := time.Parse(time.RFC3339, db); err == nil {
			query.DeletedBefore = &t
		}
	}

	summaries, cursor, err := store.AdminListConversations(c.Request.Context(), query)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": summaries, "afterCursor": cursor})
}

func adminGetConversation(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	conv, err := store.AdminGetConversation(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, conv)
}

func adminDeleteConversation(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	if err := store.AdminDeleteConversation(c.Request.Context(), id); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func adminRestoreConversation(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	if err := store.AdminRestoreConversation(c.Request.Context(), id); err != nil {
		handleError(c, err)
		return
	}
	conv, err := store.AdminGetConversation(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, conv)
}

func adminGetEntries(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	forks := strings.ToLower(strings.TrimSpace(c.DefaultQuery("forks", "none")))
	switch forks {
	case "none", "all":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid forks value"})
		return
	}

	query := registrystore.AdminMessageQuery{
		Limit:       queryInt(c, "limit", 20),
		AfterCursor: queryPtr(c, "afterCursor"),
		AllForks:    forks == "all",
	}
	if ch := c.Query("channel"); ch != "" {
		v := model.Channel(ch)
		query.Channel = &v
	}

	result, err := store.AdminGetEntries(c.Request.Context(), id, query)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result.Data, "afterCursor": result.AfterCursor})
}

func adminGetMemberships(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	memberships, cursor, err := store.AdminListMemberships(c.Request.Context(), id, afterCursor, limit)
	if err != nil {
		handleError(c, err)
		return
	}
	// Wrap memberships to include conversationId (parity with Java AdminResource)
	type membershipResponse struct {
		ConversationID uuid.UUID         `json:"conversationId"`
		UserID         string            `json:"userId"`
		AccessLevel    model.AccessLevel `json:"accessLevel"`
		CreatedAt      time.Time         `json:"createdAt"`
	}
	wrapped := make([]membershipResponse, len(memberships))
	for i, m := range memberships {
		wrapped[i] = membershipResponse{
			ConversationID: id,
			UserID:         m.UserID,
			AccessLevel:    m.AccessLevel,
			CreatedAt:      m.CreatedAt,
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": wrapped, "afterCursor": cursor})
}

func adminListForks(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	forks, cursor, err := store.AdminListForks(c.Request.Context(), id, afterCursor, limit)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": forks, "afterCursor": cursor})
}

func adminSearchConversations(c *gin.Context, store registrystore.MemoryStore) {
	var req struct {
		Query        string  `json:"query"        binding:"required"`
		Limit        int     `json:"limit"`
		UserID       *string `json:"userId"`
		IncludeEntry *bool   `json:"includeEntry"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}
	includeEntry := true
	if req.IncludeEntry != nil {
		includeEntry = *req.IncludeEntry
	}

	results, err := store.AdminSearchEntries(c.Request.Context(), registrystore.AdminSearchQuery{
		Query:        req.Query,
		UserID:       req.UserID,
		Limit:        req.Limit,
		IncludeEntry: includeEntry,
	})
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": results.Data})
}

func adminListAttachments(c *gin.Context, store registrystore.MemoryStore) {
	query := registrystore.AdminAttachmentQuery{
		Status:      c.DefaultQuery("status", "all"),
		AfterCursor: queryPtr(c, "afterCursor"),
		Limit:       queryInt(c, "limit", 50),
	}
	if uid := strings.TrimSpace(c.Query("userId")); uid != "" {
		query.UserID = &uid
	}
	if entryID := strings.TrimSpace(c.Query("entryId")); entryID != "" {
		id, err := uuid.Parse(entryID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid entryId"})
			return
		}
		query.EntryID = &id
	}

	attachments, cursor, err := store.AdminListAttachments(c.Request.Context(), query)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": attachments, "afterCursor": cursor})
}

func adminGetAttachment(c *gin.Context, store registrystore.MemoryStore) {
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}
	attachment, err := store.AdminGetAttachment(c.Request.Context(), attachmentID)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, attachment)
}

func adminDeleteAttachment(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore) {
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	attachment, err := store.AdminGetAttachment(c.Request.Context(), attachmentID)
	if err != nil {
		handleError(c, err)
		return
	}

	if err := store.AdminDeleteAttachment(c.Request.Context(), attachmentID); err != nil {
		handleError(c, err)
		return
	}

	if attachStore != nil && attachment.StorageKey != nil && attachment.RefCount <= 1 {
		_ = attachStore.Delete(c.Request.Context(), *attachment.StorageKey)
	}
	c.Status(http.StatusNoContent)
}

func adminGetAttachmentContent(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	attachment, err := store.AdminGetAttachment(c.Request.Context(), attachmentID)
	if err != nil {
		handleError(c, err)
		return
	}
	if attachStore == nil || attachment.StorageKey == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment content not available"})
		return
	}

	if cfg != nil && cfg.S3DirectDownload {
		if signed, err := attachStore.GetSignedURL(c.Request.Context(), *attachment.StorageKey, cfg.AttachmentDownloadURLExpiresIn); err == nil {
			c.Redirect(http.StatusFound, signed.String())
			return
		}
	}

	reader, err := attachStore.Retrieve(c.Request.Context(), *attachment.StorageKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve attachment content"})
		return
	}
	defer reader.Close()

	if attachment.SHA256 != nil && *attachment.SHA256 != "" {
		etag := fmt.Sprintf("\"%s\"", *attachment.SHA256)
		c.Header("ETag", etag)
		if c.GetHeader("If-None-Match") == etag {
			c.Header("Cache-Control", "private, max-age=300, immutable")
			c.Status(http.StatusNotModified)
			return
		}
	}
	c.Header("Cache-Control", "private, max-age=300, immutable")
	c.Header("Content-Type", attachment.ContentType)
	if attachment.Filename != nil && *attachment.Filename != "" {
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", *attachment.Filename))
	}
	if attachment.Size != nil {
		c.Header("Content-Length", strconv.FormatInt(*attachment.Size, 10))
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, reader)
}

func adminGetAttachmentDownloadURL(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	attachment, err := store.AdminGetAttachment(c.Request.Context(), attachmentID)
	if err != nil {
		handleError(c, err)
		return
	}
	if attachStore == nil || attachment.StorageKey == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment content not available"})
		return
	}

	expires := 15 * time.Minute
	if cfg != nil && cfg.AttachmentDownloadURLExpiresIn > 0 {
		expires = cfg.AttachmentDownloadURLExpiresIn
	}

	if cfg == nil || cfg.S3DirectDownload {
		if signed, err := attachStore.GetSignedURL(c.Request.Context(), *attachment.StorageKey, expires); err == nil {
			c.JSON(http.StatusOK, gin.H{"url": signed.String(), "expiresIn": int(expires.Seconds())})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"url":       fmt.Sprintf("/v1/admin/attachments/%s/content", attachment.ID),
		"expiresIn": int(expires.Seconds()),
	})
}

func adminEvict(c *gin.Context, store registrystore.MemoryStore) {
	var req struct {
		RetentionPeriod string   `json:"retentionPeriod" binding:"required"`
		ResourceTypes   []string `json:"resourceTypes"   binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.ResourceTypes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "resourceTypes is required"})
		return
	}
	for _, resourceType := range req.ResourceTypes {
		if strings.TrimSpace(strings.ToLower(resourceType)) != "conversations" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported resource type"})
			return
		}
	}

	// Parse ISO 8601 duration (simplified: support "P30D", "PT1H", etc.)
	duration, err := parseDuration(req.RetentionPeriod)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid retention period: %v", err)})
		return
	}

	cutoff := time.Now().Add(-duration)

	// Check if SSE is requested
	asyncRequested := strings.EqualFold(c.Query("async"), "true")
	acceptsSSE := strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/event-stream")
	if asyncRequested || acceptsSSE {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Flush()

		total, err := store.CountEvictableGroups(c.Request.Context(), cutoff)
		if err != nil {
			fmt.Fprintf(c.Writer, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error())
			c.Writer.Flush()
			return
		}

		if total == 0 {
			fmt.Fprintf(c.Writer, "event: progress\ndata: {\"progress\":100}\n\n")
			c.Writer.Flush()
			return
		}

		batchSize := 1000
		evicted := int64(0)
		for {
			ids, err := store.FindEvictableGroupIDs(c.Request.Context(), cutoff, batchSize)
			if err != nil {
				fmt.Fprintf(c.Writer, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error())
				c.Writer.Flush()
				return
			}
			if len(ids) == 0 {
				break
			}
			createVectorDeleteTasks(c.Request.Context(), store, ids)
			if err := store.HardDeleteConversationGroups(c.Request.Context(), ids); err != nil {
				log.Error("Eviction batch failed", "err", err)
			}
			evicted += int64(len(ids))
			progress := int(float64(evicted) / float64(total) * 100)
			if progress > 100 {
				progress = 100
			}
			fmt.Fprintf(c.Writer, "event: progress\ndata: {\"progress\":%d}\n\n", progress)
			c.Writer.Flush()
		}

		fmt.Fprintf(c.Writer, "event: progress\ndata: {\"progress\":100}\n\n")
		c.Writer.Flush()
		return
	}

	// Non-streaming mode
	batchSize := 1000
	for {
		ids, err := store.FindEvictableGroupIDs(c.Request.Context(), cutoff, batchSize)
		if err != nil {
			handleError(c, err)
			return
		}
		if len(ids) == 0 {
			break
		}
		// Create vector_store_delete tasks for each group before hard-deleting
		createVectorDeleteTasks(c.Request.Context(), store, ids)
		if err := store.HardDeleteConversationGroups(c.Request.Context(), ids); err != nil {
			handleError(c, err)
			return
		}
	}
	c.Status(http.StatusNoContent)
}

func createVectorDeleteTasks(ctx context.Context, store registrystore.MemoryStore, groupIDs []uuid.UUID) {
	for _, id := range groupIDs {
		body := map[string]interface{}{
			"conversationGroupId": id.String(),
		}
		if err := store.CreateTask(ctx, "vector_store_delete", body); err != nil {
			log.Error("Failed to create vector_store_delete task", "groupId", id, "err", err)
		}
	}
}

func parseDuration(iso string) (time.Duration, error) {
	// Simple ISO 8601 duration parser for common formats
	// P30D = 30 days, PT1H = 1 hour, PT30M = 30 minutes
	if len(iso) < 2 || iso[0] != 'P' {
		return 0, fmt.Errorf("not an ISO 8601 duration: %s", iso)
	}
	s := iso[1:]
	inTime := false
	var d time.Duration
	numBuf := ""
	for _, ch := range s {
		switch {
		case ch == 'T':
			inTime = true
		case ch >= '0' && ch <= '9':
			numBuf += string(ch)
		default:
			n, err := strconv.Atoi(numBuf)
			if err != nil {
				return 0, fmt.Errorf("invalid number in duration: %s", numBuf)
			}
			numBuf = ""
			switch {
			case ch == 'D' && !inTime:
				d += time.Duration(n) * 24 * time.Hour
			case ch == 'Y' && !inTime:
				d += time.Duration(n) * 365 * 24 * time.Hour
			case ch == 'H' && inTime:
				d += time.Duration(n) * time.Hour
			case ch == 'M' && inTime:
				d += time.Duration(n) * time.Minute
			case ch == 'S' && inTime:
				d += time.Duration(n) * time.Second
			default:
				return 0, fmt.Errorf("unsupported duration unit: %c", ch)
			}
		}
	}
	return d, nil
}

func handleError(c *gin.Context, err error) {
	var notFound *registrystore.NotFoundError
	var forbidden *registrystore.ForbiddenError
	var conflict *registrystore.ConflictError
	var validation *registrystore.ValidationError
	switch {
	case errors.As(err, &notFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.As(err, &forbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.As(err, &conflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		log.Error("Admin API error", "err", err)
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

func queryInt(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}
