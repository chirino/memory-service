package encrypt

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"net/url"
	"time"

	"github.com/chirino/memory-service/internal/dataencryption"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
)

// Wrap wraps an AttachmentStore with MSEH-based attachment encryption via svc.
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

// Store streams plaintext through AES-CTR encryption into the inner store while
// hashing and enforcing the plaintext max size.
func (s *EncryptStore) Store(ctx context.Context, data io.Reader, maxSize int64, contentType string) (*registryattach.FileStoreResult, error) {
	hasher := sha256.New()
	plaintext := &hashLimitReader{
		src:     data,
		maxSize: maxSize,
		hasher:  hasher,
	}
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)

	go func() {
		enc, err := s.svc.EncryptStream(pw)
		if err != nil {
			_ = pw.CloseWithError(err)
			errCh <- err
			return
		}
		if _, err := io.Copy(enc, plaintext); err != nil {
			_ = pw.CloseWithError(err)
			errCh <- err
			return
		}
		if err := enc.Close(); err != nil {
			_ = pw.CloseWithError(err)
			errCh <- err
			return
		}
		errCh <- pw.Close()
	}()

	result, err := s.inner.Store(ctx, pr, -1, contentType)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-errCh
		return nil, err
	}
	if err := <-errCh; err != nil {
		if result != nil && result.StorageKey != "" {
			_ = s.inner.Delete(ctx, result.StorageKey)
		}
		return nil, err
	}
	// Callers receive the logical (plaintext) size and SHA-256.
	result.Size = plaintext.count
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

type hashLimitReader struct {
	src     io.Reader
	maxSize int64
	hasher  hash.Hash
	count   int64
}

func (r *hashLimitReader) Read(p []byte) (int, error) {
	if r.maxSize >= 0 {
		remaining := r.maxSize - r.count
		if remaining == 0 {
			var probe [1]byte
			n, err := r.src.Read(probe[:])
			if n > 0 {
				return 0, fmt.Errorf("file exceeds maximum size of %d bytes", r.maxSize)
			}
			return 0, err
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}

	n, err := r.src.Read(p)
	if n > 0 {
		r.count += int64(n)
		if _, writeErr := r.hasher.Write(p[:n]); writeErr != nil {
			return n, writeErr
		}
	}
	return n, err
}
