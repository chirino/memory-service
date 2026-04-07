package knowledge

import "github.com/google/uuid"

// DiffResult describes the changes between previous clusters and a new DBSCAN run.
type DiffResult struct {
	// Updated clusters: existing cluster matched to new DBSCAN output.
	Updated []ClusterUpdate
	// Born clusters: new DBSCAN clusters that don't match any existing cluster.
	Born []ClusterBirth
	// Died clusters: existing clusters with no matching new DBSCAN cluster.
	Died []uuid.UUID
}

// ClusterUpdate represents an existing cluster that matched a new DBSCAN cluster.
type ClusterUpdate struct {
	ClusterID    uuid.UUID
	NewMembers   []int // indices in the new embedding set
	NewCentroid  []float64
	OverlapRatio float64 // fraction of new cluster members that were in the old cluster
}

// ClusterBirth represents a new cluster discovered by DBSCAN.
type ClusterBirth struct {
	Members  []int // indices in the new embedding set
	Centroid []float64
}

// DiffClusters compares a new DBSCAN ClusterResult against existing clusters
// and determines which clusters are updated, born, or died.
//
// members maps each existing cluster ID to the set of source IDs that belonged to it.
// sourceIDs is the ordered list of source IDs corresponding to the embeddings
// passed to DBSCAN (i.e., sourceIDs[i] is the source of embeddings[i]).
// embeddings are the vectors used in the current DBSCAN run.
//
// Matching uses majority-member overlap: a new DBSCAN cluster is matched to
// the existing cluster that shares the highest fraction of members, provided
// that fraction exceeds 0.5 (majority). Each existing cluster is matched at
// most once (best match wins).
func DiffClusters(
	newResult ClusterResult,
	embeddings [][]float64,
	sourceIDs []uuid.UUID,
	existingClusters []Cluster,
) DiffResult {
	// Build lookup: sourceID -> set of existing cluster IDs it belongs to.
	sourceToCluster := make(map[uuid.UUID]uuid.UUID)
	for _, ec := range existingClusters {
		for _, m := range ec.Members {
			sourceToCluster[m.SourceID] = ec.ID
		}
	}

	// For each new DBSCAN cluster, count how many of its members came from each existing cluster.
	var overlaps []clusterOverlap
	for label, indices := range newResult.Clusters {
		counts := make(map[uuid.UUID]int)
		for _, idx := range indices {
			if idx < len(sourceIDs) {
				if ecID, ok := sourceToCluster[sourceIDs[idx]]; ok {
					counts[ecID]++
				}
			}
		}
		for ecID, count := range counts {
			overlaps = append(overlaps, clusterOverlap{
				newLabel:     label,
				existingID:   ecID,
				overlapCount: count,
				newSize:      len(indices),
			})
		}
	}

	// Sort overlaps by overlap ratio descending to give best matches priority.
	sortOverlaps(overlaps)

	// Greedy matching: each existing cluster and each new cluster matched at most once.
	matchedExisting := make(map[uuid.UUID]bool)
	matchedNew := make(map[int]bool)
	var diff DiffResult

	for _, ov := range overlaps {
		if matchedExisting[ov.existingID] || matchedNew[ov.newLabel] {
			continue
		}
		ratio := float64(ov.overlapCount) / float64(ov.newSize)
		if ratio < 0.5 {
			continue
		}
		matchedExisting[ov.existingID] = true
		matchedNew[ov.newLabel] = true
		diff.Updated = append(diff.Updated, ClusterUpdate{
			ClusterID:    ov.existingID,
			NewMembers:   newResult.Clusters[ov.newLabel],
			NewCentroid:  ComputeCentroid(embeddings, newResult.Clusters[ov.newLabel]),
			OverlapRatio: ratio,
		})
	}

	// New DBSCAN clusters that didn't match any existing cluster are births.
	for label, indices := range newResult.Clusters {
		if !matchedNew[label] {
			diff.Born = append(diff.Born, ClusterBirth{
				Members:  indices,
				Centroid: ComputeCentroid(embeddings, indices),
			})
		}
	}

	// Existing clusters that didn't match any new DBSCAN cluster are deaths.
	for _, ec := range existingClusters {
		if !matchedExisting[ec.ID] {
			diff.Died = append(diff.Died, ec.ID)
		}
	}

	return diff
}

func sortOverlaps(overlaps []clusterOverlap) {
	// Sort by overlap ratio descending, then by overlap count descending for ties.
	for i := 1; i < len(overlaps); i++ {
		for j := i; j > 0; j-- {
			ri := float64(overlaps[j].overlapCount) / float64(overlaps[j].newSize)
			rj := float64(overlaps[j-1].overlapCount) / float64(overlaps[j-1].newSize)
			if ri > rj || (ri == rj && overlaps[j].overlapCount > overlaps[j-1].overlapCount) {
				overlaps[j], overlaps[j-1] = overlaps[j-1], overlaps[j]
			} else {
				break
			}
		}
	}
}

type clusterOverlap struct {
	newLabel     int
	existingID   uuid.UUID
	overlapCount int
	newSize      int
}
