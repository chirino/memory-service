// Package dekstore provides a thin database layer for vault and kms encryption
// providers to manage their wrapped DEK record in the application database.
// Supports both postgres and mongo backends, matching cfg.DatastoreType.
//
// Schema: one row per provider. wrapped_deks[0] is the primary DEK (newest);
// subsequent elements are legacy keys kept for decryption-only rotation.
// revision enables optimistic updates so a future key-rotation CLI can safely
// prepend a new wrapped DEK without clobbering a concurrent update.
package dekstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Record is the single DEK record stored per encryption provider.
type Record struct {
	// WrappedDEKs holds backend-wrapped DEK ciphertexts.
	// Index 0 is the primary (used for new encryptions); subsequent entries
	// are legacy keys retained for decryption-only key rotation.
	WrappedDEKs [][]byte
	// Revision is incremented on every update. Used for optimistic locking.
	Revision int64
}

// Store manages a single DEK record per provider name.
type Store interface {
	// Load returns the record for provider, or nil if none exists.
	Load(ctx context.Context, provider string) (*Record, error)

	// Bootstrap inserts the initial record if no row exists for provider.
	// On primary-key conflict (another instance beat us) it silently succeeds;
	// the caller must Load again to obtain the winning record.
	Bootstrap(ctx context.Context, provider string, wrappedDEK []byte) error

	// Update replaces wrapped_deks and increments revision, but only when the
	// stored revision equals oldRevision (optimistic locking). Returns true if
	// the update was applied, false if the revision was stale.
	Update(ctx context.Context, provider string, wrappedDEKs [][]byte, oldRevision int64) (bool, error)

	// Close releases the underlying connection.
	Close()
}

// New opens a minimal connection and returns a Store based on cfg.DatastoreType.
func New(cfg *config.Config) (Store, error) {
	if cfg.DatastoreType == "mongo" {
		return newMongo(cfg)
	}
	return newPostgres(cfg)
}

// ── Postgres ──────────────────────────────────────────────────────────────────

type pgStore struct{ conn *pgx.Conn }

func newPostgres(cfg *config.Config) (Store, error) {
	conn, err := pgx.Connect(context.Background(), cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("dekstore: postgres connect: %w", err)
	}
	return &pgStore{conn: conn}, nil
}

func (s *pgStore) Close() { s.conn.Close(context.Background()) }

func (s *pgStore) Load(ctx context.Context, provider string) (*Record, error) {
	var r Record
	err := s.conn.QueryRow(ctx,
		`SELECT wrapped_deks, revision FROM encryption_deks WHERE provider=$1`,
		provider,
	).Scan(&r.WrappedDEKs, &r.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dekstore: load: %w", err)
	}
	return &r, nil
}

func (s *pgStore) Bootstrap(ctx context.Context, provider string, wrappedDEK []byte) error {
	_, err := s.conn.Exec(ctx,
		`INSERT INTO encryption_deks (provider, wrapped_deks, revision)
		 VALUES ($1, $2, 0)
		 ON CONFLICT (provider) DO NOTHING`,
		provider, [][]byte{wrappedDEK},
	)
	if err != nil {
		return fmt.Errorf("dekstore: bootstrap: %w", err)
	}
	return nil
}

func (s *pgStore) Update(ctx context.Context, provider string, wrappedDEKs [][]byte, oldRevision int64) (bool, error) {
	tag, err := s.conn.Exec(ctx,
		`UPDATE encryption_deks
		 SET wrapped_deks=$2, revision=revision+1
		 WHERE provider=$1 AND revision=$3`,
		provider, wrappedDEKs, oldRevision,
	)
	if err != nil {
		return false, fmt.Errorf("dekstore: update: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ── MongoDB ───────────────────────────────────────────────────────────────────

type mongoStore struct {
	client *mongo.Client
	coll   *mongo.Collection
}

type dekDoc struct {
	Provider    string    `bson:"provider"`
	WrappedDEKs [][]byte  `bson:"wrapped_deks"`
	Revision    int64     `bson:"revision"`
	CreatedAt   time.Time `bson:"created_at,omitempty"`
}

func newMongo(cfg *config.Config) (Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.DBURL))
	if err != nil {
		return nil, fmt.Errorf("dekstore: mongo connect: %w", err)
	}
	if err := client.Ping(context.Background(), nil); err != nil {
		client.Disconnect(context.Background())
		return nil, fmt.Errorf("dekstore: mongo ping: %w", err)
	}
	coll := client.Database("memory_service").Collection("encryption_deks")
	return &mongoStore{client: client, coll: coll}, nil
}

func (s *mongoStore) Close() { s.client.Disconnect(context.Background()) }

func (s *mongoStore) Load(ctx context.Context, provider string) (*Record, error) {
	var doc dekDoc
	err := s.coll.FindOne(ctx, bson.M{"provider": provider}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dekstore: load: %w", err)
	}
	return &Record{WrappedDEKs: doc.WrappedDEKs, Revision: doc.Revision}, nil
}

func (s *mongoStore) Bootstrap(ctx context.Context, provider string, wrappedDEK []byte) error {
	// $setOnInsert fires only when a new document is created (upsert race-safe).
	_, err := s.coll.UpdateOne(ctx,
		bson.M{"provider": provider},
		bson.M{"$setOnInsert": bson.M{
			"provider":     provider,
			"wrapped_deks": [][]byte{wrappedDEK},
			"revision":     int64(0),
			"created_at":   time.Now(),
		}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("dekstore: bootstrap: %w", err)
	}
	return nil
}

func (s *mongoStore) Update(ctx context.Context, provider string, wrappedDEKs [][]byte, oldRevision int64) (bool, error) {
	result, err := s.coll.UpdateOne(ctx,
		bson.M{"provider": provider, "revision": oldRevision},
		bson.M{"$set": bson.M{
			"wrapped_deks": wrappedDEKs,
			"revision":     oldRevision + 1,
		}},
	)
	if err != nil {
		return false, fmt.Errorf("dekstore: update: %w", err)
	}
	return result.MatchedCount == 1, nil
}
