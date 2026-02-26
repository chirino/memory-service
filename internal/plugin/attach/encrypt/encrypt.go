package encrypt

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
)

// Wrap wraps an existing AttachmentStore with AES-GCM encryption.
// Returns the store unchanged if encryptionKey is empty.
func Wrap(inner registryattach.AttachmentStore, encryptionKey string) (registryattach.AttachmentStore, error) {
	if encryptionKey == "" {
		return inner, nil
	}

	key, err := config.DecodeEncryptionKey(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt attach: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("encrypt attach: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encrypt attach: %w", err)
	}

	return &EncryptStore{
		inner: inner,
		gcm:   gcm,
	}, nil
}

type EncryptStore struct {
	inner registryattach.AttachmentStore
	gcm   cipher.AEAD
}

func (s *EncryptStore) Store(ctx context.Context, data io.Reader, maxSize int64, contentType string) (*registryattach.FileStoreResult, error) {
	const chunkSize = 64 * 1024
	hasher := sha256.New()
	limited := io.LimitReader(data, maxSize+1)
	pr, pw := io.Pipe()
	type encryptionMeta struct {
		size   int64
		sha256 string
		err    error
	}
	done := make(chan encryptionMeta, 1)

	go func() {
		var total int64
		buf := make([]byte, chunkSize)
		for {
			n, err := limited.Read(buf)
			if n > 0 {
				total += int64(n)
				if total > maxSize {
					_ = pw.CloseWithError(fmt.Errorf("file exceeds maximum size of %d bytes", maxSize))
					done <- encryptionMeta{err: fmt.Errorf("file exceeds maximum size of %d bytes", maxSize)}
					return
				}
				plain := append([]byte(nil), buf[:n]...)
				if _, hErr := hasher.Write(plain); hErr != nil {
					_ = pw.CloseWithError(hErr)
					done <- encryptionMeta{err: hErr}
					return
				}
				nonce := make([]byte, s.gcm.NonceSize())
				if _, nErr := rand.Read(nonce); nErr != nil {
					_ = pw.CloseWithError(nErr)
					done <- encryptionMeta{err: nErr}
					return
				}
				ciphertext := s.gcm.Seal(nil, nonce, plain, nil)
				frameLen := uint32(len(nonce) + len(ciphertext))
				var header [4]byte
				binary.BigEndian.PutUint32(header[:], frameLen)
				if _, wErr := pw.Write(header[:]); wErr != nil {
					done <- encryptionMeta{err: wErr}
					return
				}
				if _, wErr := pw.Write(nonce); wErr != nil {
					done <- encryptionMeta{err: wErr}
					return
				}
				if _, wErr := pw.Write(ciphertext); wErr != nil {
					done <- encryptionMeta{err: wErr}
					return
				}
			}
			if err == io.EOF {
				_ = pw.Close()
				done <- encryptionMeta{size: total, sha256: fmt.Sprintf("%x", hasher.Sum(nil))}
				return
			}
			if err != nil {
				_ = pw.CloseWithError(err)
				done <- encryptionMeta{err: err}
				return
			}
		}
	}()

	result, err := s.inner.Store(ctx, pr, encryptedUpperBound(maxSize, chunkSize, s.gcm.NonceSize(), s.gcm.Overhead()), contentType)
	meta := <-done
	if meta.err != nil {
		return nil, meta.err
	}
	if err != nil {
		return nil, err
	}
	// Return plaintext size and SHA256 — callers see the logical (unencrypted) values.
	result.Size = meta.size
	result.SHA256 = meta.sha256
	return result, nil
}

func (s *EncryptStore) Retrieve(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	rc, err := s.inner.Retrieve(ctx, storageKey)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		defer rc.Close()
		defer pw.Close()

		reader := bufio.NewReader(rc)
		nonceSize := s.gcm.NonceSize()

		for {
			var hdr [4]byte
			if _, err := io.ReadFull(reader, hdr[:]); err != nil {
				if err == io.EOF {
					return
				}
				if err == io.ErrUnexpectedEOF {
					_ = pw.CloseWithError(fmt.Errorf("decrypt failed: truncated frame header"))
					return
				}
				_ = pw.CloseWithError(fmt.Errorf("decrypt failed: %w", err))
				return
			}
			frameLen := binary.BigEndian.Uint32(hdr[:])
			if frameLen < uint32(nonceSize+s.gcm.Overhead()) {
				_ = pw.CloseWithError(fmt.Errorf("decrypt failed: invalid frame length"))
				return
			}

			frame := make([]byte, frameLen)
			if _, err := io.ReadFull(reader, frame); err != nil {
				_ = pw.CloseWithError(fmt.Errorf("decrypt failed: truncated frame payload"))
				return
			}
			nonce := frame[:nonceSize]
			ciphertext := frame[nonceSize:]
			plaintext, err := s.gcm.Open(nil, nonce, ciphertext, nil)
			if err != nil {
				_ = pw.CloseWithError(fmt.Errorf("decrypt failed: %w", err))
				return
			}
			if _, err := pw.Write(plaintext); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
	}()

	return pr, nil
}

func (s *EncryptStore) Delete(ctx context.Context, storageKey string) error {
	return s.inner.Delete(ctx, storageKey)
}

func (s *EncryptStore) GetSignedURL(ctx context.Context, storageKey string, expiry time.Duration) (*url.URL, error) {
	_ = ctx
	_ = storageKey
	_ = expiry
	return nil, fmt.Errorf("signed URLs not supported for encrypted attachment store")
}

func encryptedUpperBound(maxSize int64, chunkSize int64, nonceSize int, overhead int) int64 {
	if maxSize <= 0 {
		return maxSize
	}
	chunks := (maxSize + chunkSize - 1) / chunkSize
	return maxSize + chunks*int64(nonceSize+overhead+4)
}
