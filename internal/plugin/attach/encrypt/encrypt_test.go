package encrypt_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	attachencrypt "github.com/chirino/memory-service/internal/plugin/attach/encrypt"
	_ "github.com/chirino/memory-service/internal/plugin/encrypt/dek"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	"github.com/stretchr/testify/require"
)

const testKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestEncryptStoreStreamsCiphertext(t *testing.T) {
	svc := newEncryptionService(t)
	inner := &captureAttachmentStore{}

	store, err := attachencrypt.Wrap(inner, svc)
	require.NoError(t, err)

	plaintext := bytes.Repeat([]byte("stream-me"), 512)
	result, err := store.Store(context.Background(), bytes.NewReader(plaintext), int64(len(plaintext)), "text/plain")
	require.NoError(t, err)

	require.Equal(t, int64(-1), inner.maxSize)
	require.Equal(t, "text/plain", inner.contentType)
	require.Equal(t, int64(len(plaintext)), result.Size)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(plaintext)), result.SHA256)

	header, hasMagic, err := dataencryption.ReadHeader(bytes.NewReader(inner.data))
	require.NoError(t, err)
	require.True(t, hasMagic)
	require.Equal(t, uint32(dataencryption.VersionAESCTR), header.Version)
	require.Len(t, header.Nonce, 16)

	reader, err := svc.DecryptStream(bytes.NewReader(inner.data))
	require.NoError(t, err)
	decrypted, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)
}

func TestEncryptStoreRejectsOversizeInput(t *testing.T) {
	svc := newEncryptionService(t)
	inner := &captureAttachmentStore{}

	store, err := attachencrypt.Wrap(inner, svc)
	require.NoError(t, err)

	_, err = store.Store(context.Background(), bytes.NewReader([]byte("12345")), 4, "text/plain")
	require.EqualError(t, err, "file exceeds maximum size of 4 bytes")
	require.Empty(t, inner.data)
	require.Empty(t, inner.deletedKeys)
}

func newEncryptionService(t *testing.T) *dataencryption.Service {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.EncryptionKey = testKeyHex
	cfg.EncryptionProviders = "dek"
	svc, err := dataencryption.New(context.Background(), &cfg)
	require.NoError(t, err)
	return svc
}

type captureAttachmentStore struct {
	data        []byte
	maxSize     int64
	contentType string
	deletedKeys []string
}

func (s *captureAttachmentStore) Store(_ context.Context, data io.Reader, maxSize int64, contentType string) (*registryattach.FileStoreResult, error) {
	s.maxSize = maxSize
	s.contentType = contentType

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, data); err != nil {
		return nil, err
	}
	s.data = buf.Bytes()
	return &registryattach.FileStoreResult{
		StorageKey: "stored",
		Size:       int64(len(s.data)),
		SHA256:     "inner",
	}, nil
}

func (s *captureAttachmentStore) Retrieve(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *captureAttachmentStore) Delete(_ context.Context, storageKey string) error {
	s.deletedKeys = append(s.deletedKeys, storageKey)
	return nil
}

func (s *captureAttachmentStore) GetSignedURL(_ context.Context, _ string, _ time.Duration) (*url.URL, error) {
	return nil, fmt.Errorf("unsupported")
}
