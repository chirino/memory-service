//go:build !nomongo

package mongo

import (
	"context"
	"fmt"
	"time"

	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *mongoEpisodicStore) AdminStatsSummary(ctx context.Context) (*registryepisodic.AdminStatsSummary, error) {
	memoriesTotal, err := s.col.CountDocuments(ctx, bson.M{"archived_at": bson.M{"$exists": false}})
	if err != nil {
		return nil, fmt.Errorf("count active memories: %w", err)
	}
	memoriesArchived, err := s.col.CountDocuments(ctx, bson.M{"archived_at": bson.M{"$exists": true}})
	if err != nil {
		return nil, fmt.Errorf("count archived memories: %w", err)
	}
	findOldestArchived := s.col.FindOne(ctx, bson.M{"archived_at": bson.M{"$exists": true}}, options.FindOne().SetSort(bson.D{{Key: "archived_at", Value: 1}, {Key: "_id", Value: 1}}))
	var oldestDeleted struct {
		ArchivedAt time.Time `bson:"archived_at"`
	}
	var oldestArchivedAt *time.Time
	if err := findOldestArchived.Decode(&oldestDeleted); err == nil {
		t := oldestDeleted.ArchivedAt.UTC()
		oldestArchivedAt = &t
	} else if err != nil && err != mongo.ErrNoDocuments {
		return nil, fmt.Errorf("load oldest archived memory: %w", err)
	}

	return &registryepisodic.AdminStatsSummary{
		Memories: registryepisodic.AdminMemoryStats{
			Total:            memoriesTotal,
			Archived:         memoriesArchived,
			OldestArchivedAt: oldestArchivedAt,
		},
	}, nil
}

var _ registryepisodic.AdminStatsSummaryProvider = (*mongoEpisodicStore)(nil)
