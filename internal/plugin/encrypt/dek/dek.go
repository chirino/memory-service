// Package dek registers the "dek" encryption provider.
// Byte-slice encryption uses AES-256-GCM; streamed attachment encryption uses MSEH v3 AES-GCM records.
package dek

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/registry/encrypt"
)

func init() {
	encrypt.Register(encrypt.Plugin{
		Name: "dek",
		Loader: func(_ context.Context, cfg *config.Config) (encrypt.Provider, error) {
			// EncryptionKey is CSV: first entry is primary (for encryption),
			// subsequent entries are legacy (decryption-only key rotation).
			allKeys, err := config.DecodeEncryptionKeysCSV(cfg.EncryptionKey)
			if err != nil {
				return nil, fmt.Errorf("dek provider: %w", err)
			}
			if len(allKeys) == 0 {
				return nil, fmt.Errorf("dek provider: MEMORY_SERVICE_ENCRYPTION_DEK_KEY is required")
			}
			return &dekProvider{
				primaryKey: allKeys[0],
				legacyKeys: allKeys[1:],
				cfg:        cfg,
			}, nil
		},
	})
}

type dekProvider struct {
	primaryKey []byte
	legacyKeys [][]byte
	cfg        *config.Config
}

func (p *dekProvider) ID() string { return "dek" }

// EncryptField encrypts plaintext with AES-256-GCM and MSEH v4 field AAD.
func (p *dekProvider) EncryptField(plaintext []byte, domain, identity string) ([]byte, error) {
	iv, err := randomBytes(gcmNonceSize)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(p.primaryKey)
	if err != nil {
		return nil, err
	}
	headerPrefix, err := dataencryption.EncodeHeader(dataencryption.Header{
		Version:    dataencryption.VersionFieldAESGCM,
		ProviderID: "dek",
		Nonce:      iv,
	})
	if err != nil {
		return nil, err
	}
	aad := dataencryption.FieldAAD(headerPrefix, domain, identity)
	ciphertext := gcm.Seal(nil, iv, plaintext, aad)

	var buf bytes.Buffer
	buf.Write(headerPrefix)
	buf.Write(ciphertext)
	return buf.Bytes(), nil
}

// DecryptField decrypts an MSEH v4 field ciphertext with domain/identity AAD.
func (p *dekProvider) DecryptField(ciphertext []byte, domain, identity string) ([]byte, error) {
	if !dataencryption.HasMagic(ciphertext) {
		return nil, fmt.Errorf("dek: expected MSEH envelope")
	}
	r := bytes.NewReader(ciphertext)
	h, _, err := dataencryption.ReadHeader(r)
	if err != nil {
		return nil, err
	}
	if h.Version != dataencryption.VersionFieldAESGCM {
		return nil, fmt.Errorf("dek: unsupported MSEH field version %d", h.Version)
	}
	headerPrefix := ciphertext[:len(ciphertext)-r.Len()]
	payload := make([]byte, r.Len())
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("dek: reading field ciphertext payload: %w", err)
	}
	return p.gcmOpenWithAAD(h.Nonce, payload, dataencryption.FieldAAD(headerPrefix, domain, identity))
}

func (p *dekProvider) gcmOpenWithAAD(iv, payload, aad []byte) ([]byte, error) {
	keys := append([][]byte{p.primaryKey}, p.legacyKeys...)
	var lastErr error
	for _, key := range keys {
		gcm, err := newGCM(key)
		if err != nil {
			lastErr = err
			continue
		}
		plain, err := gcm.Open(nil, iv, payload, aad)
		if err == nil {
			return plain, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("dek: decryption failed with all keys: %w", lastErr)
}

// EncryptStream writes the MSEH v3 header immediately, then returns a WriteCloser
// that writes authenticated AES-GCM records to dst.
func (p *dekProvider) EncryptStream(dst io.Writer) (io.WriteCloser, error) {
	nonce, err := NewGCMStreamNonce(p.primaryKey)
	if err != nil {
		return nil, err
	}
	if err := dataencryption.WriteHeader(dst, dataencryption.Header{
		Version:    dataencryption.VersionAttachmentStreamAESGCM,
		ProviderID: "dek",
		Nonce:      nonce,
	}); err != nil {
		return nil, err
	}
	return NewGCMStreamEncryptWriter(dst, p.primaryKey, p.ID(), nonce)
}

// DecryptStream reads ciphertext from src (already positioned after the MSEH header)
// and returns a Reader over the decrypted plaintext.
func (p *dekProvider) DecryptStream(src io.Reader, header *encrypt.Header) (io.Reader, error) {
	if header == nil {
		return nil, fmt.Errorf("dek: DecryptStream requires a parsed MSEH header")
	}
	if header.Version != dataencryption.VersionAttachmentStreamAESGCM {
		return nil, fmt.Errorf("dek: unsupported MSEH stream version %d", header.Version)
	}
	key, err := SelectGCMStreamKey(append([][]byte{p.primaryKey}, p.legacyKeys...), header.Nonce)
	if err != nil {
		return nil, err
	}
	return NewGCMStreamDecryptReader(src, key, p.ID(), header.Nonce)
}

// AttachmentSigningKeys returns HKDF-derived signing keys for attachment download
// URL token signing (primary first, then legacy rotation keys).
func (p *dekProvider) AttachmentSigningKeys(_ context.Context) ([][]byte, error) {
	return p.cfg.AttachmentSigningKeys()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func randomBytes(size int) ([]byte, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("dek: generating nonce: %w", err)
	}
	return buf, nil
}

// NewGCMNonce returns a fresh AES-GCM nonce.
func NewGCMNonce() ([]byte, error) {
	return randomBytes(gcmNonceSize)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("dek: AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("dek: GCM: %w", err)
	}
	return gcm, nil
}

// AESGCMSealWithNonceAndAAD encrypts plaintext with AES-256-GCM using the supplied IV and AAD.
func AESGCMSealWithNonceAndAAD(key, iv, plaintext, aad []byte) (usedIV, ciphertext []byte, err error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	return iv, gcm.Seal(nil, iv, plaintext, aad), nil
}

// AESGCMOpenWithAAD decrypts ciphertext using key, iv, and AAD.
func AESGCMOpenWithAAD(key, iv, ciphertext, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, iv, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("dek: AES-GCM open: %w", err)
	}
	return plain, nil
}
