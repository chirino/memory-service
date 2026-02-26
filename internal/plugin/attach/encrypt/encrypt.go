package encrypt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/chirino/memory-service/internal/dataencryption"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
)

// Wrap wraps an AttachmentStore with MSEH-based AES-GCM encryption via svc.
func Wrap(inner registryattach.AttachmentStore, svc *dataencryption.Service) (registryattach.AttachmentStore, error) {
	return &EncryptStore{inner: inner, svc: svc}, nil
}

// EncryptStore wraps an AttachmentStore with MSEH encryption on write and
// MSEH decryption on read.
type EncryptStore struct {
	inner registryattach.AttachmentStore
	svc   *dataencryption.Service
}

var _ registryattach.AttachmentStore = (*EncryptStore)(nil)

// Store buffers the full plaintext (required for AES-GCM), computes SHA-256 and
// size on the plaintext, encrypts with MSEH, then writes to the inner store.
func (s *EncryptStore) Store(ctx context.Context, data io.Reader, maxSize int64, contentType string) (*registryattach.FileStoreResult, error) {
	limited := io.LimitReader(data, maxSize+1)
	hasher := sha256.New()

	// Read all plaintext so we can compute hash and encrypt in one pass.
	var plainBuf bytes.Buffer
	n, err := io.Copy(io.MultiWriter(&plainBuf, hasher), limited)
	if err != nil {
		return nil, err
	}
	if n > maxSize {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxSize)
	}

	// Encrypt into a buffer. EncryptStream writes the MSEH header immediately;
	// Close() seals and flushes the GCM ciphertext + tag.
	var encBuf bytes.Buffer
	enc, err := s.svc.EncryptStream(&encBuf)
	if err != nil {
		return nil, err
	}
	if _, err := enc.Write(plainBuf.Bytes()); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	encSize := int64(encBuf.Len())
	result, err := s.inner.Store(ctx, &encBuf, encSize, contentType)
	if err != nil {
		return nil, err
	}
	// Callers receive the logical (plaintext) size and SHA-256.
	result.Size = n
	result.SHA256 = fmt.Sprintf("%x", hasher.Sum(nil))
	return result, nil
}

// Retrieve decrypts an MSEH-wrapped attachment.
func (s *EncryptStore) Retrieve(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	rc, err := s.inner.Retrieve(ctx, storageKey)
	if err != nil {
		return nil, err
	}

	reader, err := s.svc.DecryptStream(rc)
	if err != nil {
		_ = rc.Close()
		return nil, err
	}
	return &readCloser{Reader: reader, close: rc.Close}, nil
}

func (s *EncryptStore) Delete(ctx context.Context, storageKey string) error {
	return s.inner.Delete(ctx, storageKey)
}

func (s *EncryptStore) GetSignedURL(ctx context.Context, storageKey string, expiry time.Duration) (*url.URL, error) {
	return nil, fmt.Errorf("signed URLs not supported for encrypted attachment store")
}

// ── helpers ───────────────────────────────────────────────────────────────────

type readCloser struct {
	io.Reader
	close func() error
}

func (r *readCloser) Close() error { return r.close() }
