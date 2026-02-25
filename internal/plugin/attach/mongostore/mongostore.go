package mongostore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	"github.com/chirino/memory-service/internal/tempfiles"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func init() {
	registryattach.Register(registryattach.Plugin{
		Name:   "mongo",
		Loader: load,
	})
}

// ForceImport is a no-op variable that can be referenced to ensure this package's init() runs.
var ForceImport = 0

func load(ctx context.Context) (registryattach.AttachmentStore, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("mongostore: missing config in context")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.DBURL))
	if err != nil {
		return nil, fmt.Errorf("mongostore: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongostore: ping failed: %w", err)
	}
	db := client.Database("memory_service")
	bucket := db.GridFSBucket()

	// Legacy chunk collection kept for backward-compat reads of old Go-stored attachments.
	chunks := db.Collection("attachment_file_chunks")
	_, _ = chunks.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "storage_key", Value: 1}, {Key: "seq", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return &MongoAttachmentStore{
		bucket:  bucket,
		chunks:  chunks,
		tempDir: cfg.ResolvedTempDir(),
	}, nil
}

type MongoAttachmentStore struct {
	bucket  *mongo.GridFSBucket
	chunks  *mongo.Collection
	tempDir string
}

// fileChunkDoc is kept only for backward-compat reads of attachments written by older Go versions.
type fileChunkDoc struct {
	StorageKey string    `bson:"storage_key"`
	Seq        int       `bson:"seq"`
	Data       []byte    `bson:"data"`
	CreatedAt  time.Time `bson:"created_at"`
}

// Store uploads data to GridFS, matching Java's DatabaseFileStore behaviour.
// Returns the ObjectId hex string as the storage key.
func (s *MongoAttachmentStore) Store(ctx context.Context, data io.Reader, maxSize int64, contentType string) (*registryattach.FileStoreResult, error) {
	hasher := sha256.New()
	limited := io.LimitReader(data, maxSize+1)

	// Wrap reader to track size and compute hash while uploading.
	counted := &countingReader{r: io.TeeReader(limited, hasher)}

	fileID, err := s.bucket.UploadFromStream(ctx, "attachment", counted)
	if err != nil {
		return nil, fmt.Errorf("mongostore: gridfs upload: %w", err)
	}

	if counted.n > maxSize {
		// Delete the oversized upload.
		_ = s.bucket.Delete(ctx, fileID)
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxSize)
	}

	return &registryattach.FileStoreResult{
		StorageKey: fileID.Hex(),
		Size:       counted.n,
		SHA256:     fmt.Sprintf("%x", hasher.Sum(nil)),
	}, nil
}

// Retrieve fetches attachment data. ObjectId hex keys (no dashes) are GridFS (Java/Go primary
// storage); UUID keys with dashes fall back to the legacy attachment_file_chunks collection.
func (s *MongoAttachmentStore) Retrieve(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	if isObjectIDHex(storageKey) {
		return s.retrieveGridFS(ctx, storageKey)
	}
	return s.retrieveChunks(ctx, storageKey)
}

func (s *MongoAttachmentStore) retrieveGridFS(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	oid, err := bson.ObjectIDFromHex(storageKey)
	if err != nil {
		return nil, fmt.Errorf("mongostore: invalid objectid key %s: %w", storageKey, err)
	}

	tmp, err := tempfiles.Create(s.tempDir, "memory-service-mongo-gridfs-*")
	if err != nil {
		return nil, fmt.Errorf("mongostore: create temp file: %w", err)
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}

	ds, err := s.bucket.OpenDownloadStream(ctx, oid)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attachment not found: %s", storageKey)
	}
	defer ds.Close()

	if _, err := io.Copy(tmp, ds); err != nil {
		cleanup()
		return nil, fmt.Errorf("mongostore: spool gridfs stream: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("mongostore: rewind temp file: %w", err)
	}
	return tempfiles.NewDeleteOnClose(tmp), nil
}

func (s *MongoAttachmentStore) retrieveChunks(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	tmp, err := tempfiles.Create(s.tempDir, "memory-service-mongo-attachment-*")
	if err != nil {
		return nil, fmt.Errorf("mongostore: create temp file: %w", err)
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}

	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}})
	cur, err := s.chunks.Find(ctx, bson.M{"storage_key": storageKey}, opts)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attachment not found: %s", storageKey)
	}
	defer cur.Close(ctx)

	found := false
	for cur.Next(ctx) {
		found = true
		var doc fileChunkDoc
		if err := cur.Decode(&doc); err != nil {
			cleanup()
			return nil, fmt.Errorf("mongostore: decode chunk: %w", err)
		}
		if _, err := tmp.Write(doc.Data); err != nil {
			cleanup()
			return nil, fmt.Errorf("mongostore: spool chunk: %w", err)
		}
	}
	if err := cur.Err(); err != nil {
		cleanup()
		return nil, fmt.Errorf("mongostore: stream chunks: %w", err)
	}
	if !found {
		cleanup()
		return nil, fmt.Errorf("attachment not found: %s", storageKey)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("mongostore: rewind temp file: %w", err)
	}
	return tempfiles.NewDeleteOnClose(tmp), nil
}

func (s *MongoAttachmentStore) Delete(ctx context.Context, storageKey string) error {
	if isObjectIDHex(storageKey) {
		oid, err := bson.ObjectIDFromHex(storageKey)
		if err != nil {
			return fmt.Errorf("mongostore: invalid objectid key %s: %w", storageKey, err)
		}
		return s.bucket.Delete(ctx, oid)
	}
	// Legacy UUID chunk key.
	_, err := s.chunks.DeleteMany(ctx, bson.M{"storage_key": storageKey})
	return err
}

func (s *MongoAttachmentStore) GetSignedURL(_ context.Context, _ string, _ time.Duration) (*url.URL, error) {
	return nil, fmt.Errorf("signed URLs not supported for mongo attachment store")
}

// isObjectIDHex returns true if s looks like a 24-character hex ObjectId (no dashes).
func isObjectIDHex(s string) bool {
	if len(s) != 24 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
