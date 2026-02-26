package serve

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func corsMiddleware(originsCSV string) gin.HandlerFunc {
	origins := parseOrigins(originsCSV)
	allowAny := len(origins) == 1 && origins["*"]
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && (allowAny || origins[origin]) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Client-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.Status(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func parseOrigins(raw string) map[string]bool {
	result := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		result[v] = true
	}
	if len(result) == 0 {
		result["*"] = true
	}
	return result
}
