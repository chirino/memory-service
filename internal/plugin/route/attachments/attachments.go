package attachments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/tempfiles"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MountRoutes mounts attachment routes.
func MountRoutes(r *gin.Engine, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, auth gin.HandlerFunc) {
	if attachStore == nil {
		return
	}

	signingKeys, err := cfg.AttachmentSigningKeys()
	if err != nil {
		log.Warn("Attachment signing keys unavailable; signed download URLs disabled", "err", err)
		signingKeys = nil
	}

	v1 := r.Group("/v1")
	v1.POST("/attachments", auth, func(c *gin.Context) {
		upload(c, store, attachStore, cfg)
	})
	v1.GET("/attachments/:attachmentId", auth, func(c *gin.Context) {
		getAttachment(c, store, attachStore, cfg)
	})
	v1.DELETE("/attachments/:attachmentId", auth, func(c *gin.Context) {
		deleteAttachment(c, store, attachStore)
	})
	v1.GET("/attachments/:attachmentId/download-url", auth, func(c *gin.Context) {
		var primaryKey []byte
		if len(signingKeys) > 0 {
			primaryKey = signingKeys[0]
		}
		downloadURL(c, store, attachStore, cfg, primaryKey)
	})
	if len(signingKeys) > 0 {
		v1.GET("/attachments/download/:token/:filename", func(c *gin.Context) {
			downloadByToken(c, store, attachStore, signingKeys)
		})
	}
}

func upload(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	userID := security.GetUserID(c)

	contentType := c.GetHeader("Content-Type")
	if strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		var req struct {
			SourceURL   string `json:"sourceUrl"   binding:"required"`
			ContentType string `json:"contentType"`
			Name        string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if _, err := url.ParseRequestURI(req.SourceURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sourceUrl"})
			return
		}
		fileContentType := strings.TrimSpace(req.ContentType)
		if fileContentType == "" {
			fileContentType = "application/octet-stream"
		}
		expiresAt := time.Now().Add(cfg.AttachmentDefaultExpiresIn)
		var filename *string
		if strings.TrimSpace(req.Name) != "" {
			name := req.Name
			filename = &name
		}
		sourceURL := req.SourceURL
		attachment, err := store.CreateAttachment(c.Request.Context(), userID, uuid.Nil, model.Attachment{
			Filename:    filename,
			ContentType: fileContentType,
			SourceURL:   &sourceURL,
			ExpiresAt:   &expiresAt,
			Status:      "downloading",
		})
		if err != nil {
			handleError(c, err)
			return
		}

		go completeSourceURLAttachment(store, attachStore, cfg, attachment.ID, userID, sourceURL, fileContentType)

		c.JSON(http.StatusCreated, toUploadResponse(attachment))
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	fileContentType := header.Header.Get("Content-Type")
	if fileContentType == "" {
		fileContentType = "application/octet-stream"
	}

	result, err := attachStore.Store(c.Request.Context(), file, cfg.AttachmentMaxSize, fileContentType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	expiresIn, err := parseExpiresIn(c.DefaultQuery("expiresIn", ""), cfg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	expiresAt := time.Now().Add(expiresIn)

	attachment, err := store.CreateAttachment(c.Request.Context(), userID, uuid.Nil, model.Attachment{
		Filename:    &header.Filename,
		ContentType: fileContentType,
		Size:        &result.Size,
		SHA256:      &result.SHA256,
		StorageKey:  &result.StorageKey,
		ExpiresAt:   &expiresAt,
		Status:      "ready",
	})
	if err != nil {
		handleError(c, err)
		return
	}

	c.JSON(http.StatusCreated, toUploadResponse(attachment))
}

func getAttachment(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	userID := security.GetUserID(c)
	attachID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "attachment not found"})
		return
	}

	attachment, err := store.GetAttachment(c.Request.Context(), userID, uuid.Nil, attachID)
	if err != nil {
		handleError(c, err)
		return
	}
	if strings.EqualFold(attachment.Status, "downloading") && attachment.SourceURL != nil && strings.TrimSpace(*attachment.SourceURL) != "" {
		c.Redirect(http.StatusTemporaryRedirect, *attachment.SourceURL)
		return
	}
	if strings.EqualFold(attachment.Status, "failed") {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "attachment download failed"})
		return
	}
	if strings.EqualFold(attachment.Status, "uploading") {
		c.JSON(http.StatusConflict, gin.H{"error": "attachment upload in progress"})
		return
	}

	if attachment.StorageKey == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment content not available"})
		return
	}

	if cfg.S3DirectDownload {
		if signed, err := attachStore.GetSignedURL(c.Request.Context(), *attachment.StorageKey, cfg.AttachmentDownloadURLExpiresIn); err == nil {
			c.Redirect(http.StatusFound, signed.String())
			return
		}
	}

	streamAttachment(c, attachStore, *attachment.StorageKey, attachment)
}

func deleteAttachment(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore) {
	userID := security.GetUserID(c)
	attachID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "attachment not found"})
		return
	}

	attachment, err := store.GetAttachment(c.Request.Context(), userID, uuid.Nil, attachID)
	if err != nil {
		handleError(c, err)
		return
	}

	if attachment.StorageKey != nil {
		if err := attachStore.Delete(c.Request.Context(), *attachment.StorageKey); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete attachment content"})
			return
		}
	}

	if err := store.DeleteAttachment(c.Request.Context(), userID, uuid.Nil, attachID); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func downloadURL(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, signingKey []byte) {
	userID := security.GetUserID(c)
	attachID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "attachment not found"})
		return
	}

	attachment, err := store.GetAttachment(c.Request.Context(), userID, uuid.Nil, attachID)
	if err != nil {
		handleError(c, err)
		return
	}
	if strings.EqualFold(attachment.Status, "downloading") && attachment.SourceURL != nil && strings.TrimSpace(*attachment.SourceURL) != "" {
		c.JSON(http.StatusOK, gin.H{
			"url":       *attachment.SourceURL,
			"expiresIn": 0,
			"status":    "downloading",
		})
		return
	}
	if strings.EqualFold(attachment.Status, "failed") {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "attachment download failed"})
		return
	}
	if strings.EqualFold(attachment.Status, "uploading") {
		c.JSON(http.StatusConflict, gin.H{"error": "attachment upload in progress"})
		return
	}
	if attachment.StorageKey == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment content not available"})
		return
	}

	if cfg.S3DirectDownload {
		if signedURL, err := attachStore.GetSignedURL(c.Request.Context(), *attachment.StorageKey, cfg.AttachmentDownloadURLExpiresIn); err == nil {
			c.JSON(http.StatusOK, gin.H{"url": signedURL.String(), "expiresIn": int(cfg.AttachmentDownloadURLExpiresIn.Seconds())})
			return
		}
	}

	if len(signingKey) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "download URLs are not available: encryption key is not configured"})
		return
	}

	filename := "download"
	if attachment.Filename != nil && strings.TrimSpace(*attachment.Filename) != "" {
		filename = *attachment.Filename
	}
	token := signDownloadToken(*attachment.StorageKey, signingKey, time.Now().Add(cfg.AttachmentDownloadURLExpiresIn))
	c.JSON(http.StatusOK, gin.H{
		"url":       fmt.Sprintf("/v1/attachments/download/%s/%s", token, filename),
		"expiresIn": int(cfg.AttachmentDownloadURLExpiresIn.Seconds()),
	})
}

func downloadByToken(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, signingKeys [][]byte) {
	storageKey, ok := verifyDownloadToken(c.Param("token"), signingKeys, time.Now())
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired token"})
		return
	}

	// Direct lookup by storage key — avoids the previous limit-200 scan.
	adminAtt, err := store.AdminGetAttachmentByStorageKey(c.Request.Context(), storageKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}
	streamAttachment(c, attachStore, storageKey, &adminAtt.Attachment)
}

func streamAttachment(c *gin.Context, attachStore registryattach.AttachmentStore, storageKey string, attachment *model.Attachment) {
	reader, err := attachStore.Retrieve(c.Request.Context(), storageKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve attachment"})
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
		c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", *attachment.Filename))
	}
	if attachment.Size != nil {
		c.Header("Content-Length", strconv.FormatInt(*attachment.Size, 10))
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, reader)
}

func toUploadResponse(attachment *model.Attachment) gin.H {
	var expiresAt *string
	if attachment.ExpiresAt != nil {
		v := attachment.ExpiresAt.UTC().Format(time.RFC3339)
		expiresAt = &v
	}
	r := gin.H{
		"id":          attachment.ID.String(),
		"href":        "/v1/attachments/" + attachment.ID.String(),
		"contentType": attachment.ContentType,
		"filename":    attachment.Filename,
		"size":        attachment.Size,
		"sha256":      attachment.SHA256,
		"expiresAt":   expiresAt,
		"status":      attachment.Status,
	}
	if attachment.SourceURL != nil {
		r["sourceUrl"] = *attachment.SourceURL
	}
	return r
}

func parseExpiresIn(raw string, cfg *config.Config) (time.Duration, error) {
	value := strings.TrimSpace(strings.ToUpper(raw))
	defaultDuration := time.Hour
	maxDuration := 24 * time.Hour
	if cfg != nil {
		if cfg.AttachmentDefaultExpiresIn > 0 {
			defaultDuration = cfg.AttachmentDefaultExpiresIn
		}
		if cfg.AttachmentMaxExpiresIn > 0 {
			maxDuration = cfg.AttachmentMaxExpiresIn
		}
	}
	if value == "" {
		return defaultDuration, nil
	}
	if strings.HasPrefix(value, "PT") && strings.HasSuffix(value, "H") {
		num := strings.TrimSuffix(strings.TrimPrefix(value, "PT"), "H")
		hours, err := strconv.Atoi(num)
		if err != nil || hours <= 0 {
			return 0, fmt.Errorf("invalid expiresIn value")
		}
		d := time.Duration(hours) * time.Hour
		if d > maxDuration {
			return 0, fmt.Errorf("expiresIn cannot exceed PT24H")
		}
		return d, nil
	}
	if strings.HasPrefix(value, "PT") && strings.HasSuffix(value, "M") {
		num := strings.TrimSuffix(strings.TrimPrefix(value, "PT"), "M")
		mins, err := strconv.Atoi(num)
		if err != nil || mins <= 0 {
			return 0, fmt.Errorf("invalid expiresIn value")
		}
		d := time.Duration(mins) * time.Minute
		if d > maxDuration {
			return 0, fmt.Errorf("expiresIn cannot exceed PT24H")
		}
		return d, nil
	}
	return 0, fmt.Errorf("invalid expiresIn value")
}

func signDownloadToken(storageKey string, secret []byte, expiresAt time.Time) string {
	payload := fmt.Sprintf("%s|%d", storageKey, expiresAt.Unix())
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return encodedPayload + "." + sig
}

func verifyDownloadToken(token string, secrets [][]byte, now time.Time) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	payload := string(payloadBytes)

	matched := false
	for _, secret := range secrets {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write([]byte(payload))
		expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(parts[1]), []byte(expected)) {
			matched = true
			break
		}
	}
	if !matched {
		return "", false
	}

	payloadParts := strings.Split(payload, "|")
	if len(payloadParts) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(payloadParts[1], 10, 64)
	if err != nil {
		return "", false
	}
	if now.Unix() > exp {
		return "", false
	}
	return payloadParts[0], true
}

func completeSourceURLAttachment(store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, attachmentID uuid.UUID, userID, sourceURL, contentType string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := validateSourceURL(sourceURL, cfg.AllowPrivateSourceURLs); err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, err)
		return
	}
	client := &http.Client{
		Timeout: 3 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateSourceURL(req.URL.String(), cfg.AllowPrivateSourceURLs)
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, fmt.Errorf("source download failed: status %d", resp.StatusCode))
		return
	}

	resolvedContentType := strings.TrimSpace(contentType)
	if headerCT := strings.TrimSpace(resp.Header.Get("Content-Type")); headerCT != "" {
		if idx := strings.Index(headerCT, ";"); idx >= 0 {
			headerCT = strings.TrimSpace(headerCT[:idx])
		}
		if resolvedContentType == "" || strings.EqualFold(resolvedContentType, "application/octet-stream") {
			resolvedContentType = headerCT
		}
	}
	if resolvedContentType == "" {
		resolvedContentType = "application/octet-stream"
	}

	tmp, err := tempfiles.Create(cfg.ResolvedTempDir(), "memory-service-source-url-*")
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, err)
		return
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	limited := io.LimitReader(resp.Body, cfg.AttachmentMaxSize+1)
	written, err := io.Copy(tmp, limited)
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, err)
		return
	}
	if written > cfg.AttachmentMaxSize {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, fmt.Errorf("file exceeds maximum size of %d bytes", cfg.AttachmentMaxSize))
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, err)
		return
	}

	result, err := attachStore.Store(ctx, tmp, cfg.AttachmentMaxSize, resolvedContentType)
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, userID, err)
		return
	}

	status := "ready"
	size := result.Size
	sha := result.SHA256
	storageKey := result.StorageKey
	if _, err := store.UpdateAttachment(ctx, userID, attachmentID, registrystore.AttachmentUpdate{
		StorageKey:  &storageKey,
		ContentType: &resolvedContentType,
		Size:        &size,
		SHA256:      &sha,
		Status:      &status,
	}); err != nil {
		log.Error("Failed to update downloaded attachment", "attachmentId", attachmentID.String(), "err", err)
	}
}

func markSourceURLAttachmentFailed(ctx context.Context, store registrystore.MemoryStore, attachmentID uuid.UUID, userID string, cause error) {
	status := "failed"
	if _, err := store.UpdateAttachment(ctx, userID, attachmentID, registrystore.AttachmentUpdate{
		Status: &status,
	}); err != nil {
		log.Error("Failed to mark attachment as failed", "attachmentId", attachmentID.String(), "err", err, "cause", cause)
		return
	}
	log.Warn("Source URL attachment download failed", "attachmentId", attachmentID.String(), "err", cause)
}

func validateSourceURL(raw string, allowPrivate bool) error {
	uri, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid sourceUrl")
	}
	scheme := strings.ToLower(uri.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("only http and https URLs are supported")
	}
	host := uri.Hostname()
	if host == "" {
		return fmt.Errorf("sourceUrl must include a host")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve sourceUrl host")
	}
	if allowPrivate {
		return nil
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URLs targeting localhost or private networks are not allowed")
		}
	}
	return nil
}

func handleError(c *gin.Context, err error) {
	var notFound *registrystore.NotFoundError
	var validation *registrystore.ValidationError
	var conflict *registrystore.ConflictError
	var forbidden *registrystore.ForbiddenError

	switch {
	case err == nil:
		return
	case strings.Contains(err.Error(), "maximum size"):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
	case errors.As(err, &notFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.As(err, &conflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.As(err, &forbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}
