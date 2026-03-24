package knowledge

import (
	"testing"

	"github.com/google/uuid"
)

func TestDiffClusters_MatchesByOverlap(t *testing.T) {
	existingID := uuid.New()
	// 5 source IDs, 3 of which were in the existing cluster.
	sourceIDs := make([]uuid.UUID, 5)
	for i := range sourceIDs {
		sourceIDs[i] = uuid.New()
	}

	existing := []Cluster{
		{
			ID: existingID,
			Members: []ClusterMember{
				{SourceID: sourceIDs[0], SourceType: 0},
				{SourceID: sourceIDs[1], SourceType: 0},
				{SourceID: sourceIDs[2], SourceType: 0},
			},
		},
	}

	embeddings := make([][]float64, 5)
	for i := range embeddings {
		embeddings[i] = []float64{float64(i), 0, 0}
	}

	// New DBSCAN cluster contains indices 0,1,2,3 — 3 out of 4 overlap with existing.
	newResult := ClusterResult{
		Clusters: map[int][]int{
			0: {0, 1, 2, 3},
		},
	}

	diff := DiffClusters(newResult, embeddings, sourceIDs, existing)

	if len(diff.Updated) != 1 {
		t.Fatalf("expected 1 updated cluster, got %d", len(diff.Updated))
	}
	if diff.Updated[0].ClusterID != existingID {
		t.Errorf("expected updated cluster ID %s, got %s", existingID, diff.Updated[0].ClusterID)
	}
	if diff.Updated[0].OverlapRatio != 0.75 {
		t.Errorf("expected overlap ratio 0.75, got %f", diff.Updated[0].OverlapRatio)
	}
	if len(diff.Born) != 0 {
		t.Errorf("expected 0 births, got %d", len(diff.Born))
	}
	if len(diff.Died) != 0 {
		t.Errorf("expected 0 deaths, got %d", len(diff.Died))
	}
}

func TestDiffClusters_DetectsNewCluster(t *testing.T) {
	sourceIDs := make([]uuid.UUID, 3)
	for i := range sourceIDs {
		sourceIDs[i] = uuid.New()
	}

	embeddings := make([][]float64, 3)
	for i := range embeddings {
		embeddings[i] = []float64{float64(i), 0, 0}
	}

	// No existing clusters.
	newResult := ClusterResult{
		Clusters: map[int][]int{
			0: {0, 1, 2},
		},
	}

	diff := DiffClusters(newResult, embeddings, sourceIDs, nil)

	if len(diff.Born) != 1 {
		t.Fatalf("expected 1 birth, got %d", len(diff.Born))
	}
	if len(diff.Born[0].Members) != 3 {
		t.Errorf("expected 3 members in born cluster, got %d", len(diff.Born[0].Members))
	}
	if len(diff.Updated) != 0 {
		t.Errorf("expected 0 updates, got %d", len(diff.Updated))
	}
}

func TestDiffClusters_DetectsDeath(t *testing.T) {
	existingID := uuid.New()
	sourceIDs := make([]uuid.UUID, 3)
	for i := range sourceIDs {
		sourceIDs[i] = uuid.New()
	}

	existing := []Cluster{
		{
			ID: existingID,
			Members: []ClusterMember{
				{SourceID: sourceIDs[0], SourceType: 0},
				{SourceID: sourceIDs[1], SourceType: 0},
				{SourceID: sourceIDs[2], SourceType: 0},
			},
		},
	}

	// DBSCAN found no clusters (all noise).
	newResult := ClusterResult{
		Clusters: map[int][]int{},
	}

	diff := DiffClusters(newResult, nil, sourceIDs, existing)

	if len(diff.Died) != 1 {
		t.Fatalf("expected 1 death, got %d", len(diff.Died))
	}
	if diff.Died[0] != existingID {
		t.Errorf("expected dead cluster ID %s, got %s", existingID, diff.Died[0])
	}
}

func TestDiffClusters_DetectsMerge(t *testing.T) {
	existingA := uuid.New()
	existingB := uuid.New()

	sourceIDs := make([]uuid.UUID, 6)
	for i := range sourceIDs {
		sourceIDs[i] = uuid.New()
	}

	existing := []Cluster{
		{
			ID: existingA,
			Members: []ClusterMember{
				{SourceID: sourceIDs[0], SourceType: 0},
				{SourceID: sourceIDs[1], SourceType: 0},
				{SourceID: sourceIDs[2], SourceType: 0},
			},
		},
		{
			ID: existingB,
			Members: []ClusterMember{
				{SourceID: sourceIDs[3], SourceType: 0},
				{SourceID: sourceIDs[4], SourceType: 0},
				{SourceID: sourceIDs[5], SourceType: 0},
			},
		},
	}

	embeddings := make([][]float64, 6)
	for i := range embeddings {
		embeddings[i] = []float64{float64(i), 0, 0}
	}

	// DBSCAN merged both into one cluster.
	newResult := ClusterResult{
		Clusters: map[int][]int{
			0: {0, 1, 2, 3, 4, 5},
		},
	}

	diff := DiffClusters(newResult, embeddings, sourceIDs, existing)

	// One cluster matches (the one with higher overlap ratio = 3/6 = 0.5),
	// the other dies. Since both have exactly 0.5 overlap, one is matched and one dies.
	if len(diff.Updated) != 1 {
		t.Fatalf("expected 1 updated cluster, got %d", len(diff.Updated))
	}
	if len(diff.Died) != 1 {
		t.Fatalf("expected 1 died cluster, got %d", len(diff.Died))
	}
}

func TestDiffClusters_LowOverlapBirth(t *testing.T) {
	existingID := uuid.New()
	sourceIDs := make([]uuid.UUID, 5)
	for i := range sourceIDs {
		sourceIDs[i] = uuid.New()
	}

	existing := []Cluster{
		{
			ID: existingID,
			Members: []ClusterMember{
				{SourceID: sourceIDs[0], SourceType: 0},
			},
		},
	}

	embeddings := make([][]float64, 5)
	for i := range embeddings {
		embeddings[i] = []float64{float64(i), 0, 0}
	}

	// New cluster has 5 members, only 1 overlaps with existing (ratio = 0.2 < 0.5).
	newResult := ClusterResult{
		Clusters: map[int][]int{
			0: {0, 1, 2, 3, 4},
		},
	}

	diff := DiffClusters(newResult, embeddings, sourceIDs, existing)

	// Below threshold — treated as birth + death, not update.
	if len(diff.Born) != 1 {
		t.Errorf("expected 1 birth (low overlap), got %d", len(diff.Born))
	}
	if len(diff.Died) != 1 {
		t.Errorf("expected 1 death (low overlap), got %d", len(diff.Died))
	}
	if len(diff.Updated) != 0 {
		t.Errorf("expected 0 updates (low overlap), got %d", len(diff.Updated))
	}
}
