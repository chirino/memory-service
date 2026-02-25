package bdd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoTestDB implements cucumber.TestDB for MongoDB.
type MongoTestDB struct {
	DBURL string
}

var _ cucumber.TestDB = (*MongoTestDB)(nil)

func (m *MongoTestDB) db(ctx context.Context) (*mongo.Client, *mongo.Database, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(m.DBURL))
	if err != nil {
		return nil, nil, fmt.Errorf("mongo connect: %w", err)
	}
	return client, client.Database("memory_service"), nil
}

func (m *MongoTestDB) ClearAll(ctx context.Context) error {
	client, db, err := m.db(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)

	collections := []string{
		"tasks",
		"entries",
		"attachment_file_chunks",
		"attachments",
		"conversation_ownership_transfers",
		"conversation_memberships",
		"conversations",
		"conversation_groups",
	}
	for _, coll := range collections {
		if _, err := db.Collection(coll).DeleteMany(ctx, bson.M{}); err != nil {
			return fmt.Errorf("cleanup: failed to clear %s: %w", coll, err)
		}
	}
	return nil
}

func (m *MongoTestDB) ResolveGroupID(ctx context.Context, conversationID string) (string, error) {
	client, db, err := m.db(ctx)
	if err != nil {
		return "", err
	}
	defer client.Disconnect(ctx)

	var doc struct {
		ConversationGroupID string `bson:"conversation_group_id"`
	}
	err = db.Collection("conversations").FindOne(ctx, bson.M{"_id": conversationID}).Decode(&doc)
	if err != nil {
		return "", fmt.Errorf("could not resolve conversation group ID for %s: %w", conversationID, err)
	}
	return doc.ConversationGroupID, nil
}

func (m *MongoTestDB) ExecSQL(_ context.Context, _ string) ([]map[string]interface{}, error) {
	// Java parity: SQL verification queries are skipped for MongoDB backend.
	// Return nil (not empty slice) to signal "skip" to assertion steps.
	return nil, nil
}

func (m *MongoTestDB) SoftDeleteConversation(ctx context.Context, conversationID string, days int) error {
	client, db, err := m.db(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)

	deletedAt := time.Now().AddDate(0, 0, -days)

	// Find the conversation to get its group ID
	var conv struct {
		ConversationGroupID string `bson:"conversation_group_id"`
	}
	err = db.Collection("conversations").FindOne(ctx, bson.M{"_id": conversationID}).Decode(&conv)
	if err != nil {
		return fmt.Errorf("failed to find conversation %s: %w", conversationID, err)
	}

	// Soft-delete the conversation group
	_, err = db.Collection("conversation_groups").UpdateByID(ctx, conv.ConversationGroupID,
		bson.M{"$set": bson.M{"deleted_at": deletedAt}})
	if err != nil {
		return fmt.Errorf("failed to soft-delete conversation group: %w", err)
	}

	// Soft-delete the conversation
	_, err = db.Collection("conversations").UpdateByID(ctx, conversationID,
		bson.M{"$set": bson.M{"deleted_at": deletedAt}})
	if err != nil {
		return fmt.Errorf("failed to soft-delete conversation: %w", err)
	}
	return nil
}

func (m *MongoTestDB) DeleteAllTasks(ctx context.Context) error {
	client, db, err := m.db(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)
	_, err = db.Collection("tasks").DeleteMany(ctx, bson.M{})
	return err
}

func (m *MongoTestDB) CreateTask(ctx context.Context, id, taskType, body string) error {
	client, db, err := m.db(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)

	var taskBody any
	if err := json.Unmarshal([]byte(body), &taskBody); err != nil {
		return fmt.Errorf("invalid task body JSON: %w", err)
	}

	now := time.Now()
	_, err = db.Collection("tasks").InsertOne(ctx, bson.M{
		"_id":         id,
		"task_type":   taskType,
		"task_body":   taskBody,
		"created_at":  now,
		"retry_at":    now,
		"retry_count": 0,
	})
	return err
}

func (m *MongoTestDB) CreateFailedTask(ctx context.Context, id, taskType, body string) error {
	client, db, err := m.db(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)

	var taskBody any
	if err := json.Unmarshal([]byte(body), &taskBody); err != nil {
		return fmt.Errorf("invalid task body JSON: %w", err)
	}

	now := time.Now()
	lastError := "previous failure"
	_, err = db.Collection("tasks").InsertOne(ctx, bson.M{
		"_id":         id,
		"task_type":   taskType,
		"task_body":   taskBody,
		"created_at":  now,
		"retry_at":    now.Add(-1 * time.Hour),
		"retry_count": 1,
		"last_error":  lastError,
	})
	return err
}

func (m *MongoTestDB) ClaimReadyTasks(ctx context.Context, limit int) ([]cucumber.TaskRow, error) {
	client, db, err := m.db(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(ctx)

	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: 1}}).
		SetLimit(int64(limit))
	cur, err := db.Collection("tasks").Find(ctx, bson.M{
		"retry_at": bson.M{"$lte": time.Now()},
	}, opts)
	if err != nil {
		return nil, err
	}

	var docs []struct {
		ID       string `bson:"_id"`
		TaskType string `bson:"task_type"`
		TaskBody any    `bson:"task_body"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}

	result := make([]cucumber.TaskRow, len(docs))
	for i, d := range docs {
		bodyBytes, _ := json.Marshal(d.TaskBody)
		result[i] = cucumber.TaskRow{
			ID:       d.ID,
			TaskType: d.TaskType,
			TaskBody: string(bodyBytes),
		}
	}
	return result, nil
}

func (m *MongoTestDB) DeleteTask(ctx context.Context, id string) error {
	client, db, err := m.db(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)
	_, err = db.Collection("tasks").DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (m *MongoTestDB) FailTask(ctx context.Context, id, errMsg string) error {
	client, db, err := m.db(ctx)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)
	_, err = db.Collection("tasks").UpdateByID(ctx, id, bson.M{
		"$inc": bson.M{"retry_count": 1},
		"$set": bson.M{
			"retry_at":   time.Now().Add(30 * time.Second),
			"last_error": errMsg,
		},
	})
	return err
}

func (m *MongoTestDB) GetTask(ctx context.Context, id string) (*cucumber.TaskRow, error) {
	client, db, err := m.db(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(ctx)

	var doc struct {
		ID         string    `bson:"_id"`
		TaskType   string    `bson:"task_type"`
		RetryAt    time.Time `bson:"retry_at"`
		RetryCount int       `bson:"retry_count"`
		LastError  *string   `bson:"last_error"`
	}
	if err := db.Collection("tasks").FindOne(ctx, bson.M{"_id": id}).Decode(&doc); err != nil {
		return nil, err
	}
	return &cucumber.TaskRow{
		ID:         doc.ID,
		TaskType:   doc.TaskType,
		RetryAt:    doc.RetryAt,
		RetryCount: doc.RetryCount,
		LastError:  doc.LastError,
	}, nil
}

func (m *MongoTestDB) CountTasks(ctx context.Context) (int, error) {
	client, db, err := m.db(ctx)
	if err != nil {
		return 0, err
	}
	defer client.Disconnect(ctx)
	count, err := db.Collection("tasks").CountDocuments(ctx, bson.M{})
	return int(count), err
}
