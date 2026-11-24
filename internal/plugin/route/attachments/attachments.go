package attachments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/chirino/memory-service/internal/plugin/route/routetx"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/tempfiles"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

// HandleUpload exposes attachment upload/create for wrapper-native adapters.
func HandleUpload(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	upload(c, store, attachStore, cfg)
}

// HandleGetAttachment exposes attachment retrieval for wrapper-native adapters.
func HandleGetAttachment(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	getAttachment(c, store, attachStore, cfg)
}

// HandleGetAttachmentMetadata exposes the metadata-only attachment lookup for wrapper-native adapters.
func HandleGetAttachmentMetadata(c *gin.Context, store registrystore.MemoryStore) {
	getAttachmentMetadata(c, store)
}

// HandleDeleteAttachment exposes attachment delete for wrapper-native adapters.
func HandleDeleteAttachment(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore) {
	deleteAttachment(c, store, attachStore)
}

// HandleDownloadURL exposes attachment signed URL issuance for wrapper-native adapters.
func HandleDownloadURL(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, signingKey []byte) {
	downloadURL(c, store, attachStore, cfg, signingKey)
}

// HandleDownloadByToken exposes token download for wrapper-native adapters.
func HandleDownloadByToken(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, signingKeys [][]byte) {
	downloadByToken(c, store, attachStore, signingKeys)
}

func upload(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	userID := security.GetUserID(c)

	contentType := c.GetHeader("Content-Type")
	if strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		var req struct {
			SourceURL   string `json:"sourceUrl"   binding:"required"`
			ContentType string `json:"contentType"`
			Name        string `json:"name"`
			// ID is the optional caller-supplied attachment ID. A pointer keeps an
			// absent field apart from an empty one, which is invalid.
			ID *string `json:"id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		callerID, hasCallerID, err := ParseCallerAttachmentID(req.ID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "error": err.Error()})
			return
		}
		recordCallerAttachmentID(c, callerID, hasCallerID)
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
		if hasCallerID {
			createSourceURLAttachmentWithCallerID(c, store, attachStore, cfg, userID, callerID, model.Attachment{
				Filename:    filename,
				ContentType: fileContentType,
				SourceURL:   &sourceURL,
				ExpiresAt:   &expiresAt,
				Status:      registrystore.AttachmentStatusDownloading,
			})
			return
		}
		var attachmentID uuid.UUID
		if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
			attachmentID = uuid.Nil
			attachment, err := store.CreateAttachment(ctx, userID, "", model.Attachment{
				Filename:    filename,
				ContentType: fileContentType,
				SourceURL:   &sourceURL,
				ExpiresAt:   &expiresAt,
				Status:      "downloading",
			})
			if err != nil {
				return err
			}
			attachmentID = attachment.ID
			c.JSON(http.StatusCreated, toUploadResponse(attachment))
			return nil
		}); err != nil {
			handleError(c, err)
			return
		}
		StartSourceURLAttachmentDownload(c.Request.Context(), store, attachStore, cfg, attachmentID, userID, sourceURL, fileContentType)
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

	// FormFile has parsed the form, so the optional ID field is available. It is
	// validated before any blob is stored.
	var rawCallerID *string
	if values := c.Request.MultipartForm.Value["id"]; len(values) > 0 {
		rawCallerID = &values[0]
	}
	callerID, hasCallerID, err := ParseCallerAttachmentID(rawCallerID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "error": err.Error()})
		return
	}
	recordCallerAttachmentID(c, callerID, hasCallerID)

	expiresIn, err := parseExpiresIn(c.DefaultQuery("expiresIn", ""), cfg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if hasCallerID {
		uploadWithCallerID(c, store, attachStore, cfg, userID, callerID, file, header.Filename, fileContentType, expiresIn)
		return
	}
	expiresAt := time.Now().Add(expiresIn)

	result, err := attachStore.Store(c.Request.Context(), file, cfg.AttachmentMaxSize, fileContentType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		attachment, err := store.CreateAttachment(ctx, userID, "", model.Attachment{
			Filename:    &header.Filename,
			ContentType: fileContentType,
			Size:        &result.Size,
			SHA256:      &result.SHA256,
			StorageKey:  &result.StorageKey,
			ExpiresAt:   &expiresAt,
			Status:      "ready",
		})
		if err != nil {
			return err
		}
		c.JSON(http.StatusCreated, toUploadResponse(attachment))
		return nil
	}); err != nil {
		if cleanupErr := registrystore.EnqueueAttachmentBlobDeleteAfterFinalizationFailure(c.Request.Context(), store, result.StorageKey); cleanupErr != nil {
			log.Error("Failed to enqueue attachment cleanup after metadata creation failure", "storageKey", result.StorageKey, "err", cleanupErr, "cause", err)
			err = errors.Join(err, fmt.Errorf("enqueue stored attachment cleanup: %w", cleanupErr))
		}
		handleError(c, err)
	}
}

func getAttachment(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config) {
	userID := security.GetUserID(c)
	attachID, err := parseAttachmentIDParam(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "attachment not found"})
		return
	}

	disposition, ok := parseDisposition(c, "")
	if !ok {
		return
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		attachment, err := store.GetAttachment(ctx, userID, "", attachID)
		if err != nil {
			return err
		}
		if strings.EqualFold(attachment.Status, "downloading") && attachment.SourceURL != nil && strings.TrimSpace(*attachment.SourceURL) != "" {
			c.Redirect(http.StatusTemporaryRedirect, *attachment.SourceURL)
			return nil
		}
		if strings.EqualFold(attachment.Status, "failed") {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "attachment download failed"})
			return nil
		}
		if strings.EqualFold(attachment.Status, "uploading") {
			c.JSON(http.StatusConflict, gin.H{"error": "attachment upload in progress"})
			return nil
		}

		if attachment.StorageKey == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "attachment content not available"})
			return nil
		}

		policy := attachmentResponsePolicy(attachment.ContentType, disposition)
		if cfg.S3DirectDownload && policy.AllowDirect {
			if signed, err := attachStore.GetSignedURL(ctx, *attachment.StorageKey, cfg.AttachmentDownloadURLExpiresIn, signedURLOptions(policy.Disposition, attachment.Filename)); err == nil {
				c.Redirect(http.StatusFound, signed.String())
				return nil
			}
		}

		streamAttachment(c, attachStore, *attachment.StorageKey, attachment, disposition)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// getAttachmentMetadata returns the attachment's metadata and status without its content. It uses
// GetAttachment, so the access rules match GET /v1/attachments/{id}. Unlike that endpoint, an
// uploading, downloading, or failed attachment is a normal 200: the status is the answer.
func getAttachmentMetadata(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	attachID, err := parseAttachmentIDParam(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "error": "invalid attachment id"})
		return
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		attachment, err := store.GetAttachment(ctx, userID, "", attachID)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, toUploadResponse(attachment))
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func deleteAttachment(c *gin.Context, store registrystore.MemoryStore, _ registryattach.AttachmentStore) {
	userID := security.GetUserID(c)
	attachID, err := parseAttachmentIDParam(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "attachment not found"})
		return
	}

	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		if err := store.DeleteAttachment(ctx, userID, "", attachID); err != nil {
			return err
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
}

func downloadURL(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, signingKey []byte) {
	userID := security.GetUserID(c)
	attachID, err := parseAttachmentIDParam(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "attachment not found"})
		return
	}
	disposition, ok := parseDisposition(c, "")
	if !ok {
		return
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		attachment, err := store.GetAttachment(ctx, userID, "", attachID)
		if err != nil {
			return err
		}
		if strings.EqualFold(attachment.Status, "downloading") && attachment.SourceURL != nil && strings.TrimSpace(*attachment.SourceURL) != "" {
			c.JSON(http.StatusOK, gin.H{
				"url":       *attachment.SourceURL,
				"expiresIn": 0,
				"status":    "downloading",
			})
			return nil
		}
		if strings.EqualFold(attachment.Status, "failed") {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "attachment download failed"})
			return nil
		}
		if strings.EqualFold(attachment.Status, "uploading") {
			c.JSON(http.StatusConflict, gin.H{"error": "attachment upload in progress"})
			return nil
		}
		if attachment.StorageKey == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "attachment content not available"})
			return nil
		}

		policy := attachmentResponsePolicy(attachment.ContentType, disposition)
		if cfg.S3DirectDownload && policy.AllowDirect {
			if signedURL, err := attachStore.GetSignedURL(ctx, *attachment.StorageKey, cfg.AttachmentDownloadURLExpiresIn, signedURLOptions(policy.Disposition, attachment.Filename)); err == nil {
				c.JSON(http.StatusOK, gin.H{"url": signedURL.String(), "expiresIn": int(cfg.AttachmentDownloadURLExpiresIn.Seconds())})
				return nil
			}
		}

		if len(signingKey) == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "download URLs are not available: encryption key is not configured"})
			return nil
		}

		filename := "download"
		if attachment.Filename != nil && strings.TrimSpace(*attachment.Filename) != "" {
			filename = *attachment.Filename
		}
		token := signDownloadToken(*attachment.StorageKey, signingKey, time.Now().Add(cfg.AttachmentDownloadURLExpiresIn))
		downloadURL := fmt.Sprintf(
			"/v1/attachments/download/%s/%s",
			url.PathEscape(token),
			url.PathEscape(filename),
		)
		if policy.Disposition != "" {
			downloadURL += "?disposition=" + url.QueryEscape(policy.Disposition)
		}
		c.JSON(http.StatusOK, gin.H{
			"url":       downloadURL,
			"expiresIn": int(cfg.AttachmentDownloadURLExpiresIn.Seconds()),
		})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func downloadByToken(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, signingKeys [][]byte) {
	disposition, ok := parseDisposition(c, "")
	if !ok {
		return
	}

	storageKey, ok := verifyDownloadToken(c.Param("token"), signingKeys, time.Now())
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired token"})
		return
	}

	// Direct lookup by storage key — avoids the previous limit-200 scan.
	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		adminAtt, err := store.AdminGetAttachmentByStorageKey(ctx, storageKey)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
			return nil
		}
		streamAttachment(c, attachStore, storageKey, &adminAtt.Attachment, disposition)
		return nil
	}); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
	}
}

func parseDisposition(c *gin.Context, defaultValue string) (string, bool) {
	disposition := strings.ToLower(strings.TrimSpace(c.Query("disposition")))
	if disposition == "" {
		return defaultValue, true
	}
	if disposition != "inline" && disposition != "attachment" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "disposition must be 'inline' or 'attachment'"})
		return "", false
	}
	return disposition, true
}

func signedURLOptions(disposition string, filename *string) *registryattach.SignedURLOptions {
	if disposition == "" {
		return nil
	}
	opts := &registryattach.SignedURLOptions{Disposition: disposition}
	if filename != nil {
		opts.Filename = strings.TrimSpace(*filename)
	}
	return opts
}

func contentDisposition(disposition string, filename *string) string {
	if disposition == "" {
		return ""
	}
	if filename == nil || strings.TrimSpace(*filename) == "" {
		return disposition
	}
	return mime.FormatMediaType(disposition, map[string]string{"filename": strings.TrimSpace(*filename)})
}

func streamAttachment(c *gin.Context, attachStore registryattach.AttachmentStore, storageKey string, attachment *model.Attachment, disposition string) {
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

	policy := attachmentResponsePolicy(attachment.ContentType, disposition)
	c.Header("Cache-Control", "private, max-age=300, immutable")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", policy.ContentType)
	if header := contentDisposition(policy.Disposition, attachment.Filename); header != "" {
		c.Header("Content-Disposition", header)
	}
	if attachment.Size != nil {
		c.Header("Content-Length", strconv.FormatInt(*attachment.Size, 10))
	}
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, reader); err != nil {
		log.Warn("attachment stream failed", "attachmentId", attachment.ID, "error", err)
		c.Abort()
	}
}

type responsePolicy struct {
	ContentType string
	Disposition string
	AllowDirect bool
}

// ResponsePolicy is the attachment response policy shared by REST and gRPC
// attachment download URL handlers.
type ResponsePolicy = responsePolicy

func attachmentResponsePolicy(contentType, requestedDisposition string) responsePolicy {
	mediaType := normalizedContentType(contentType)
	disposition := strings.ToLower(strings.TrimSpace(requestedDisposition))
	if activeAttachmentContentType(mediaType) {
		return responsePolicy{
			ContentType: "application/octet-stream",
			Disposition: "attachment",
			AllowDirect: false,
		}
	}
	return responsePolicy{
		ContentType: mediaType,
		Disposition: disposition,
		AllowDirect: disposition != "inline",
	}
}

// AttachmentResponsePolicy returns the content/disposition/direct-download
// policy for an attachment response.
func AttachmentResponsePolicy(contentType, requestedDisposition string) ResponsePolicy {
	return attachmentResponsePolicy(contentType, requestedDisposition)
}

// SignedURLOptions returns the S3 signing options for a disposition/filename pair.
func SignedURLOptions(disposition string, filename *string) *registryattach.SignedURLOptions {
	return signedURLOptions(disposition, filename)
}

func normalizedContentType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || mediaType == "" {
		return "application/octet-stream"
	}
	return strings.ToLower(mediaType)
}

func activeAttachmentContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "text/html", "application/xhtml+xml", "image/svg+xml":
		return true
	default:
		return false
	}
}

func parseAttachmentIDParam(c *gin.Context) (uuid.UUID, error) {
	// Wrapper routes use :id while legacy routes use :attachmentId.
	if raw := strings.TrimSpace(c.Param("attachmentId")); raw != "" {
		return uuid.Parse(raw)
	}
	return uuid.Parse(c.Param("id"))
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

// SignDownloadToken returns the HMAC token used by public attachment download URLs.
func SignDownloadToken(storageKey string, secret []byte, expiresAt time.Time) string {
	return signDownloadToken(storageKey, secret, expiresAt)
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

func completeSourceURLAttachment(ctx context.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, attachmentID uuid.UUID, userID, sourceURL, contentType string, tp trace.TracerProvider) error {
	return completeSourceURLAttachmentAttempt(ctx, store, attachStore, cfg, attachmentID, nil, userID, sourceURL, contentType, tp)
}

// completeSourceURLAttachmentAttempt downloads sourceURL into the attachment. A
// nil attemptID is the no-ID flow and updates the row by ID, as before. A non-nil
// attemptID is a caller-ID row: finalize and failure are fenced on that attempt,
// so a late download can never modify a row that was deleted or replaced.
func completeSourceURLAttachmentAttempt(ctx context.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, attachmentID uuid.UUID, attemptID *uuid.UUID, userID, sourceURL, contentType string, tp trace.TracerProvider) error {
	if err := validateSourceURL(sourceURL, cfg.AllowPrivateSourceURLs); err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
	}
	client := &http.Client{
		Timeout: 3 * time.Minute,
		Transport: otelhttp.NewTransport(
			newSourceURLTransport(cfg.AllowPrivateSourceURLs),
			otelhttp.WithTracerProvider(tp),
			otelhttp.WithPropagators(tracing.OutboundPropagatorFromContext(ctx)),
		),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateSourceURL(req.URL.String(), cfg.AllowPrivateSourceURLs)
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("source download failed: status %d", resp.StatusCode)
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
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
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	limited := io.LimitReader(resp.Body, cfg.AttachmentMaxSize+1)
	written, err := io.Copy(tmp, limited)
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
	}
	if written > cfg.AttachmentMaxSize {
		err := fmt.Errorf("file exceeds maximum size of %d bytes", cfg.AttachmentMaxSize)
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
	}

	result, err := attachStore.Store(ctx, tmp, cfg.AttachmentMaxSize, resolvedContentType)
	if err != nil {
		markSourceURLAttachmentFailed(ctx, store, attachmentID, attemptID, userID, err)
		return err
	}

	if attemptID != nil {
		return finalizeFencedSourceURLAttachment(ctx, store, cfg, attachmentID, *attemptID, userID, resolvedContentType, result)
	}
	status := "ready"
	size := result.Size
	sha := result.SHA256
	storageKey := result.StorageKey
	if err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		if _, err := store.UpdateAttachment(txCtx, userID, attachmentID, registrystore.AttachmentUpdate{
			StorageKey:  &storageKey,
			ContentType: &resolvedContentType,
			Size:        &size,
			SHA256:      &sha,
			Status:      &status,
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		if cleanupErr := registrystore.EnqueueAttachmentBlobDeleteAfterFinalizationFailure(ctx, store, storageKey); cleanupErr != nil {
			log.Error("Failed to enqueue downloaded attachment cleanup", "attachmentId", attachmentID.String(), "storageKey", storageKey, "err", cleanupErr, "cause", err)
			return errors.Join(err, fmt.Errorf("enqueue downloaded attachment cleanup: %w", cleanupErr))
		}
		log.Error("Failed to update downloaded attachment", "attachmentId", attachmentID.String(), "err", err)
		return err
	}
	return nil
}

// StartSourceURLAttachmentDownload starts the same asynchronous source URL
// download workflow used by the REST attachment API.
func StartSourceURLAttachmentDownload(parentCtx context.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, attachmentID uuid.UUID, userID, sourceURL, contentType string) {
	startSourceURLAttachmentDownload(parentCtx, store, attachStore, cfg, attachmentID, nil, userID, sourceURL, contentType)
}

// StartFencedSourceURLAttachmentDownload starts the download for a row created
// from a caller-supplied ID. attemptID is the attempt that created the row; the
// download finalizes or fails the row only while that attempt is still current.
func StartFencedSourceURLAttachmentDownload(parentCtx context.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, attachmentID, attemptID uuid.UUID, userID, sourceURL, contentType string) {
	startSourceURLAttachmentDownload(parentCtx, store, attachStore, cfg, attachmentID, &attemptID, userID, sourceURL, contentType)
}

func startSourceURLAttachmentDownload(parentCtx context.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, attachmentID uuid.UUID, attemptID *uuid.UUID, userID, sourceURL, contentType string) {
	event := operationevent.New("job.attachment_download")
	event.SetAttachmentID(attachmentID.String())
	event.SetUserID(userID)
	if parent := operationevent.FromContext(parentCtx); parent != nil {
		event.SetRequestID(parent.Snapshot().RequestID)
	}
	tp := tracing.ProviderFromContextOrNoop(parentCtx)
	// context.WithoutCancel preserves the participation marker and provider from
	// parentCtx while detaching from the request cancellation signal, so the
	// async goroutine runs to completion independent of the HTTP request lifetime.
	baseCtx := context.WithoutCancel(parentCtx)
	ctx, cancel := context.WithTimeout(baseCtx, 5*time.Minute)
	ctx = operationevent.WithContext(ctx, event)
	go func() {
		defer cancel()
		runSourceURLAttachmentOperation(ctx, event, func(ctx context.Context) error {
			return completeSourceURLAttachmentAttempt(ctx, store, attachStore, cfg, attachmentID, attemptID, userID, sourceURL, contentType, tp)
		})
	}()
}

func runSourceURLAttachmentOperation(ctx context.Context, event *operationevent.Event, work func(context.Context) error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			stack := debug.Stack()
			err := operationevent.RecoveredPanicError(event, "", recovered, stack)
			event.EnrichError(err)
			if errors.Is(err, context.Canceled) {
				event.SetReason("client_disconnect")
				event.EmitTerminal(operationevent.ResultCanceled)
				return
			}
			event.SetFailureCount(1)
			event.EmitTerminal(operationevent.ResultFailed)
		}
	}()
	event.EmitStart()

	err := work(ctx)
	if err == nil {
		event.EmitTerminal(operationevent.ResultSuccess)
		return
	}
	event.EnrichError(err)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		event.SetReason("deadline")
		event.EmitTerminal(operationevent.ResultTimedOut)
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		event.SetReason("shutdown")
		event.EmitTerminal(operationevent.ResultCanceled)
	case errors.Is(err, operationevent.ErrRecoveredPanic):
		event.SetReason("panic")
		event.SetFailureCount(1)
		event.EmitTerminal(operationevent.ResultFailed)
	default:
		event.SetReason("download_failed")
		event.SetFailureCount(1)
		event.EmitTerminal(operationevent.ResultFailed)
	}
}

func markSourceURLAttachmentFailed(ctx context.Context, store registrystore.MemoryStore, attachmentID uuid.UUID, attemptID *uuid.UUID, userID string, cause error) {
	if attemptID != nil {
		// Fenced: matches nothing when the row finished or was replaced.
		markAttemptFailed(ctx, store, userID, attachmentID, *attemptID, cause)
		return
	}
	status := "failed"
	if err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		if _, err := store.UpdateAttachment(txCtx, userID, attachmentID, registrystore.AttachmentUpdate{
			Status: &status,
		}); err != nil {
			return err
		}
		return nil
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

func newSourceURLTransport(allowPrivate bool) http.RoundTripper {
	base := http.DefaultTransport.(*http.Transport).Clone()
	if allowPrivate {
		return base
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		if err := validateResolvedHost(host); err != nil {
			return nil, err
		}
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if err := validateRemoteAddr(conn.RemoteAddr()); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
	return base
}

func validateResolvedHost(host string) error {
	if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
		return validateSourceIP(ip)
	}
	return nil
}

func validateRemoteAddr(addr net.Addr) error {
	switch v := addr.(type) {
	case *net.TCPAddr:
		return validateSourceIP(v.IP)
	case *net.UDPAddr:
		return validateSourceIP(v.IP)
	case *net.IPAddr:
		return validateSourceIP(v.IP)
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			host = addr.String()
		}
		if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
			return validateSourceIP(ip)
		}
		return nil
	}
}

func validateSourceIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("cannot resolve sourceUrl host")
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("URLs targeting localhost or private networks are not allowed")
	}
	if len(ip) == net.IPv6len {
		if ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
			return fmt.Errorf("URLs targeting localhost or private networks are not allowed")
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("URLs targeting localhost or private networks are not allowed")
		}
		if ip[0]&0xfe == 0xfc {
			return fmt.Errorf("URLs targeting localhost or private networks are not allowed")
		}
	}
	return nil
}

func NewSourceURLTransportForTest(allowPrivate bool) http.RoundTripper {
	return newSourceURLTransport(allowPrivate)
}

func ValidateSourceURLForTest(raw string, allowPrivate bool) error {
	return validateSourceURL(raw, allowPrivate)
}

func handleError(c *gin.Context, err error) {
	_ = c.Error(err)
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
