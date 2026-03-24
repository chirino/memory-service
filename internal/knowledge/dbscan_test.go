package knowledge

import (
	"math"
	"testing"
)

func TestCosineDistance_IdenticalVectors(t *testing.T) {
	a := []float64{1, 2, 3}
	d := CosineDistance(a, a)
	if d > 1e-10 {
		t.Errorf("expected ~0 for identical vectors, got %f", d)
	}
}

func TestCosineDistance_OrthogonalVectors(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{0, 1, 0}
	d := CosineDistance(a, b)
	if math.Abs(d-1.0) > 1e-10 {
		t.Errorf("expected ~1.0 for orthogonal vectors, got %f", d)
	}
}

func TestCosineDistance_OppositeVectors(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{-1, 0, 0}
	d := CosineDistance(a, b)
	if math.Abs(d-2.0) > 1e-10 {
		t.Errorf("expected ~2.0 for opposite vectors, got %f", d)
	}
}

func TestCosineDistance_EmptyVectors(t *testing.T) {
	d := CosineDistance([]float64{}, []float64{})
	if d != 2.0 {
		t.Errorf("expected 2.0 for empty vectors, got %f", d)
	}
}

func TestCosineDistance_DifferentLengths(t *testing.T) {
	d := CosineDistance([]float64{1, 2}, []float64{1, 2, 3})
	if d != 2.0 {
		t.Errorf("expected 2.0 for mismatched lengths, got %f", d)
	}
}

func TestDBSCAN_FormsClusters(t *testing.T) {
	// Two tight groups of 3 points each, well separated.
	embeddings := [][]float64{
		// Cluster A: near [1, 0, 0]
		{1, 0.01, 0},
		{1, -0.01, 0},
		{1, 0, 0.01},
		// Cluster B: near [0, 1, 0]
		{0, 1, 0.01},
		{0, 1, -0.01},
		{0.01, 1, 0},
	}

	cfg := DBSCANConfig{Epsilon: 0.1, MinPoints: 3}
	result := RunDBSCAN(embeddings, cfg)

	if len(result.Clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(result.Clusters))
	}
	if len(result.Noise) != 0 {
		t.Errorf("expected 0 noise points, got %d", len(result.Noise))
	}

	// Verify each cluster has exactly 3 members.
	for label, members := range result.Clusters {
		if len(members) != 3 {
			t.Errorf("cluster %d: expected 3 members, got %d", label, len(members))
		}
	}
}

func TestDBSCAN_Noise(t *testing.T) {
	// One tight cluster and one isolated point.
	embeddings := [][]float64{
		{1, 0, 0},
		{1, 0.01, 0},
		{1, 0, 0.01},
		{0, 0, 1}, // isolated
	}

	cfg := DBSCANConfig{Epsilon: 0.1, MinPoints: 3}
	result := RunDBSCAN(embeddings, cfg)

	if len(result.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(result.Clusters))
	}
	if len(result.Noise) != 1 {
		t.Errorf("expected 1 noise point, got %d", len(result.Noise))
	}
	if result.Noise[0] != 3 {
		t.Errorf("expected index 3 as noise, got %d", result.Noise[0])
	}
}

func TestDBSCAN_AllNoise(t *testing.T) {
	// All points far apart, none form a cluster.
	embeddings := [][]float64{
		{1, 0, 0},
		{0, 1, 0},
		{0, 0, 1},
	}

	cfg := DBSCANConfig{Epsilon: 0.01, MinPoints: 3}
	result := RunDBSCAN(embeddings, cfg)

	if len(result.Clusters) != 0 {
		t.Errorf("expected 0 clusters, got %d", len(result.Clusters))
	}
	if len(result.Noise) != 3 {
		t.Errorf("expected 3 noise points, got %d", len(result.Noise))
	}
}

func TestDBSCAN_SingleCluster(t *testing.T) {
	// All points close together.
	embeddings := [][]float64{
		{1, 0.1, 0.1},
		{1, 0.2, 0.1},
		{1, 0.1, 0.2},
		{1, 0.15, 0.15},
	}

	cfg := DBSCANConfig{Epsilon: 0.2, MinPoints: 3}
	result := RunDBSCAN(embeddings, cfg)

	if len(result.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(result.Clusters))
	}
	if len(result.Clusters[0]) != 4 {
		t.Errorf("expected 4 members, got %d", len(result.Clusters[0]))
	}
}

func TestDBSCAN_Empty(t *testing.T) {
	result := RunDBSCAN(nil, DefaultDBSCANConfig())
	if len(result.Clusters) != 0 {
		t.Errorf("expected 0 clusters for empty input, got %d", len(result.Clusters))
	}
}

func TestComputeCentroid(t *testing.T) {
	embeddings := [][]float64{
		{2, 4, 6},
		{4, 6, 8},
	}
	centroid := ComputeCentroid(embeddings, []int{0, 1})
	expected := []float64{3, 5, 7}
	for i, v := range centroid {
		if math.Abs(v-expected[i]) > 1e-10 {
			t.Errorf("centroid[%d]: expected %f, got %f", i, expected[i], v)
		}
	}
}

func TestComputeCentroid_Empty(t *testing.T) {
	centroid := ComputeCentroid(nil, nil)
	if centroid != nil {
		t.Errorf("expected nil centroid for empty input, got %v", centroid)
	}
}
