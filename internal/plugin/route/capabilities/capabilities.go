package capabilities

import (
	"net/http"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/security"
	servicecapabilities "github.com/chirino/memory-service/internal/service/capabilities"
	"github.com/gin-gonic/gin"
)

func HandleGetCapabilities(c *gin.Context, cfg *config.Config) {
	if !hasCapabilitiesAccess(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "client context or admin/auditor role required"})
		return
	}
	c.JSON(http.StatusOK, servicecapabilities.Build(cfg))
}

func hasCapabilitiesAccess(c *gin.Context) bool {
	if security.GetClientID(c) != "" {
		return true
	}
	return security.HasRole(c, security.RoleAdmin) || security.HasRole(c, security.RoleAuditor)
}
