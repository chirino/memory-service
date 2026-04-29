//go:build !nomongo

package mongo

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type checkpointDoc struct {
	ClientID    string    `bson:"client_id"`
	ContentType string    `bson:"content_type"`
	Value       []byte    `bson:"value"`
	UpdatedAt   time.Time `bson:"updated_at"`
}

func (s *MongoStore) AdminGetCheckpoint(ctx context.Context, clientID string) (*registrystore.ClientCheckpoint, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, &registrystore.ValidationError{Field: "clientId", Message: "clientId is required"}
	}
	var doc checkpointDoc
	err := s.db.Collection("admin_checkpoints").FindOne(ctx, bson.M{
		"client_id": clientID,
	}).Decode(&doc)
	if err != nil {
		if strings.Contains(err.Error(), "no documents") {
			return nil, &registrystore.NotFoundError{Resource: "checkpoint", ID: clientID}
		}
		return nil, err
	}
	value, err := s.decrypt(doc.Value)
	if err != nil {
		return nil, err
	}
	return &registrystore.ClientCheckpoint{
		ClientID:    doc.ClientID,
		ContentType: doc.ContentType,
		Value:       append(json.RawMessage(nil), value...),
		UpdatedAt:   doc.UpdatedAt.UTC(),
	}, nil
}

func (s *MongoStore) AdminPutCheckpoint(ctx context.Context, checkpoint registrystore.ClientCheckpoint) (*registrystore.ClientCheckpoint, error) {
	clientID := strings.TrimSpace(checkpoint.ClientID)
	contentType := strings.TrimSpace(checkpoint.ContentType)
	value := append(json.RawMessage(nil), checkpoint.Value...)
	if clientID == "" {
		return nil, &registrystore.ValidationError{Field: "clientId", Message: "clientId is required"}
	}
	if contentType == "" {
		return nil, &registrystore.ValidationError{Field: "contentType", Message: "contentType is required"}
	}
	if !json.Valid(value) {
		return nil, &registrystore.ValidationError{Field: "value", Message: "value must be valid JSON"}
	}
	encryptedValue, err := s.encrypt(value)
	if err != nil {
		return nil, err
	}
	doc := checkpointDoc{
		ClientID:    clientID,
		ContentType: contentType,
		Value:       encryptedValue,
		UpdatedAt:   time.Now().UTC(),
	}
	_, err = s.db.Collection("admin_checkpoints").UpdateOne(
		ctx,
		bson.M{
			"client_id": clientID,
		},
		bson.M{"$set": doc},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return nil, &registrystore.NotFoundError{Resource: "checkpoint", ID: clientID}
		}
		return nil, err
	}
	return &registrystore.ClientCheckpoint{
		ClientID:    doc.ClientID,
		ContentType: doc.ContentType,
		Value:       append(json.RawMessage(nil), value...),
		UpdatedAt:   doc.UpdatedAt,
	}, nil
}

var _ registrystore.AdminCheckpointStore = (*MongoStore)(nil)
