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
	"github.com/chirino/memory-service/internal/plugin/route/routetx"
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
	g.GET("/conversations/:id/children", func(c *gin.Context) {
		adminListChildConversations(c, store)
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

func runMiddlewares(c *gin.Context, middlewares ...gin.HandlerFunc) bool {
	for _, middleware := range middlewares {
		middleware(c)
		if c.IsAborted() {
			return false
		}
	}
	return true
}

// HandleAdminListConversations exposes admin list conversations for wrapper-native adapters.
func HandleAdminListConversations(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminListConversations(c, store)
}

// HandleAdminGetConversation exposes admin get conversation for wrapper-native adapters.
func HandleAdminGetConversation(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminGetConversation(c, store)
}

// HandleAdminDeleteConversation exposes admin delete conversation for wrapper-native adapters.
func HandleAdminDeleteConversation(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole(), security.RequireAdminRole()) {
		return
	}
	adminDeleteConversation(c, store)
}

// HandleAdminRestoreConversation exposes admin restore conversation for wrapper-native adapters.
func HandleAdminRestoreConversation(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole(), security.RequireAdminRole()) {
		return
	}
	adminRestoreConversation(c, store)
}

// HandleAdminGetEntries exposes admin get entries for wrapper-native adapters.
func HandleAdminGetEntries(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminGetEntries(c, store)
}

// HandleAdminGetMemberships exposes admin get memberships for wrapper-native adapters.
func HandleAdminGetMemberships(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminGetMemberships(c, store)
}

// HandleAdminListForks exposes admin list forks for wrapper-native adapters.
func HandleAdminListForks(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminListForks(c, store)
}

// HandleAdminListChildConversations exposes admin list children for wrapper-native adapters.
func HandleAdminListChildConversations(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminListChildConversations(c, store)
}

// HandleAdminSearchConversations exposes admin search for wrapper-native adapters.
func HandleAdminSearchConversations(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminSearchConversations(c, store)
}

// HandleAdminListAttachments exposes admin list attachments for wrapper-native adapters.
func HandleAdminListAttachments(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminListAttachments(c, store)
}

// HandleAdminGetAttachment exposes admin get attachment for wrapper-native adapters.
func HandleAdminGetAttachment(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminGetAttachment(c, store)
}

// HandleAdminDeleteAttachment exposes admin delete attachment for wrapper-native adapters.
func HandleAdminDeleteAttachment(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore) {
	if !runMiddlewares(c, security.RequireAuditorRole(), security.RequireAdminRole()) {
		return
	}
	adminDeleteAttachment(c, store, attachStore)
}

// HandleAdminGetAttachmentContent exposes admin attachment content for wrapper-native adapters.
func HandleAdminGetAttachmentContent(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminGetAttachmentContent(c, store, attachStore, cfg)
}

// HandleAdminGetAttachmentDownloadURL exposes admin attachment download-url for wrapper-native adapters.
func HandleAdminGetAttachmentDownloadURL(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	adminGetAttachmentDownloadURL(c, store, attachStore, cfg)
}

// HandleAdminEvict exposes admin eviction for wrapper-native adapters.
func HandleAdminEvict(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAuditorRole(), security.RequireAdminRole()) {
		return
	}
	adminEvict(c, store)
}

// HandleAdminStatsRequestRate exposes admin request-rate stats for wrapper-native adapters.
func HandleAdminStatsRequestRate(c *gin.Context, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	stats := newPrometheusStatsHandler(cfg)
	stats.rangeHandler(requestRateQuery, "request_rate", "requests/sec")(c)
}

// HandleAdminStatsErrorRate exposes admin error-rate stats for wrapper-native adapters.
func HandleAdminStatsErrorRate(c *gin.Context, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	stats := newPrometheusStatsHandler(cfg)
	stats.rangeHandler(errorRateQuery, "error_rate", "percent")(c)
}

// HandleAdminStatsLatencyP95 exposes admin latency-p95 stats for wrapper-native adapters.
func HandleAdminStatsLatencyP95(c *gin.Context, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	stats := newPrometheusStatsHandler(cfg)
	stats.rangeHandler(latencyP95Query, "latency_p95", "seconds")(c)
}

// HandleAdminStatsCacheHitRate exposes admin cache-hit-rate stats for wrapper-native adapters.
func HandleAdminStatsCacheHitRate(c *gin.Context, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	stats := newPrometheusStatsHandler(cfg)
	stats.rangeHandler(cacheHitRateQuery, "cache_hit_rate", "percent")(c)
}

// HandleAdminStatsDbPoolUtilization exposes admin db-pool-utilization stats for wrapper-native adapters.
func HandleAdminStatsDbPoolUtilization(c *gin.Context, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	stats := newPrometheusStatsHandler(cfg)
	stats.rangeHandler(dbPoolUtilizationQuery, "db_pool_utilization", "percent")(c)
}

// HandleAdminStatsStoreLatencyP95 exposes admin store-latency-p95 stats for wrapper-native adapters.
func HandleAdminStatsStoreLatencyP95(c *gin.Context, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	stats := newPrometheusStatsHandler(cfg)
	stats.multiSeriesHandler(storeLatencyP95Query, "store_latency_p95", "seconds", "operation")(c)
}

// HandleAdminStatsStoreThroughput exposes admin store-throughput stats for wrapper-native adapters.
func HandleAdminStatsStoreThroughput(c *gin.Context, cfg *config.Config) {
	if !runMiddlewares(c, security.RequireAuditorRole()) {
		return
	}
	stats := newPrometheusStatsHandler(cfg)
	stats.multiSeriesHandler(storeThroughputQuery, "store_throughput", "operations/sec", "operation")(c)
}

func adminListConversations(c *gin.Context, store registrystore.MemoryStore) {
	query := registrystore.AdminConversationQuery{
		Mode:           model.ConversationListMode(c.DefaultQuery("mode", "latest-fork")),
		Ancestry:       model.ConversationAncestryFilter(c.DefaultQuery("ancestry", "roots")),
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

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		summaries, cursor, err := store.AdminListConversations(ctx, query)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": summaries, "afterCursor": cursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminGetConversation(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		conv, err := store.AdminGetConversation(ctx, id)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, conv)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminDeleteConversation(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		if err := store.AdminDeleteConversation(ctx, id); err != nil {
			return err
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminRestoreConversation(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		if err := store.AdminRestoreConversation(ctx, id); err != nil {
			return err
		}
		conv, err := store.AdminGetConversation(ctx, id)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, conv)
		return nil
	}); err != nil {
		handleError(c, err)
	}
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

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		result, err := store.AdminGetEntries(ctx, id, query)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": result.Data, "afterCursor": result.AfterCursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminGetMemberships(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		memberships, cursor, err := store.AdminListMemberships(ctx, id, afterCursor, limit)
		if err != nil {
			return err
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
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminListForks(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		forks, cursor, err := store.AdminListForks(ctx, id, afterCursor, limit)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": forks, "afterCursor": cursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminListChildConversations(c *gin.Context, store registrystore.MemoryStore) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		children, cursor, err := store.AdminListChildConversations(ctx, id, afterCursor, limit)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": toAdminChildConversationSummaries(children), "afterCursor": cursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

type adminChildConversationSummaryResponse struct {
	ID               uuid.UUID         `json:"id"`
	Title            string            `json:"title"`
	OwnerUserID      string            `json:"ownerUserId"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
	DeletedAt        *time.Time        `json:"deletedAt,omitempty"`
	AccessLevel      model.AccessLevel `json:"accessLevel"`
	StartedByEntryID *uuid.UUID        `json:"startedByEntryId,omitempty"`
}

func toAdminChildConversationSummaries(items []registrystore.ConversationSummary) []adminChildConversationSummaryResponse {
	result := make([]adminChildConversationSummaryResponse, 0, len(items))
	for _, item := range items {
		result = append(result, adminChildConversationSummaryResponse{
			ID:               item.ID,
			Title:            item.Title,
			OwnerUserID:      item.OwnerUserID,
			CreatedAt:        item.CreatedAt,
			UpdatedAt:        item.UpdatedAt,
			DeletedAt:        item.DeletedAt,
			AccessLevel:      item.AccessLevel,
			StartedByEntryID: item.StartedByEntryID,
		})
	}
	return result
}

func adminSearchConversations(c *gin.Context, store registrystore.MemoryStore) {
	var req struct {
		Query          string  `json:"query"          binding:"required"`
		Limit          int     `json:"limit"`
		AfterCursor    *string `json:"afterCursor"`
		UserID         *string `json:"userId"`
		IncludeDeleted *bool   `json:"includeDeleted"`
		IncludeEntry   *bool   `json:"includeEntry"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "limit must be less than or equal to 1000"})
		return
	}
	includeDeleted := false
	if req.IncludeDeleted != nil {
		includeDeleted = *req.IncludeDeleted
	}
	includeEntry := true
	if req.IncludeEntry != nil {
		includeEntry = *req.IncludeEntry
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		results, err := store.AdminSearchEntries(ctx, registrystore.AdminSearchQuery{
			Query:          req.Query,
			UserID:         req.UserID,
			Limit:          req.Limit,
			IncludeEntry:   includeEntry,
			IncludeDeleted: includeDeleted,
			AfterCursor:    req.AfterCursor,
		})
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": results.Data, "afterCursor": results.AfterCursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
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

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		attachments, cursor, err := store.AdminListAttachments(ctx, query)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": attachments, "afterCursor": cursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminGetAttachment(c *gin.Context, store registrystore.MemoryStore) {
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}
	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		attachment, err := store.AdminGetAttachment(ctx, attachmentID)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, attachment)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminDeleteAttachment(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore) {
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		attachment, err := store.AdminGetAttachment(ctx, attachmentID)
		if err != nil {
			return err
		}

		if err := store.AdminDeleteAttachment(ctx, attachmentID); err != nil {
			return err
		}

		if attachStore != nil && attachment.StorageKey != nil && attachment.RefCount <= 1 {
			_ = attachStore.Delete(ctx, *attachment.StorageKey)
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminGetAttachmentContent(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		attachment, err := store.AdminGetAttachment(ctx, attachmentID)
		if err != nil {
			return err
		}
		if attachStore == nil || attachment.StorageKey == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "attachment content not available"})
			return nil
		}

		if cfg != nil && cfg.S3DirectDownload {
			if signed, err := attachStore.GetSignedURL(ctx, *attachment.StorageKey, cfg.AttachmentDownloadURLExpiresIn); err == nil {
				c.Redirect(http.StatusFound, signed.String())
				return nil
			}
		}

		reader, err := attachStore.Retrieve(ctx, *attachment.StorageKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve attachment content"})
			return nil
		}
		defer reader.Close()

		if attachment.SHA256 != nil && *attachment.SHA256 != "" {
			etag := fmt.Sprintf("\"%s\"", *attachment.SHA256)
			c.Header("ETag", etag)
			if c.GetHeader("If-None-Match") == etag {
				c.Header("Cache-Control", "private, max-age=300, immutable")
				c.Status(http.StatusNotModified)
				return nil
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
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func adminGetAttachmentDownloadURL(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	attachmentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		attachment, err := store.AdminGetAttachment(ctx, attachmentID)
		if err != nil {
			return err
		}
		if attachStore == nil || attachment.StorageKey == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "attachment content not available"})
			return nil
		}

		expires := 15 * time.Minute
		if cfg != nil && cfg.AttachmentDownloadURLExpiresIn > 0 {
			expires = cfg.AttachmentDownloadURLExpiresIn
		}

		if cfg == nil || cfg.S3DirectDownload {
			if signed, err := attachStore.GetSignedURL(ctx, *attachment.StorageKey, expires); err == nil {
				c.JSON(http.StatusOK, gin.H{"url": signed.String(), "expiresIn": int(expires.Seconds())})
				return nil
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"url":       fmt.Sprintf("/v1/admin/attachments/%s/content", attachment.ID),
			"expiresIn": int(expires.Seconds()),
		})
		return nil
	}); err != nil {
		handleError(c, err)
	}
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
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		if asyncRequested || acceptsSSE {
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			c.Writer.Header().Set("Cache-Control", "no-cache")
			c.Writer.Header().Set("Connection", "keep-alive")
			c.Writer.Flush()

			total, err := store.CountEvictableGroups(ctx, cutoff)
			if err != nil {
				fmt.Fprintf(c.Writer, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error())
				c.Writer.Flush()
				return nil
			}

			if total == 0 {
				fmt.Fprintf(c.Writer, "event: progress\ndata: {\"progress\":100}\n\n")
				c.Writer.Flush()
				return nil
			}

			batchSize := 1000
			evicted := int64(0)
			for {
				ids, err := store.FindEvictableGroupIDs(ctx, cutoff, batchSize)
				if err != nil {
					fmt.Fprintf(c.Writer, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error())
					c.Writer.Flush()
					return nil
				}
				if len(ids) == 0 {
					break
				}
				createVectorDeleteTasks(ctx, store, ids)
				if err := store.HardDeleteConversationGroups(ctx, ids); err != nil {
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
			return nil
		}

		batchSize := 1000
		for {
			ids, err := store.FindEvictableGroupIDs(ctx, cutoff, batchSize)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			createVectorDeleteTasks(ctx, store, ids)
			if err := store.HardDeleteConversationGroups(ctx, ids); err != nil {
				return err
			}
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
	}
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
