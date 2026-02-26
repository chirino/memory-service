package system

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	registryroute "github.com/chirino/memory-service/internal/registry/route"
)

var ready atomic.Bool

// MarkReady signals that the service has finished initializing and is ready to
// serve traffic. Call this once StartServer has completed successfully.
func MarkReady() {
	ready.Store(true)
}

func init() {
	registryroute.Register(registryroute.Plugin{
		Order: 0,
		Type:  registryroute.RouteTypeManagement,
		Loader: func(r *gin.Engine) error {
			// Liveness: process is up
			r.GET("/health", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"status": "ok"})
			})

			// Readiness: service has finished initializing
			r.GET("/ready", func(c *gin.Context) {
				if ready.Load() {
					c.JSON(http.StatusOK, gin.H{"status": "ready"})
				} else {
					c.JSON(http.StatusServiceUnavailable, gin.H{"status": "starting"})
				}
			})

			// Prometheus metrics
			r.GET("/metrics", gin.WrapH(promhttp.Handler()))

			return nil
		},
	})
}
