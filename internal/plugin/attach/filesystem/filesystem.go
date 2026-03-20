package filesystem

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	"github.com/chirino/memory-service/internal/tempfiles"
	"github.com/google/uuid"
)

func init() {
	registryattach.Register(registryattach.Plugin{
		Name:   "fs",
		Loader: load,
	})
}

func load(ctx context.Context) (registryattach.AttachmentStore, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("fsstore: missing config in context")
	}
	rootDir, err := cfg.ResolvedAttachmentsFSDir()
	if err != nil {
		return nil, fmt.Errorf("fsstore: %w", err)
	}
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return nil, fmt.Errorf("fsstore: create root dir: %w", err)
	}
	return &AttachmentStore{rootDir: rootDir}, nil
}

// AttachmentStore stores attachment content on the local filesystem.
type AttachmentStore struct {
	rootDir string
}

func (s *AttachmentStore) Store(_ context.Context, data io.Reader, maxSize int64, _ string) (*registryattach.FileStoreResult, error) {
	storageKey := uuid.NewString()
	destPath, err := s.storagePath(storageKey)
	if err != nil {
		return nil, err
	}
	destDir := filepath.Dir(destPath)

	tmp, err := tempfiles.Create(destDir, "memory-service-fs-upload-*")
	if err != nil {
		return nil, fmt.Errorf("fsstore: create temp file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	hasher := sha256.New()
	limited := data
	if maxSize >= 0 {
		limited = io.LimitReader(data, maxSize+1)
	}
	counting := &countingWriter{h: hasher}
	if _, err := io.Copy(tmp, io.TeeReader(limited, counting)); err != nil {
		return nil, fmt.Errorf("fsstore: write temp file: %w", err)
	}
	if maxSize >= 0 && counting.n > maxSize {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxSize)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("fsstore: close temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), destPath); err != nil {
		return nil, fmt.Errorf("fsstore: move temp file into place: %w", err)
	}

	return &registryattach.FileStoreResult{
		StorageKey: storageKey,
		Size:       counting.n,
		SHA256:     fmt.Sprintf("%x", hasher.Sum(nil)),
	}, nil
}

func (s *AttachmentStore) Retrieve(_ context.Context, storageKey string) (io.ReadCloser, error) {
	path, err := s.storagePath(storageKey)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("attachment not found: %s", storageKey)
		}
		return nil, fmt.Errorf("fsstore: open attachment: %w", err)
	}
	return f, nil
}

func (s *AttachmentStore) Delete(_ context.Context, storageKey string) error {
	path, err := s.storagePath(storageKey)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fsstore: delete attachment: %w", err)
	}
	return nil
}

func (s *AttachmentStore) GetSignedURL(_ context.Context, _ string, _ time.Duration) (*url.URL, error) {
	return nil, fmt.Errorf("signed url unsupported")
}

func (s *AttachmentStore) storagePath(storageKey string) (string, error) {
	key := strings.TrimSpace(storageKey)
	if key == "" {
		return "", fmt.Errorf("fsstore: storage key is required")
	}
	if strings.Contains(key, "..") || strings.ContainsAny(key, `/\`) {
		return "", fmt.Errorf("fsstore: invalid storage key %q", storageKey)
	}
	if len(key) < 4 {
		return "", fmt.Errorf("fsstore: storage key %q is too short", storageKey)
	}
	return filepath.Join(s.rootDir, key[:2], key[2:4], key), nil
}

type countingWriter struct {
	h hash.Hash
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	w.n += int64(n)
	if _, err := w.h.Write(p); err != nil {
		return 0, err
	}
	return n, nil
}
