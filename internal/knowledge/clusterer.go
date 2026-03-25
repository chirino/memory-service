package knowledge

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/google/uuid"
)

// Clusterer runs DBSCAN clustering on user embeddings in the background.
type Clusterer struct {
	store         KnowledgeStore
	cfg           DBSCANConfig
	interval      time.Duration
	decay         time.Duration
	keywordsCount int
	mu            sync.Mutex
}

// ClusterRunStats summarizes a single clustering cycle.
type ClusterRunStats struct {
	UsersProcessed  int `json:"users_processed"`
	Clustersborn    int `json:"clusters_born"`
	ClustersUpdated int `json:"clusters_updated"`
	ClustersDied    int `json:"clusters_died"`
	Failures        int `json:"failures"`
}

// NewClusterer creates a new background clusterer.
func NewClusterer(store KnowledgeStore, interval time.Duration, decay time.Duration, keywordsCount int, cfg DBSCANConfig) *Clusterer {
	if keywordsCount <= 0 {
		keywordsCount = 10
	}
	return &Clusterer{
		store:         store,
		cfg:           cfg,
		interval:      interval,
		decay:         decay,
		keywordsCount: keywordsCount,
	}
}

// Start runs the clusterer until ctx is cancelled.
func (c *Clusterer) Start(ctx context.Context) {
	if c == nil || c.store == nil || c.interval <= 0 {
		return
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = c.Trigger(ctx)
		}
	}
}

// Trigger runs one clustering cycle synchronously.
func (c *Clusterer) Trigger(ctx context.Context) (ClusterRunStats, error) {
	if c == nil || c.store == nil {
		return ClusterRunStats{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runOnce(ctx), nil
}

func (c *Clusterer) runOnce(ctx context.Context) ClusterRunStats {
	stats := ClusterRunStats{}

	users, err := c.store.ListUsersWithEmbeddings(ctx)
	if err != nil {
		log.Error("Knowledge clusterer: list users failed", "err", err)
		stats.Failures++
		return stats
	}

	for _, userID := range users {
		born, updated, died, err := c.clusterUser(ctx, userID)
		if err != nil {
			log.Warn("Knowledge clusterer: user clustering failed", "user", userID, "err", err)
			stats.Failures++
			continue
		}
		stats.UsersProcessed++
		stats.Clustersborn += born
		stats.ClustersUpdated += updated
		stats.ClustersDied += died
	}

	if stats.Clustersborn > 0 || stats.ClustersUpdated > 0 || stats.ClustersDied > 0 {
		log.Info("Knowledge clusterer: cycle complete",
			"users", stats.UsersProcessed,
			"born", stats.Clustersborn,
			"updated", stats.ClustersUpdated,
			"died", stats.ClustersDied,
		)
	}

	return stats
}

func (c *Clusterer) clusterUser(ctx context.Context, userID string) (born, updated, died int, err error) {
	records, err := c.store.LoadEmbeddingsForUser(ctx, userID)
	if err != nil {
		return 0, 0, 0, err
	}
	if len(records) == 0 {
		log.Debug("Knowledge clusterer: no embeddings for user", "user", userID)
		return 0, 0, 0, nil
	}

	// Build embedding matrix and source ID list.
	embeddings := make([][]float64, len(records))
	sourceIDs := make([]uuid.UUID, len(records))
	for i, r := range records {
		embeddings[i] = r.Embedding
		sourceIDs[i] = r.SourceID
	}

	// Run DBSCAN.
	result := RunDBSCAN(embeddings, c.cfg)

	// Load existing clusters for diff.
	existing, err := c.store.LoadClustersForUser(ctx, userID)
	if err != nil {
		return 0, 0, 0, err
	}

	// Convert to diff-compatible format.
	existingClusters := make([]Cluster, len(existing))
	for i, sc := range existing {
		members := make([]ClusterMember, len(sc.Members))
		for j, m := range sc.Members {
			members[j] = ClusterMember{SourceID: m.SourceID, SourceType: m.SourceType}
		}
		existingClusters[i] = Cluster{
			ID:      sc.ID,
			Members: members,
		}
	}

	diff := DiffClusters(result, embeddings, sourceIDs, existingClusters)

	// Apply births.
	for _, b := range diff.Born {
		clusterID := uuid.New()
		members := buildMembers(clusterID, b.Members, embeddings, sourceIDs, records, b.Centroid)
		sc := StoredCluster{
			ID:          clusterID,
			UserID:      userID,
			MemberCount: len(b.Members),
			Centroid:    b.Centroid,
			Trend:       0, // growing
			SourceType:  inferSourceType(b.Members, records),
		}
		if err := c.store.SaveCluster(ctx, sc, members); err != nil {
			return born, updated, died, err
		}
		born++
	}

	// Apply updates.
	now := time.Now()
	for _, u := range diff.Updated {
		members := buildMembers(u.ClusterID, u.NewMembers, embeddings, sourceIDs, records, u.NewCentroid)
		trend := computeTrend(existing, u.ClusterID, now, c.decay)
		sc := StoredCluster{
			ID:          u.ClusterID,
			UserID:      userID,
			MemberCount: len(u.NewMembers),
			Centroid:    u.NewCentroid,
			Trend:       trend,
			SourceType:  inferSourceType(u.NewMembers, records),
		}
		if err := c.store.UpdateCluster(ctx, sc, members); err != nil {
			return born, updated, died, err
		}
		updated++
	}

	// Apply deaths.
	for _, deadID := range diff.Died {
		if err := c.store.DeleteCluster(ctx, deadID); err != nil {
			return born, updated, died, err
		}
		died++
	}

	// Extract keywords for all clusters if anything changed.
	if born > 0 || updated > 0 {
		if err := c.refreshKeywords(ctx, userID); err != nil {
			log.Warn("Knowledge clusterer: keyword extraction failed", "user", userID, "err", err)
		}
	}

	return born, updated, died, nil
}

func (c *Clusterer) refreshKeywords(ctx context.Context, userID string) error {
	clusters, err := c.store.LoadClustersForUser(ctx, userID)
	if err != nil {
		return err
	}
	if len(clusters) == 0 {
		return nil
	}

	// Collect all source IDs across all clusters.
	var allSourceIDs []uuid.UUID
	for _, cl := range clusters {
		for _, m := range cl.Members {
			allSourceIDs = append(allSourceIDs, m.SourceID)
		}
	}

	// Load text content for all source IDs.
	texts, err := c.store.LoadTextsForSourceIDs(ctx, allSourceIDs)
	if err != nil {
		return err
	}

	// Build ClusterTexts: concatenate member texts per cluster.
	clusterTexts := make(ClusterTexts, len(clusters))
	for i, cl := range clusters {
		var combined []string
		for _, m := range cl.Members {
			if text, ok := texts[m.SourceID]; ok {
				combined = append(combined, text)
			}
		}
		clusterTexts[i] = strings.Join(combined, " ")
	}

	// Run c-TF-IDF.
	keywordResults := ExtractKeywords(clusterTexts, c.keywordsCount)

	// Update each cluster with keywords and label.
	for _, kr := range keywordResults {
		if kr.ClusterLabel >= len(clusters) {
			continue
		}
		cl := clusters[kr.ClusterLabel]
		cl.Keywords = KeywordStrings(kr.Keywords)
		cl.Label = GenerateLabel(kr.Keywords, 3)
		if err := c.store.UpdateCluster(ctx, cl, nil); err != nil {
			return err
		}
	}

	return nil
}

func buildMembers(clusterID uuid.UUID, indices []int, embeddings [][]float64, sourceIDs []uuid.UUID, records []EmbeddingRecord, centroid []float64) []StoredClusterMember {
	members := make([]StoredClusterMember, 0, len(indices))
	for _, idx := range indices {
		if idx >= len(records) {
			continue
		}
		dist := float32(CosineDistance(embeddings[idx], centroid))
		members = append(members, StoredClusterMember{
			ClusterID:  clusterID,
			SourceID:   sourceIDs[idx],
			SourceType: records[idx].SourceType,
			Distance:   dist,
		})
	}
	return members
}

func inferSourceType(indices []int, records []EmbeddingRecord) int {
	hasEntry, hasMemory := false, false
	for _, idx := range indices {
		if idx >= len(records) {
			continue
		}
		if records[idx].SourceType == 0 {
			hasEntry = true
		} else {
			hasMemory = true
		}
	}
	if hasEntry && hasMemory {
		return 2 // mixed
	}
	if hasMemory {
		return 1
	}
	return 0 // entries
}

func computeTrend(existing []StoredCluster, clusterID uuid.UUID, now time.Time, decay time.Duration) int {
	for _, sc := range existing {
		if sc.ID == clusterID {
			if now.Sub(sc.UpdatedAt) > decay {
				return 2 // decaying
			}
			return 0 // growing (has new members via update)
		}
	}
	return 0
}
