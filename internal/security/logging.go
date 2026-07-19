package security

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/gin-gonic/gin"
)

const contextKeyOperationPanic = "operationEventRecoveredPanic"
const contextKeyOperationTerminal = "operationEventTerminalOverride"

type operationTerminalOverride struct {
	result operationevent.Result
	reason string
	err    error
}

// OperationEventMiddleware emits one canonical terminal event for each REST operation.
// Paths listed in skipPaths are silently passed through without operational logging.
func OperationEventMiddleware(skipPaths ...string) gin.HandlerFunc {
	skip := make(map[string]bool, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = true
	}
	return func(c *gin.Context) {
		if skip[c.Request.URL.Path] {
			c.Next()
			return
		}
		event := operationevent.New(ginOperationName(c))
		event.SetRequestID(RequestIDFromGin(c))
		c.Request = c.Request.WithContext(operationevent.WithContext(c.Request.Context(), event))
		c.Next()

		enrichGinOperationEvent(c, event)
		status := c.Writer.Status()
		event.SetHTTPStatus(status)
		if code := RESTErrorCodeFromGin(c); code != "" {
			event.SetErrorCode(code)
		}
		if last := c.Errors.Last(); last != nil && status >= 400 {
			event.EnrichError(last.Err)
		}
		result := operationevent.ResultFromHTTP(status, c.Request.Context().Err())
		if value, ok := c.Get(contextKeyOperationTerminal); ok {
			if terminal, ok := value.(operationTerminalOverride); ok {
				result = terminal.result
				event.EnrichError(terminal.err)
				event.SetReason(terminal.reason)
			}
		}
		if c.GetBool(contextKeyOperationPanic) {
			event.SetErrorCode("internal_error")
			result = operationevent.ResultFailed
		}
		event.EmitTerminal(result)
	}
}

// SetOperationTerminalError records a failure that happens after an HTTP
// streaming response has committed. The operation middleware applies the
// override when the handler returns so the canonical terminal record does not
// incorrectly inherit the already-committed success status.
func SetOperationTerminalError(c *gin.Context, reason string, err error) {
	if c == nil || err == nil {
		return
	}
	result := operationevent.ResultFailed
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result = operationevent.ResultTimedOut
		reason = "deadline"
	case errors.Is(err, context.Canceled):
		result = operationevent.ResultCanceled
		reason = "client_disconnect"
	}
	c.Set(contextKeyOperationTerminal, operationTerminalOverride{
		result: result,
		reason: reason,
		err:    err,
	})
}

// OperationRecoveryMiddleware logs non-connection panics with their operation
// correlation and marks the canonical event failed even after response headers
// have committed. Expected connection-abort panics remain stack-suppressed.
func OperationRecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if err, ok := recovered.(error); ok && isConnectionAbort(err) {
				_ = c.Error(err)
				c.Set(contextKeyOperationTerminal, operationTerminalOverride{
					result: operationevent.ResultCanceled,
					reason: "client_disconnect",
				})
				c.Abort()
				return
			}
			stack := debug.Stack()
			event := OperationEventFromGin(c)
			operationevent.LogRecoveredPanic(event, ginOperationName(c), recovered, stack)
			c.Set(contextKeyOperationPanic, true)
			c.Abort()
			if writer, ok := c.Writer.(*errorEnvelopeWriter); ok {
				writer.finishRecoveredPanic(c)
			} else {
				c.Writer.WriteHeader(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

func isConnectionAbort(err error) bool {
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, http.ErrAbortHandler)
}

func ginOperationName(c *gin.Context) string {
	route := c.FullPath()
	if route == "" {
		route = "<unmatched>"
	} else {
		route = canonicalGinRoute(route)
	}
	return fmt.Sprintf("http %s %s", c.Request.Method, route)
}

func canonicalGinRoute(route string) string {
	parts := strings.Split(route, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") && len(part) > 1 {
			parts[i] = "{" + part[1:] + "}"
		} else if strings.HasPrefix(part, "*") && len(part) > 1 {
			parts[i] = "{" + part[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}

// OperationEventFromGin returns the canonical event associated with a request.
func OperationEventFromGin(c *gin.Context) *operationevent.Event {
	if c == nil || c.Request == nil {
		return nil
	}
	return operationevent.FromContext(c.Request.Context())
}

func enrichGinOperationEvent(c *gin.Context, event *operationevent.Event) {
	if event == nil {
		return
	}
	event.SetUserID(c.GetString(ContextKeyUserID))
	event.SetClientID(c.GetString(ContextKeyClientID))
}

// AdminAuditMiddleware logs admin API calls with caller identity and target resource.
// When requireJustification is true, admin requests must include a justification
// via query param (?justification=...) or X-Justification header.
func AdminAuditMiddleware(requireJustification bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdminPath := strings.HasPrefix(c.Request.URL.Path, "/v1/admin") || strings.HasPrefix(c.Request.URL.Path, "/admin/v1")
		if isAdminPath {
			justification := c.Query("justification")
			if justification == "" {
				justification = c.GetHeader("X-Justification")
			}
			if requireJustification && justification == "" {
				c.AbortWithStatusJSON(400, gin.H{"error": "justification is required"})
				return
			}
		}

		c.Next()

		if isAdminPath {
			justification := c.Query("justification")
			if justification == "" {
				justification = c.GetHeader("X-Justification")
			}
			role := EffectiveAdminRole(c)
			if role == "" {
				role = "none"
			}
			log.Info("Admin audit",
				"caller", c.GetString(ContextKeyUserID),
				"role", role,
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"status", c.Writer.Status(),
				"requestId", RequestIDFromGin(c),
				"clientIP", c.ClientIP(),
				"justification", justification,
			)
		}
	}
}
