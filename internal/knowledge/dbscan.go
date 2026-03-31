package knowledge

import (
	"math"
	"sort"

	"github.com/google/uuid"
)

// ClusterResult represents the output of a single clustering run.
type ClusterResult struct {
	// Clusters maps cluster label (0-based) to the set of member indices.
	Clusters map[int][]int
	// Noise contains indices of points classified as noise by DBSCAN.
	Noise []int
}

// ClusterMember identifies an embedding source.
type ClusterMember struct {
	SourceID   uuid.UUID
	SourceType int // 0=entry, 1=memory
}

// Cluster represents a persisted knowledge cluster with metadata.
type Cluster struct {
	ID          uuid.UUID
	UserID      string
	Label       string
	Keywords    []string
	Centroid    []float64
	MemberCount int
	Trend       int // 0=growing, 1=stable, 2=decaying
	SourceType  int // 0=entries, 1=memories, 2=mixed
	Members     []ClusterMember
}

// DBSCANConfig holds parameters for the DBSCAN algorithm.
type DBSCANConfig struct {
	// Epsilon is the maximum cosine distance between two points to be
	// considered neighbors. Range: 0.0 (identical) to 2.0 (opposite).
	Epsilon float64
	// MinPoints is the minimum number of points required to form a dense region.
	MinPoints int
}

// DefaultDBSCANConfig returns sensible defaults for clustering embeddings.
func DefaultDBSCANConfig() DBSCANConfig {
	return DBSCANConfig{
		Epsilon:   0.3,
		MinPoints: 3,
	}
}

// RunDBSCAN executes the DBSCAN algorithm on the given embeddings using cosine distance.
// Each embedding is a float64 slice. Returns cluster assignments.
func RunDBSCAN(embeddings [][]float64, cfg DBSCANConfig) ClusterResult {
	n := len(embeddings)
	if n == 0 {
		return ClusterResult{Clusters: map[int][]int{}}
	}

	const (
		undefined = -2
		noise     = -1
	)

	labels := make([]int, n)
	for i := range labels {
		labels[i] = undefined
	}

	clusterID := 0
	for i := 0; i < n; i++ {
		if labels[i] != undefined {
			continue
		}

		neighbors := rangeQuery(embeddings, i, cfg.Epsilon)
		if len(neighbors) < cfg.MinPoints {
			labels[i] = noise
			continue
		}

		labels[i] = clusterID

		// Seed set: neighbors minus point i itself.
		seed := make([]int, 0, len(neighbors))
		for _, nb := range neighbors {
			if nb != i {
				seed = append(seed, nb)
			}
		}

		for j := 0; j < len(seed); j++ {
			q := seed[j]
			if labels[q] == noise {
				labels[q] = clusterID
			}
			if labels[q] != undefined {
				continue
			}
			labels[q] = clusterID

			qNeighbors := rangeQuery(embeddings, q, cfg.Epsilon)
			if len(qNeighbors) >= cfg.MinPoints {
				seed = append(seed, qNeighbors...)
			}
		}

		clusterID++
	}

	result := ClusterResult{Clusters: make(map[int][]int)}
	for i, label := range labels {
		if label == noise {
			result.Noise = append(result.Noise, i)
		} else {
			result.Clusters[label] = append(result.Clusters[label], i)
		}
	}
	return result
}

// rangeQuery returns indices of all points within epsilon cosine distance of point at index p.
func rangeQuery(embeddings [][]float64, p int, epsilon float64) []int {
	var neighbors []int
	for i := range embeddings {
		if CosineDistance(embeddings[p], embeddings[i]) <= epsilon {
			neighbors = append(neighbors, i)
		}
	}
	return neighbors
}

// CosineDistance computes 1 - cosine_similarity between two vectors.
// Returns 0.0 for identical directions, 2.0 for opposite directions.
func CosineDistance(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 2.0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 2.0
	}
	sim := dot / (math.Sqrt(normA) * math.Sqrt(normB))
	// Clamp to [-1, 1] to handle floating point errors.
	if sim > 1.0 {
		sim = 1.0
	} else if sim < -1.0 {
		sim = -1.0
	}
	return 1.0 - sim
}

// ComputeCentroid returns the mean vector of the given embeddings.
func ComputeCentroid(embeddings [][]float64, indices []int) []float64 {
	if len(indices) == 0 || len(embeddings) == 0 {
		return nil
	}
	dim := len(embeddings[indices[0]])
	centroid := make([]float64, dim)
	for _, idx := range indices {
		for d := 0; d < dim; d++ {
			centroid[d] += embeddings[idx][d]
		}
	}
	n := float64(len(indices))
	for d := range centroid {
		centroid[d] /= n
	}
	return centroid
}

// SortedClusterLabels returns cluster labels sorted by label number.
func SortedClusterLabels(clusters map[int][]int) []int {
	labels := make([]int, 0, len(clusters))
	for k := range clusters {
		labels = append(labels, k)
	}
	sort.Ints(labels)
	return labels
}
