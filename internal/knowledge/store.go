package knowledge

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// EmbeddingRecord represents a stored embedding that the clustering goroutine reads.
type EmbeddingRecord struct {
	SourceID   uuid.UUID // entry_id or memory_id
	SourceType int       // 0=entry, 1=memory
	UserID     string    // owner of the data
	Embedding  []float64
}

// StoredCluster is a cluster as persisted in the database.
type StoredCluster struct {
	ID          uuid.UUID
	UserID      string
	Label       string
	Keywords    []string
	Centroid    []float64
	MemberCount int
	Trend       int // 0=growing, 1=stable, 2=decaying
	SourceType  int // 0=entries, 1=memories, 2=mixed
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Members     []StoredClusterMember
}

// StoredClusterMember is a cluster membership row.
type StoredClusterMember struct {
	ClusterID  uuid.UUID
	SourceID   uuid.UUID
	SourceType int
	Distance   float32
}

// KnowledgeStore defines the persistence interface for the clustering goroutine.
type KnowledgeStore interface {
	// ListUsersWithEmbeddings returns distinct user IDs that have embeddings.
	ListUsersWithEmbeddings(ctx context.Context) ([]string, error)

	// LoadEmbeddingsForUser returns all embeddings belonging to a user.
	LoadEmbeddingsForUser(ctx context.Context, userID string) ([]EmbeddingRecord, error)

	// LoadClustersForUser returns all stored clusters (with members) for a user.
	LoadClustersForUser(ctx context.Context, userID string) ([]StoredCluster, error)

	// SaveCluster creates a new cluster with its members.
	SaveCluster(ctx context.Context, cluster StoredCluster, members []StoredClusterMember) error

	// UpdateCluster updates an existing cluster's metadata and replaces its members.
	UpdateCluster(ctx context.Context, cluster StoredCluster, members []StoredClusterMember) error

	// DeleteCluster removes a cluster and its members.
	DeleteCluster(ctx context.Context, clusterID uuid.UUID) error

	// LoadTextsForSourceIDs returns the indexed text content for the given source IDs.
	// Used for c-TF-IDF keyword extraction after clustering.
	LoadTextsForSourceIDs(ctx context.Context, sourceIDs []uuid.UUID) (map[uuid.UUID]string, error)

	// ResolveOwnersByConversationGroupIDs returns distinct owner user IDs for the
	// given conversation group IDs. Used by the indexer to determine which users
	// need re-clustering after new embeddings are created.
	ResolveOwnersByConversationGroupIDs(ctx context.Context, groupIDs []uuid.UUID) ([]string, error)
}
