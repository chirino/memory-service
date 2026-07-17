package serve

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// newGinRouter keeps percent encoding intact while Gin matches route segments.
// The generated OpenAPI wrapper decodes each captured parameter exactly once
// with path semantics before invoking the proxy server interface.
func newGinRouter() *gin.Engine {
	router := gin.New()
	router.UseEscapedPath = true
	router.UnescapePathValues = false
	// Make unmatched encoded paths explicit inside Gin's middleware chain so
	// the error-envelope writer cannot commit its default 200 before Gin's
	// fallback 404 runs.
	router.NoRoute(func(c *gin.Context) {
		c.Status(http.StatusNotFound)
	})
	return router
}

// setDecodedPathParam exposes the value decoded by the generated OpenAPI
// wrapper to the existing route handlers, which read path values from Gin.
func setDecodedPathParam(c *gin.Context, key, value string) {
	for i := range c.Params {
		if c.Params[i].Key == key {
			c.Params[i].Value = value
			return
		}
	}
	c.Params = append(c.Params, gin.Param{Key: key, Value: value})
}
