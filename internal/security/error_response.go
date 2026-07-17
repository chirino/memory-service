package security

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// ErrorEnvelopeMiddleware normalizes non-streaming REST error responses that reach
// application middleware. Transport-level errors that happen before gin are outside this path.
func ErrorEnvelopeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		writer := &errorEnvelopeWriter{
			ResponseWriter: c.Writer,
			status:         http.StatusOK,
			size:           -1,
		}
		c.Writer = writer
		c.Next()
		writer.finish(c)
	}
}

type errorEnvelopeWriter struct {
	gin.ResponseWriter
	status int
	size   int
	wrote  bool
	body   bytes.Buffer
}

func (w *errorEnvelopeWriter) WriteHeader(code int) {
	if code <= 0 {
		return
	}
	if w.wrote {
		return
	}
	w.status = code
}

func (w *errorEnvelopeWriter) WriteHeaderNow() {
	if w.wrote {
		return
	}
	w.wrote = true
	w.size = 0
	if !w.shouldCapture() {
		w.ResponseWriter.WriteHeader(w.status)
	}
}

func (w *errorEnvelopeWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	if w.shouldCapture() {
		n, err := w.body.Write(data)
		w.size += n
		return n, err
	}
	n, err := w.ResponseWriter.Write(data)
	w.size += n
	return n, err
}

func (w *errorEnvelopeWriter) WriteString(data string) (int, error) {
	w.WriteHeaderNow()
	if w.shouldCapture() {
		n, err := w.body.WriteString(data)
		w.size += n
		return n, err
	}
	n, err := w.ResponseWriter.WriteString(data)
	w.size += n
	return n, err
}

func (w *errorEnvelopeWriter) Status() int {
	return w.status
}

func (w *errorEnvelopeWriter) Size() int {
	return w.size
}

func (w *errorEnvelopeWriter) Written() bool {
	return w.wrote
}

func (w *errorEnvelopeWriter) Flush() {
	w.finish(nil)
	w.ResponseWriter.Flush()
}

func (w *errorEnvelopeWriter) finish(c *gin.Context) {
	if w.ResponseWriter.Written() {
		return
	}
	if w.status < http.StatusBadRequest {
		// WriteHeader on Gin's writer records the status; Gin commits it after
		// middleware returns. Forward it even when the handler wrote no body.
		w.ResponseWriter.WriteHeader(w.status)
		return
	}
	if isStreamingContentType(w.Header().Get("Content-Type")) {
		w.ResponseWriter.WriteHeader(w.status)
		if w.body.Len() > 0 {
			_, _ = w.ResponseWriter.Write(w.body.Bytes())
		}
		return
	}

	requestID := ""
	if c != nil {
		requestID = RequestIDFromGin(c)
	}
	body := normalizeErrorBody(w.status, requestID, w.body.Bytes())
	encoded, err := json.Marshal(body)
	if err != nil {
		encoded = []byte(`{"code":"internal_error","error":"internal server error"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(encoded)
}

func (w *errorEnvelopeWriter) shouldCapture() bool {
	return w.status >= http.StatusBadRequest
}

func normalizeErrorBody(status int, requestID string, raw []byte) gin.H {
	body := gin.H{}
	if len(bytes.TrimSpace(raw)) > 0 {
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err == nil && parsed != nil {
			for k, v := range parsed {
				body[k] = v
			}
		}
	}

	if _, ok := body["code"].(string); !ok {
		if codeFromError, _ := body["error"].(string); isStableErrorCode(codeFromError) {
			body["code"] = codeFromError
		} else {
			body["code"] = defaultRESTErrorCode(status)
		}
	}
	if _, ok := body["error"].(string); !ok {
		body["error"] = defaultRESTErrorMessage(status)
	}

	if requestID != "" {
		body["requestId"] = requestID
	}
	return body
}

func isStableErrorCode(value string) bool {
	switch value {
	case "invalid_request", "validation_error", "unauthenticated", "permission_denied",
		"not_found", "method_not_allowed", "request_timeout", "conflict",
		"payload_too_large", "unsupported_media_type", "rate_limited",
		"internal_error", "not_implemented", "search_type_unavailable",
		"upstream_error", "service_unavailable", "upstream_timeout":
		return true
	default:
		return false
	}
}

func defaultRESTErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "permission_denied"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusRequestTimeout:
		return "request_timeout"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusNotImplemented:
		return "not_implemented"
	case http.StatusBadGateway:
		return "upstream_error"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusGatewayTimeout:
		return "upstream_timeout"
	default:
		if status >= http.StatusInternalServerError {
			return "internal_error"
		}
		return "invalid_request"
	}
}

func defaultRESTErrorMessage(status int) string {
	if status >= http.StatusInternalServerError {
		return "internal server error"
	}
	return http.StatusText(status)
}

func isStreamingContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(contentType), "text/event-stream")
}
