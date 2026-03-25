//go:build !nopostgresql

package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/charmbracelet/log"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// PostgresKnowledgeStore implements KnowledgeStore using GORM + PostgreSQL.
type PostgresKnowledgeStore struct {
	db *gorm.DB
}

// NewPostgresKnowledgeStore creates a new PostgreSQL-backed knowledge store.
func NewPostgresKnowledgeStore(db *gorm.DB) *PostgresKnowledgeStore {
	return &PostgresKnowledgeStore{db: db}
}

// OpenPostgresKnowledgeStore opens a new gorm.DB connection for the knowledge store.
func OpenPostgresKnowledgeStore(dbURL string) (*PostgresKnowledgeStore, error) {
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		return nil, fmt.Errorf("knowledge store: failed to connect to postgres: %w", err)
	}
	return &PostgresKnowledgeStore{db: db}, nil
}

// GORM models for knowledge tables.

type knowledgeClusterRow struct {
	ID          uuid.UUID `gorm:"column:id;type:uuid;primaryKey"`
	UserID      string    `gorm:"column:user_id"`
	Label       string    `gorm:"column:label"`
	Keywords    []byte    `gorm:"column:keywords;type:jsonb"`
	Centroid    []byte    `gorm:"column:centroid"`
	MemberCount int       `gorm:"column:member_count"`
	Trend       int16     `gorm:"column:trend"`
	SourceType  int16     `gorm:"column:source_type"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (knowledgeClusterRow) TableName() string { return "knowledge_clusters" }

type knowledgeClusterMemberRow struct {
	ClusterID  uuid.UUID `gorm:"column:cluster_id;type:uuid"`
	SourceID   uuid.UUID `gorm:"column:source_id;type:uuid"`
	SourceType int16     `gorm:"column:source_type"`
	Distance   float32   `gorm:"column:distance"`
	AssignedAt time.Time `gorm:"column:assigned_at"`
}

func (knowledgeClusterMemberRow) TableName() string { return "knowledge_cluster_members" }

// embeddingRow is used to scan entry embeddings joined with conversation ownership.
type embeddingRow struct {
	SourceID  uuid.UUID `gorm:"column:source_id"`
	UserID    string    `gorm:"column:user_id"`
	Embedding string    `gorm:"column:embedding_text"`
}

func (s *PostgresKnowledgeStore) ListUsersWithEmbeddings(ctx context.Context) ([]string, error) {
	var users []string
	// Find users who own conversations that have indexed entries.
	err := s.db.WithContext(ctx).Raw(`
		SELECT DISTINCT cm.user_id
		FROM entry_embeddings ee
		JOIN conversations c ON c.id = ee.conversation_id
		JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id
		WHERE cm.access_level = 'owner'
	`).Scan(&users).Error
	return users, err
}

func (s *PostgresKnowledgeStore) LoadEmbeddingsForUser(ctx context.Context, userID string) ([]EmbeddingRecord, error) {
	var rows []embeddingRow
	err := s.db.WithContext(ctx).Raw(`
		SELECT ee.entry_id AS source_id,
		       cm.user_id AS user_id,
		       ee.embedding::text AS embedding_text
		FROM entry_embeddings ee
		JOIN conversations c ON c.id = ee.conversation_id
		JOIN conversation_memberships cm ON cm.conversation_group_id = c.conversation_group_id
		WHERE cm.access_level = 'owner' AND cm.user_id = ?
	`, userID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	records := make([]EmbeddingRecord, 0, len(rows))
	parseFailures := 0
	for _, r := range rows {
		emb := parseEmbeddingText([]byte(r.Embedding))
		if len(emb) > 0 {
			records = append(records, EmbeddingRecord{
				SourceID:   r.SourceID,
				SourceType: 0, // entry
				UserID:     r.UserID,
				Embedding:  emb,
			})
		} else {
			parseFailures++
		}
	}
	if parseFailures > 0 {
		log.Warn("Knowledge store: failed to parse embeddings", "user", userID, "failures", parseFailures, "total", len(rows))
	}
	return records, nil
}

func (s *PostgresKnowledgeStore) LoadClustersForUser(ctx context.Context, userID string) ([]StoredCluster, error) {
	var clusterRows []knowledgeClusterRow
	q := s.db.WithContext(ctx)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if err := q.Find(&clusterRows).Error; err != nil {
		return nil, err
	}

	clusters := make([]StoredCluster, 0, len(clusterRows))
	for _, row := range clusterRows {
		var members []knowledgeClusterMemberRow
		if err := s.db.WithContext(ctx).Where("cluster_id = ?", row.ID).Find(&members).Error; err != nil {
			return nil, err
		}

		sc := StoredCluster{
			ID:          row.ID,
			UserID:      row.UserID,
			Label:       row.Label,
			Keywords:    decodeJSONStringSlice(row.Keywords),
			Centroid:    decodeFloat64Slice(row.Centroid),
			MemberCount: row.MemberCount,
			Trend:       int(row.Trend),
			SourceType:  int(row.SourceType),
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		}
		for _, m := range members {
			sc.Members = append(sc.Members, StoredClusterMember{
				ClusterID:  m.ClusterID,
				SourceID:   m.SourceID,
				SourceType: int(m.SourceType),
				Distance:   m.Distance,
			})
		}
		clusters = append(clusters, sc)
	}
	return clusters, nil
}

func (s *PostgresKnowledgeStore) SaveCluster(ctx context.Context, cluster StoredCluster, members []StoredClusterMember) error {
	now := time.Now()
	row := knowledgeClusterRow{
		ID:          cluster.ID,
		UserID:      cluster.UserID,
		Label:       cluster.Label,
		Keywords:    encodeJSONStringSlice(cluster.Keywords),
		Centroid:    encodeFloat64Slice(cluster.Centroid),
		MemberCount: cluster.MemberCount,
		Trend:       int16(cluster.Trend),
		SourceType:  int16(cluster.SourceType),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	return s.insertMembers(ctx, cluster.ID, members, now)
}

func (s *PostgresKnowledgeStore) UpdateCluster(ctx context.Context, cluster StoredCluster, members []StoredClusterMember) error {
	now := time.Now()
	updates := map[string]interface{}{
		"label":        cluster.Label,
		"keywords":     encodeJSONStringSlice(cluster.Keywords),
		"centroid":     encodeFloat64Slice(cluster.Centroid),
		"member_count": cluster.MemberCount,
		"trend":        int16(cluster.Trend),
		"source_type":  int16(cluster.SourceType),
		"updated_at":   now,
	}
	if err := s.db.WithContext(ctx).Model(&knowledgeClusterRow{}).Where("id = ?", cluster.ID).Updates(updates).Error; err != nil {
		return err
	}
	// Replace members only if provided (nil = keyword-only update).
	if members != nil {
		if err := s.db.WithContext(ctx).Where("cluster_id = ?", cluster.ID).Delete(&knowledgeClusterMemberRow{}).Error; err != nil {
			return err
		}
		return s.insertMembers(ctx, cluster.ID, members, now)
	}
	return nil
}

func (s *PostgresKnowledgeStore) DeleteCluster(ctx context.Context, clusterID uuid.UUID) error {
	// Members cascade-deleted via FK.
	return s.db.WithContext(ctx).Where("id = ?", clusterID).Delete(&knowledgeClusterRow{}).Error
}

func (s *PostgresKnowledgeStore) LoadTextsForSourceIDs(ctx context.Context, sourceIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}

	type textRow struct {
		ID   uuid.UUID `gorm:"column:id"`
		Text *string   `gorm:"column:indexed_content"`
	}
	var rows []textRow
	err := s.db.WithContext(ctx).Raw(`
		SELECT id, indexed_content
		FROM entries
		WHERE id = ANY(?)
		AND indexed_content IS NOT NULL
	`, sourceIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		if r.Text != nil && *r.Text != "" {
			result[r.ID] = *r.Text
		}
	}
	return result, nil
}

func (s *PostgresKnowledgeStore) insertMembers(ctx context.Context, clusterID uuid.UUID, members []StoredClusterMember, now time.Time) error {
	if len(members) == 0 {
		return nil
	}
	rows := make([]knowledgeClusterMemberRow, len(members))
	for i, m := range members {
		rows[i] = knowledgeClusterMemberRow{
			ClusterID:  clusterID,
			SourceID:   m.SourceID,
			SourceType: int16(m.SourceType),
			Distance:   m.Distance,
			AssignedAt: now,
		}
	}
	return s.db.WithContext(ctx).Create(&rows).Error
}

// Serialization helpers.

func encodeJSONStringSlice(s []string) []byte {
	if s == nil {
		s = []string{}
	}
	b, _ := json.Marshal(s)
	return b
}

func decodeJSONStringSlice(b []byte) []string {
	var s []string
	if len(b) > 0 {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func encodeFloat64Slice(v []float64) []byte {
	if len(v) == 0 {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func decodeFloat64Slice(b []byte) []float64 {
	if len(b) == 0 {
		return nil
	}
	var v []float64
	_ = json.Unmarshal(b, &v)
	return v
}

// parseEmbeddingText parses a pgvector text representation like "[0.1,0.2,0.3]" into []float64.
func parseEmbeddingText(b []byte) []float64 {
	if len(b) == 0 {
		return nil
	}
	var f32 []float32
	if err := json.Unmarshal(b, &f32); err != nil {
		// Try float64 directly.
		var f64 []float64
		if err2 := json.Unmarshal(b, &f64); err2 != nil {
			return nil
		}
		return f64
	}
	result := make([]float64, len(f32))
	for i, v := range f32 {
		result[i] = float64(math.Float32frombits(math.Float32bits(v)))
	}
	return result
}
