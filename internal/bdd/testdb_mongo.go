package bdd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

type mongoQuerySpec struct {
	Collection string                   `json:"collection"`
	Operation  string                   `json:"operation"`
	Filter     map[string]interface{}   `json:"filter"`
	Projection map[string]interface{}   `json:"projection"`
	Sort       map[string]interface{}   `json:"sort"`
	Limit      *int64                   `json:"limit"`
	Pipeline   []map[string]interface{} `json:"pipeline"`
}

func (m *MongoTestDB) ExecMongoQuery(ctx context.Context, query string) ([]map[string]interface{}, error) {
	client, db, err := m.db(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(ctx)

	spec, err := parseMongoQuerySpec(query)
	if err != nil {
		return nil, err
	}

	collection := db.Collection(spec.Collection)
	filter := bson.M{}
	if spec.Filter != nil {
		filter = bson.M(spec.Filter)
	}

	switch spec.Operation {
	case "find":
		opts := options.Find()
		if spec.Projection != nil {
			opts.SetProjection(bson.M(spec.Projection))
		}
		if spec.Sort != nil {
			opts.SetSort(bson.M(spec.Sort))
		}
		if spec.Limit != nil {
			opts.SetLimit(*spec.Limit)
		}
		cur, err := collection.Find(ctx, filter, opts)
		if err != nil {
			return nil, fmt.Errorf("mongo find failed: %w", err)
		}
		defer cur.Close(ctx)
		return mongoCursorToRows(ctx, cur)
	case "count":
		count, err := collection.CountDocuments(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("mongo count failed: %w", err)
		}
		return []map[string]interface{}{{"count": count}}, nil
	case "aggregate":
		if len(spec.Pipeline) == 0 {
			return nil, fmt.Errorf("mongo query 'aggregate' requires non-empty pipeline")
		}
		pipeline := make([]bson.M, 0, len(spec.Pipeline))
		for _, stage := range spec.Pipeline {
			pipeline = append(pipeline, bson.M(stage))
		}
		cur, err := collection.Aggregate(ctx, pipeline)
		if err != nil {
			return nil, fmt.Errorf("mongo aggregate failed: %w", err)
		}
		defer cur.Close(ctx)
		return mongoCursorToRows(ctx, cur)
	default:
		return nil, fmt.Errorf("unsupported mongo operation %q (supported: find, count, aggregate)", spec.Operation)
	}
}

func parseMongoQuerySpec(query string) (*mongoQuerySpec, error) {
	decoder := json.NewDecoder(strings.NewReader(query))
	decoder.UseNumber()

	var spec mongoQuerySpec
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("invalid MongoDB query JSON: %w", err)
	}

	spec.Collection = strings.TrimSpace(spec.Collection)
	if spec.Collection == "" {
		return nil, fmt.Errorf("mongo query requires non-empty 'collection'")
	}

	spec.Operation = strings.ToLower(strings.TrimSpace(spec.Operation))
	if spec.Operation == "" {
		spec.Operation = "find"
	}

	if spec.Filter != nil {
		spec.Filter = normalizeJSONObject(spec.Filter)
	}
	if spec.Projection != nil {
		spec.Projection = normalizeJSONObject(spec.Projection)
	}
	if spec.Sort != nil {
		spec.Sort = normalizeJSONObject(spec.Sort)
	}
	if spec.Pipeline != nil {
		for i, stage := range spec.Pipeline {
			spec.Pipeline[i] = normalizeJSONObject(stage)
		}
	}

	return &spec, nil
}

func normalizeJSONObject(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = normalizeJSONValue(value)
	}
	return out
}

func normalizeJSONValue(v interface{}) interface{} {
	switch typed := v.(type) {
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return i
		}
		if f, err := typed.Float64(); err == nil {
			return f
		}
		return typed.String()
	case map[string]interface{}:
		return normalizeJSONObject(typed)
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i := range typed {
			out[i] = normalizeJSONValue(typed[i])
		}
		return out
	default:
		return v
	}
}

func mongoCursorToRows(ctx context.Context, cur *mongo.Cursor) ([]map[string]interface{}, error) {
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongo decode failed: %w", err)
	}

	rows := make([]map[string]interface{}, 0, len(docs))
	for _, doc := range docs {
		rows = append(rows, normalizeMongoDocument(doc))
	}
	return rows, nil
}

func normalizeMongoDocument(doc map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(doc))
	for key, value := range doc {
		out[key] = normalizeMongoValue(value)
	}
	return out
}

func normalizeMongoValue(v interface{}) interface{} {
	switch typed := v.(type) {
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	case bson.DateTime:
		return typed.Time().Format(time.RFC3339Nano)
	case bson.ObjectID:
		return typed.Hex()
	case bson.Decimal128:
		return typed.String()
	case bson.M:
		return normalizeMongoDocument(map[string]interface{}(typed))
	case map[string]interface{}:
		return normalizeMongoDocument(typed)
	case bson.D:
		out := make(map[string]interface{}, len(typed))
		for _, item := range typed {
			out[item.Key] = normalizeMongoValue(item.Value)
		}
		return out
	case bson.A:
		out := make([]interface{}, len(typed))
		for i := range typed {
			out[i] = normalizeMongoValue(typed[i])
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i := range typed {
			out[i] = normalizeMongoValue(typed[i])
		}
		return out
	default:
		return v
	}
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
