package security

import (
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

// AccessLogMiddleware logs each HTTP request with method, path, status, and duration.
// Paths listed in skipPaths are silently passed through without logging.
func AccessLogMiddleware(skipPaths ...string) gin.HandlerFunc {
	skip := make(map[string]bool, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = true
	}
	return func(c *gin.Context) {
		if skip[c.Request.URL.Path] {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		duration := time.Since(start)

		log.Info("HTTP request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration", duration,
			"clientIP", c.ClientIP(),
			"userAgent", c.Request.UserAgent(),
		)
	}
}

// AdminAuditMiddleware logs admin API calls with caller identity and target resource.
// When requireJustification is true, admin requests must include a justification
// via query param (?justification=...) or X-Justification header.
func AdminAuditMiddleware(requireJustification bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/v1/admin") {
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

		if strings.HasPrefix(c.Request.URL.Path, "/v1/admin") {
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
				"clientIP", c.ClientIP(),
				"justification", justification,
			)
		}
	}
}
