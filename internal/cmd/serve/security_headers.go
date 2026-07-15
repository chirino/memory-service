package serve

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodTrace {
			c.AbortWithStatus(http.StatusMethodNotAllowed)
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		if c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
