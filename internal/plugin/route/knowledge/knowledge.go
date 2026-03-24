package knowledge

import (
	"net/http"
	"time"

	knowledgepkg "github.com/chirino/memory-service/internal/knowledge"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler serves knowledge cluster REST endpoints.
type Handler struct {
	Store     knowledgepkg.KnowledgeStore
	Clusterer *knowledgepkg.Clusterer
}

type clusterResponse struct {
	ID          uuid.UUID `json:"id"`
	Label       string    `json:"label"`
	Keywords    []string  `json:"keywords"`
	MemberCount int       `json:"member_count"`
	Trend       string    `json:"trend"`
	SourceType  string    `json:"source_type"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RegisterRoutes adds knowledge endpoints to the router.
func (h *Handler) RegisterRoutes(router *gin.Engine, authMiddleware gin.HandlerFunc) {
	g := router.Group("/v1/knowledge", authMiddleware)
	g.GET("/clusters", h.listClusters)

	admin := router.Group("/admin/v1/knowledge", authMiddleware)
	admin.POST("/trigger", h.triggerClustering)
}

func (h *Handler) listClusters(c *gin.Context) {
	userID := security.GetUserID(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	clusters, err := h.Store.LoadClustersForUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load clusters"})
		return
	}

	result := make([]clusterResponse, 0, len(clusters))
	for _, cl := range clusters {
		result = append(result, clusterResponse{
			ID:          cl.ID,
			Label:       cl.Label,
			Keywords:    cl.Keywords,
			MemberCount: cl.MemberCount,
			Trend:       trendName(cl.Trend),
			SourceType:  sourceTypeName(cl.SourceType),
			CreatedAt:   cl.CreatedAt,
			UpdatedAt:   cl.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"clusters": result})
}

func (h *Handler) triggerClustering(c *gin.Context) {
	if h.Clusterer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "clustering not enabled"})
		return
	}

	stats, err := h.Clusterer.Trigger(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "clustering failed"})
		return
	}

	c.JSON(http.StatusOK, stats)
}

func trendName(trend int) string {
	switch trend {
	case 0:
		return "growing"
	case 1:
		return "stable"
	case 2:
		return "decaying"
	default:
		return "unknown"
	}
}

func sourceTypeName(st int) string {
	switch st {
	case 0:
		return "entries"
	case 1:
		return "memories"
	case 2:
		return "mixed"
	default:
		return "unknown"
	}
}
