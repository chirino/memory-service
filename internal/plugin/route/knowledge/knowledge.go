package knowledge

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	knowledgepkg "github.com/chirino/memory-service/internal/knowledge"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler serves knowledge cluster admin endpoints.
type Handler struct {
	Store     knowledgepkg.KnowledgeStore
	Clusterer *knowledgepkg.Clusterer
}

type clusterResponse struct {
	ID          uuid.UUID `json:"id"`
	UserID      string    `json:"user_id"`
	Label       string    `json:"label"`
	Keywords    []string  `json:"keywords"`
	MemberCount int       `json:"member_count"`
	Trend       string    `json:"trend"`
	SourceType  string    `json:"source_type"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type clusterDetailResponse struct {
	clusterResponse
	Members            []memberResponse  `json:"members"`
	RepresentativeTexts map[string]string `json:"representative_texts"`
}

type memberResponse struct {
	SourceID   uuid.UUID `json:"source_id"`
	SourceType string    `json:"source_type"`
	Distance   float32   `json:"distance"`
}

// RegisterRoutes adds knowledge admin endpoints to the router.
func (h *Handler) RegisterRoutes(router *gin.Engine, authMiddleware gin.HandlerFunc) {
	admin := router.Group("/admin/v1/knowledge", authMiddleware, security.RequireAdminRole())
	admin.GET("/clusters", h.listClusters)
	admin.GET("/clusters/:id", h.getCluster)
	admin.POST("/trigger", h.triggerClustering)
}

func (h *Handler) listClusters(c *gin.Context) {
	userID := c.Query("user_id")

	var clusters []knowledgepkg.StoredCluster
	var err error
	if userID != "" {
		clusters, err = h.Store.LoadClustersForUser(c.Request.Context(), userID)
	} else {
		clusters, err = h.Store.LoadClustersForUser(c.Request.Context(), "")
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load clusters"})
		return
	}

	result := make([]clusterResponse, 0, len(clusters))
	for _, cl := range clusters {
		result = append(result, clusterResponse{
			ID:          cl.ID,
			UserID:      cl.UserID,
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

func (h *Handler) getCluster(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cluster id"})
		return
	}

	topN := 5
	if v := c.Query("representative_count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			topN = n
		}
	}

	cluster, err := h.Store.LoadClusterByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load cluster"})
		return
	}
	if cluster == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cluster not found"})
		return
	}

	members := make([]memberResponse, 0, len(cluster.Members))
	for _, m := range cluster.Members {
		members = append(members, memberResponse{
			SourceID:   m.SourceID,
			SourceType: sourceTypeName(m.SourceType),
			Distance:   m.Distance,
		})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Distance < members[j].Distance })

	representativeTexts := make(map[string]string)
	if len(cluster.Members) > 0 {
		sorted := make([]knowledgepkg.StoredClusterMember, len(cluster.Members))
		copy(sorted, cluster.Members)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Distance < sorted[j].Distance })
		if len(sorted) > topN {
			sorted = sorted[:topN]
		}
		sourceIDs := make([]uuid.UUID, len(sorted))
		for i, m := range sorted {
			sourceIDs[i] = m.SourceID
		}
		texts, err := h.Store.LoadTextsForSourceIDs(c.Request.Context(), sourceIDs)
		if err == nil {
			for id, text := range texts {
				representativeTexts[id.String()] = text
			}
		}
	}

	c.JSON(http.StatusOK, clusterDetailResponse{
		clusterResponse: clusterResponse{
			ID:          cluster.ID,
			UserID:      cluster.UserID,
			Label:       cluster.Label,
			Keywords:    cluster.Keywords,
			MemberCount: cluster.MemberCount,
			Trend:       trendName(cluster.Trend),
			SourceType:  sourceTypeName(cluster.SourceType),
			CreatedAt:   cluster.CreatedAt,
			UpdatedAt:   cluster.UpdatedAt,
		},
		Members:            members,
		RepresentativeTexts: representativeTexts,
	})
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
