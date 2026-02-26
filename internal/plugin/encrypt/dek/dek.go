// Package dek registers the "dek" AES-256-GCM encryption provider.
// Ciphertext is wrapped in an MSEH envelope for Java interoperability.
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

// Encrypt encrypts plaintext with AES-256-GCM and wraps it in an MSEH envelope.
func (p *dekProvider) Encrypt(plaintext []byte) ([]byte, error) {
	iv, err := randomIV()
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(p.primaryKey)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, iv, plaintext, nil)

	var buf bytes.Buffer
	if err := dataencryption.WriteHeader(&buf, dataencryption.Header{
		Version:    1,
		ProviderID: "dek",
		Nonce:      iv,
	}); err != nil {
		return nil, err
	}
	buf.Write(ciphertext)
	return buf.Bytes(), nil
}

// Decrypt decrypts an MSEH-wrapped ciphertext produced by Encrypt.
func (p *dekProvider) Decrypt(ciphertext []byte) ([]byte, error) {
	if !dataencryption.HasMagic(ciphertext) {
		return nil, fmt.Errorf("dek: expected MSEH envelope")
	}
	return p.decryptMSEH(ciphertext)
}

func (p *dekProvider) decryptMSEH(ciphertext []byte) ([]byte, error) {
	r := bytes.NewReader(ciphertext)
	h, _, err := dataencryption.ReadHeader(r)
	if err != nil {
		return nil, err
	}
	payload := make([]byte, r.Len())
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("dek: reading ciphertext payload: %w", err)
	}
	return p.gcmOpen(h.Nonce, payload)
}

// gcmOpen tries decrypting payload+nonce with the primary key then all legacy keys.
func (p *dekProvider) gcmOpen(iv, payload []byte) ([]byte, error) {
	keys := append([][]byte{p.primaryKey}, p.legacyKeys...)
	var lastErr error
	for _, key := range keys {
		gcm, err := newGCM(key)
		if err != nil {
			lastErr = err
			continue
		}
		plain, err := gcm.Open(nil, iv, payload, nil)
		if err == nil {
			return plain, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("dek: decryption failed with all keys: %w", lastErr)
}

// EncryptStream writes the MSEH header immediately (IV is pre-generated), then
// returns a WriteCloser that buffers plaintext and seals it on Close.
// AES-GCM requires all plaintext before computing the auth tag, so buffering is
// unavoidable — this matches Java's CipherOutputStream behaviour.
func (p *dekProvider) EncryptStream(dst io.Writer) (io.WriteCloser, error) {
	iv, err := randomIV()
	if err != nil {
		return nil, err
	}
	// Write MSEH header up-front so the receiver can start streaming.
	if err := dataencryption.WriteHeader(dst, dataencryption.Header{
		Version:    1,
		ProviderID: "dek",
		Nonce:      iv,
	}); err != nil {
		return nil, err
	}
	return &gcmEncryptWriter{
		dst: dst,
		key: p.primaryKey,
		iv:  iv,
	}, nil
}

// DecryptStream reads ciphertext from src (already positioned after the MSEH header)
// and returns a Reader over the decrypted plaintext.
func (p *dekProvider) DecryptStream(src io.Reader, header *encrypt.Header) (io.Reader, error) {
	if header == nil {
		return nil, fmt.Errorf("dek: DecryptStream requires a parsed MSEH header")
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("dek: reading ciphertext stream: %w", err)
	}
	plain, err := p.gcmOpen(header.Nonce, data)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(plain), nil
}

// AttachmentSigningKeys returns HKDF-derived signing keys for attachment download
// URL token signing (primary first, then legacy rotation keys).
func (p *dekProvider) AttachmentSigningKeys(_ context.Context) ([][]byte, error) {
	return p.cfg.AttachmentSigningKeys()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func randomIV() ([]byte, error) {
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("dek: generating nonce: %w", err)
	}
	return iv, nil
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

// AESGCMSeal encrypts plaintext with AES-256-GCM using key and a random IV.
// Returns (iv, ciphertext, error). Exported for use by KEK-backed providers.
func AESGCMSeal(key, plaintext []byte) (iv, ciphertext []byte, err error) {
	iv, err = randomIV()
	if err != nil {
		return nil, nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	return iv, gcm.Seal(nil, iv, plaintext, nil), nil
}

// AESGCMOpen decrypts ciphertext (with appended GCM tag) using key and iv.
// Exported for use by KEK-backed providers.
func AESGCMOpen(key, iv, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("dek: AES-GCM open: %w", err)
	}
	return plain, nil
}

// NewGCMEncryptWriter returns a WriteCloser that buffers plaintext and seals it
// with AES-GCM using key+iv on Close, writing the ciphertext to dst.
// The MSEH header must already have been written to dst before calling this.
// Exported for use by KEK-backed providers.
func NewGCMEncryptWriter(dst io.Writer, key, iv []byte) io.WriteCloser {
	return &gcmEncryptWriter{dst: dst, key: key, iv: iv}
}

// gcmEncryptWriter buffers plaintext and seals it with AES-GCM on Close.
// The MSEH header is already written to dst by EncryptStream before this is returned.
type gcmEncryptWriter struct {
	dst  io.Writer
	key  []byte
	iv   []byte
	buf  bytes.Buffer
	done bool
}

func (w *gcmEncryptWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

func (w *gcmEncryptWriter) Close() error {
	if w.done {
		return nil
	}
	w.done = true
	gcm, err := newGCM(w.key)
	if err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, w.iv, w.buf.Bytes(), nil)
	_, err = w.dst.Write(ciphertext)
	return err
}
