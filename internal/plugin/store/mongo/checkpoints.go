//go:build !nomongo

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const adminCheckpointValueFieldDomain = "admin-checkpoint.value"

type checkpointDoc struct {
	ClientID        string     `bson:"client_id"`
	ContentType     string     `bson:"content_type"`
	Value           []byte     `bson:"value"`
	Revision        uint64     `bson:"revision"`
	LeaseTokenHash  []byte     `bson:"lease_token_hash,omitempty"`
	LeaseGeneration uint64     `bson:"lease_generation"`
	LeaseExpiresAt  *time.Time `bson:"lease_expires_at,omitempty"`
	UpdatedAt       time.Time  `bson:"updated_at"`
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
	value, err := s.decryptCheckpointValue(clientID, doc.Value)
	if err != nil {
		return nil, err
	}
	return &registrystore.ClientCheckpoint{
		ClientID:    doc.ClientID,
		ContentType: doc.ContentType,
		Value:       append(json.RawMessage(nil), value...),
		Revision:    registrystore.EncodeCheckpointRevision(doc.Revision),
		UpdatedAt:   doc.UpdatedAt.UTC(),
	}, nil
}

func (s *MongoStore) AdminPutCheckpoint(ctx context.Context, checkpoint registrystore.ClientCheckpoint) (*registrystore.ClientCheckpoint, error) {
	return s.AdminPutCheckpointCAS(ctx, registrystore.CheckpointCASWrite{Checkpoint: checkpoint})
}

func (s *MongoStore) AdminPutCheckpointCAS(ctx context.Context, write registrystore.CheckpointCASWrite) (*registrystore.ClientCheckpoint, error) {
	checkpoint := write.Checkpoint
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
	expectedRevision, err := registrystore.ParseCheckpointRevision(strings.TrimSpace(write.ExpectedRevision))
	if err != nil {
		return nil, err
	}
	tokenHash, err := registrystore.HashCheckpointLeaseToken(strings.TrimSpace(write.LeaseToken))
	if err != nil {
		return nil, err
	}
	encryptedValue, err := s.encryptCheckpointValue(clientID, value)
	if err != nil {
		return nil, err
	}
	conditions := bson.A{
		bson.M{"$or": bson.A{
			bson.M{"lease_expires_at": bson.M{"$exists": false}},
			bson.M{"lease_expires_at": nil},
			bson.M{"$expr": bson.M{"$lte": bson.A{"$lease_expires_at", "$$NOW"}}},
		}},
	}
	if expectedRevision != 0 {
		conditions = append(conditions, bson.M{"revision": expectedRevision})
	}
	if strings.TrimSpace(write.LeaseToken) != "" {
		conditions[0] = bson.M{"$or": bson.A{
			bson.M{"lease_expires_at": bson.M{"$exists": false}},
			bson.M{"lease_expires_at": nil},
			bson.M{"$expr": bson.M{"$lte": bson.A{"$lease_expires_at", "$$NOW"}}},
			bson.M{"lease_token_hash": tokenHash[:]},
		}}
	}
	filter := bson.M{"client_id": clientID, "$and": conditions}
	update := mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{
		"content_type": contentType,
		"value":        encryptedValue,
		"revision":     bson.M{"$add": bson.A{bson.M{"$ifNull": bson.A{"$revision", 0}}, 1}},
		"updated_at":   "$$NOW",
	}}}}
	var doc checkpointDoc
	err = s.db.Collection("admin_checkpoints").FindOneAndUpdate(ctx, filter, update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, registrystore.NewCheckpointConflict()
		}
		return nil, err
	}
	decrypted, err := s.decryptCheckpointValue(clientID, doc.Value)
	if err != nil {
		return nil, err
	}
	return &registrystore.ClientCheckpoint{
		ClientID:    doc.ClientID,
		ContentType: doc.ContentType,
		Value:       append(json.RawMessage(nil), decrypted...),
		Revision:    registrystore.EncodeCheckpointRevision(doc.Revision),
		UpdatedAt:   doc.UpdatedAt.UTC(),
	}, nil
}

func (s *MongoStore) AdminAcquireCheckpointLease(ctx context.Context, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
	return s.mongoUpdateCheckpointLease(ctx, "acquire", clientID, token, ttl)
}

func (s *MongoStore) AdminRenewCheckpointLease(ctx context.Context, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
	return s.mongoUpdateCheckpointLease(ctx, "renew", clientID, token, ttl)
}

func (s *MongoStore) mongoUpdateCheckpointLease(ctx context.Context, operation, clientID, token string, ttl time.Duration) (*registrystore.ClientCheckpointLease, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, &registrystore.ValidationError{Field: "clientId", Message: "clientId is required"}
	}
	if err := registrystore.ValidateCheckpointLeaseTTL(ttl); err != nil {
		return nil, err
	}
	hash, err := registrystore.HashCheckpointLeaseToken(strings.TrimSpace(token))
	if err != nil {
		return nil, err
	}
	filter := bson.M{"client_id": clientID}
	set := bson.M{
		"lease_expires_at": bson.M{"$dateAdd": bson.M{"startDate": "$$NOW", "unit": "second", "amount": int64(ttl / time.Second)}},
	}
	if operation == "acquire" {
		filter["$or"] = bson.A{
			bson.M{"lease_expires_at": bson.M{"$exists": false}},
			bson.M{"lease_expires_at": nil},
			bson.M{"$expr": bson.M{"$lte": bson.A{"$lease_expires_at", "$$NOW"}}},
		}
		set["lease_token_hash"] = hash[:]
		set["lease_generation"] = bson.M{"$add": bson.A{bson.M{"$ifNull": bson.A{"$lease_generation", 0}}, 1}}
	} else {
		filter["lease_token_hash"] = hash[:]
		filter["$expr"] = bson.M{"$gt": bson.A{"$lease_expires_at", "$$NOW"}}
	}
	update := mongo.Pipeline{bson.D{{Key: "$set", Value: set}}}
	var doc checkpointDoc
	err = s.db.Collection("admin_checkpoints").FindOneAndUpdate(ctx, filter, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, registrystore.NewCheckpointConflict()
		}
		return nil, err
	}
	if doc.LeaseExpiresAt == nil {
		return nil, errors.New("checkpoint lease update returned no expiry")
	}
	return &registrystore.ClientCheckpointLease{ClientID: clientID, Token: token, Generation: doc.LeaseGeneration, ExpiresAt: doc.LeaseExpiresAt.UTC()}, nil
}

func (s *MongoStore) AdminReleaseCheckpointLease(ctx context.Context, clientID, token string) error {
	hash, err := registrystore.HashCheckpointLeaseToken(strings.TrimSpace(token))
	if err != nil {
		return err
	}
	result, err := s.db.Collection("admin_checkpoints").UpdateOne(ctx,
		bson.M{"client_id": strings.TrimSpace(clientID), "lease_token_hash": hash[:]},
		bson.M{"$unset": bson.M{"lease_token_hash": "", "lease_expires_at": ""}})
	if err != nil {
		return err
	}
	if result.ModifiedCount == 0 {
		return registrystore.NewCheckpointConflict()
	}
	return nil
}

func (s *MongoStore) encryptCheckpointValue(clientID string, value []byte) ([]byte, error) {
	if s.enc == nil || value == nil {
		return value, nil
	}
	return s.enc.EncryptField(value, adminCheckpointValueFieldDomain, clientID)
}

func (s *MongoStore) decryptCheckpointValue(clientID string, value []byte) ([]byte, error) {
	if s.enc == nil || value == nil {
		return value, nil
	}
	return s.enc.DecryptField(value, adminCheckpointValueFieldDomain, clientID)
}

var _ registrystore.AdminCheckpointStore = (*MongoStore)(nil)
var _ registrystore.AdminCheckpointLeaseStore = (*MongoStore)(nil)
