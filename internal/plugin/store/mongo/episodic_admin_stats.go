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
	memoriesTotal, err := s.col.CountDocuments(ctx, bson.M{"deleted_at": bson.M{"$exists": false}})
	if err != nil {
		return nil, fmt.Errorf("count active memories: %w", err)
	}
	memoriesSoftDeleted, err := s.col.CountDocuments(ctx, bson.M{"deleted_at": bson.M{"$exists": true}})
	if err != nil {
		return nil, fmt.Errorf("count soft-deleted memories: %w", err)
	}
	findOldestSoftDeleted := s.col.FindOne(ctx, bson.M{"deleted_at": bson.M{"$exists": true}}, options.FindOne().SetSort(bson.D{{Key: "deleted_at", Value: 1}, {Key: "_id", Value: 1}}))
	var oldestDeleted struct {
		DeletedAt time.Time `bson:"deleted_at"`
	}
	var oldestSoftDeletedAt *time.Time
	if err := findOldestSoftDeleted.Decode(&oldestDeleted); err == nil {
		t := oldestDeleted.DeletedAt.UTC()
		oldestSoftDeletedAt = &t
	} else if err != nil && err != mongo.ErrNoDocuments {
		return nil, fmt.Errorf("load oldest soft-deleted memory: %w", err)
	}

	return &registryepisodic.AdminStatsSummary{
		Memories: registryepisodic.AdminMemoryStats{
			Total:               memoriesTotal,
			SoftDeleted:         memoriesSoftDeleted,
			OldestSoftDeletedAt: oldestSoftDeletedAt,
		},
	}, nil
}

var _ registryepisodic.AdminStatsSummaryProvider = (*mongoEpisodicStore)(nil)
