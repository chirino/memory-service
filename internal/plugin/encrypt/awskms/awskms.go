// Package awskms registers the "kms" encryption provider backed by AWS KMS.
// DEKs are loaded from the application database (encryption_deks table) at startup.
// AWS KMS is used only to wrap/unwrap DEKs at load time — never per-request.
package awskms

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	dekpkg "github.com/chirino/memory-service/internal/plugin/encrypt/dek"
	"github.com/chirino/memory-service/internal/plugin/encrypt/dekstore"
	"github.com/chirino/memory-service/internal/registry/encrypt"
)

func init() {
	encrypt.Register(encrypt.Plugin{
		Name: "kms",
		Loader: func(ctx context.Context, cfg *config.Config) (encrypt.Provider, error) {
			if cfg.EncryptionKMSKeyID == "" {
				return nil, fmt.Errorf("kms provider: MEMORY_SERVICE_ENCRYPTION_KMS_KEY_ID is required")
			}
			awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
			if err != nil {
				return nil, fmt.Errorf("kms provider: loading AWS config: %w", err)
			}
			return &kmsProvider{
				kmsClient: kms.NewFromConfig(awsCfg),
				keyID:     cfg.EncryptionKMSKeyID,
				cfg:       cfg,
			}, nil
		},
	})
}

type kmsProvider struct {
	kmsClient *kms.Client
	keyID     string
	cfg       *config.Config

	once    sync.Once
	mu      sync.RWMutex // protects keys
	keys    [][]byte     // keys[0]=primary, keys[1:]=legacy; newest DB row first
	loadErr error
}

func (p *kmsProvider) ID() string { return "kms" }

// load fetches all wrapped DEKs from the DB for provider="kms", unwraps each via
// KMS Decrypt, and caches the plaintext DEKs. On first start (empty table) a new
// random DEK is generated, wrapped, and inserted. Called exactly once via sync.Once.
func (p *kmsProvider) load(ctx context.Context) {
	keys, err := p.loadFromDB(ctx, true /* bootstrapIfEmpty */)
	if err != nil {
		p.loadErr = err
		return
	}
	p.mu.Lock()
	p.keys = keys
	p.mu.Unlock()
}

// loadFromDB reads the DEK record for provider="kms" from the DB and returns
// the unwrapped plaintext keys (index 0 = primary, rest = legacy).
// If bootstrapIfEmpty is true and no record exists, a new DEK is generated,
// wrapped via KMS Encrypt, and written with Bootstrap (INSERT ON CONFLICT DO
// NOTHING). If another instance wins the race the re-read returns their record.
func (p *kmsProvider) loadFromDB(ctx context.Context, bootstrapIfEmpty bool) ([][]byte, error) {
	store, err := dekstore.New(p.cfg)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	rec, err := store.Load(ctx, "kms")
	if err != nil {
		return nil, err
	}

	if rec == nil && bootstrapIfEmpty {
		// No record yet: generate a fresh DEK, wrap it, attempt INSERT.
		plain := make([]byte, 32)
		if _, err := rand.Read(plain); err != nil {
			return nil, fmt.Errorf("kms: generating DEK: %w", err)
		}
		wrapped, err := p.kmsEncrypt(ctx, plain)
		if err != nil {
			return nil, fmt.Errorf("kms: wrapping new DEK: %w", err)
		}
		// ON CONFLICT (provider) DO NOTHING — if another instance beat us, re-read.
		if err := store.Bootstrap(ctx, "kms", wrapped); err != nil {
			return nil, err
		}
		rec, err = store.Load(ctx, "kms")
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, fmt.Errorf("kms: no DEK record found after bootstrap")
		}
	}

	if rec == nil {
		return nil, nil
	}

	keys := make([][]byte, 0, len(rec.WrappedDEKs))
	for _, w := range rec.WrappedDEKs {
		plain, err := p.kmsDecrypt(ctx, w)
		if err != nil {
			return nil, fmt.Errorf("kms: unwrap DEK from DB: %w", err)
		}
		keys = append(keys, plain)
	}
	return keys, nil
}

// refreshKeys re-reads the DB and updates the cached key list under the write lock.
// Called when gcmOpen fails with all current keys to handle a rotated primary DEK.
func (p *kmsProvider) refreshKeys(ctx context.Context) error {
	keys, err := p.loadFromDB(ctx, false /* don't bootstrap */)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	p.mu.Lock()
	p.keys = keys
	p.mu.Unlock()
	return nil
}

func (p *kmsProvider) ensureLoaded() error {
	p.once.Do(func() { p.load(context.Background()) })
	return p.loadErr
}

// currentKeys returns a snapshot of the plaintext key list under the read lock.
func (p *kmsProvider) currentKeys() [][]byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([][]byte, len(p.keys))
	copy(result, p.keys)
	return result
}

// Encrypt encrypts plaintext with the primary DEK using AES-256-GCM + MSEH envelope.
func (p *kmsProvider) Encrypt(plaintext []byte) ([]byte, error) {
	if err := p.ensureLoaded(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	pk := p.keys[0]
	p.mu.RUnlock()

	iv, ciphertext, err := dekpkg.AESGCMSeal(pk, plaintext)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := dataencryption.WriteHeader(&buf, dataencryption.Header{
		Version:    1,
		ProviderID: "kms",
		Nonce:      iv,
	}); err != nil {
		return nil, err
	}
	buf.Write(ciphertext)
	return buf.Bytes(), nil
}

// Decrypt unwraps MSEH-wrapped ciphertext using the cached DEKs (primary first, then legacy).
func (p *kmsProvider) Decrypt(ciphertext []byte) ([]byte, error) {
	if err := p.ensureLoaded(); err != nil {
		return nil, err
	}
	if !dataencryption.HasMagic(ciphertext) {
		return nil, fmt.Errorf("kms: expected MSEH envelope")
	}
	r := bytes.NewReader(ciphertext)
	h, _, err := dataencryption.ReadHeader(r)
	if err != nil {
		return nil, err
	}
	payload := make([]byte, r.Len())
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("kms: reading ciphertext: %w", err)
	}
	return p.gcmOpen(h.Nonce, payload)
}

// EncryptStream writes the MSEH header then returns a WriteCloser that buffers
// and seals the plaintext with the primary DEK on Close.
func (p *kmsProvider) EncryptStream(dst io.Writer) (io.WriteCloser, error) {
	if err := p.ensureLoaded(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	pk := p.keys[0]
	p.mu.RUnlock()

	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("kms: generating nonce: %w", err)
	}
	if err := dataencryption.WriteHeader(dst, dataencryption.Header{
		Version:    1,
		ProviderID: "kms",
		Nonce:      iv,
	}); err != nil {
		return nil, err
	}
	return dekpkg.NewGCMEncryptWriter(dst, pk, iv), nil
}

// DecryptStream decrypts a ciphertext stream (already past the MSEH header).
func (p *kmsProvider) DecryptStream(src io.Reader, header *encrypt.Header) (io.Reader, error) {
	if err := p.ensureLoaded(); err != nil {
		return nil, err
	}
	if header == nil {
		return nil, fmt.Errorf("kms: DecryptStream requires a parsed MSEH header")
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("kms: reading ciphertext stream: %w", err)
	}
	plain, err := p.gcmOpen(header.Nonce, data)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(plain), nil
}

// AttachmentSigningKeys derives HKDF-SHA256 signing keys from all loaded plaintext DEKs.
func (p *kmsProvider) AttachmentSigningKeys(_ context.Context) ([][]byte, error) {
	if err := p.ensureLoaded(); err != nil {
		return nil, err
	}
	keys := p.currentKeys()
	result := make([][]byte, 0, len(keys))
	for _, k := range keys {
		derived, err := hkdf.Key(sha256.New, k, nil, "attachment-download-tokens", 32)
		if err != nil {
			return nil, fmt.Errorf("kms: HKDF signing key derivation: %w", err)
		}
		result = append(result, derived)
	}
	return result, nil
}

// gcmOpen tries decrypting payload with all cached keys. If all fail it refreshes
// the key cache from the DB (handles a rotated primary) and retries once.
func (p *kmsProvider) gcmOpen(iv, payload []byte) ([]byte, error) {
	if plain, err := p.tryKeys(iv, payload, p.currentKeys()); err == nil {
		return plain, nil
	}

	// Cache miss — refresh from DB and try once more.
	if refreshErr := p.refreshKeys(context.Background()); refreshErr != nil {
		return nil, fmt.Errorf("kms: decryption failed and cache refresh also failed: %w", refreshErr)
	}
	plain, err := p.tryKeys(iv, payload, p.currentKeys())
	if err != nil {
		return nil, fmt.Errorf("kms: decryption failed with all keys (after cache refresh): %w", err)
	}
	return plain, nil
}

// tryKeys attempts AES-GCM decryption with each key in order, returning the first success.
func (p *kmsProvider) tryKeys(iv, payload []byte, keys [][]byte) ([]byte, error) {
	var lastErr error
	for _, key := range keys {
		if plain, err := dekpkg.AESGCMOpen(key, iv, payload); err == nil {
			return plain, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no keys available")
	}
	return nil, lastErr
}

// ── AWS KMS helpers ───────────────────────────────────────────────────────────

// kmsEncrypt wraps plaintext via AWS KMS Encrypt.
func (p *kmsProvider) kmsEncrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	out, err := p.kmsClient.Encrypt(ctx, &kms.EncryptInput{
		KeyId:     aws.String(p.keyID),
		Plaintext: plaintext,
	})
	if err != nil {
		return nil, fmt.Errorf("kms: Encrypt: %w", err)
	}
	return out.CiphertextBlob, nil
}

// kmsDecrypt unwraps a KMS ciphertext blob back to plaintext.
func (p *kmsProvider) kmsDecrypt(ctx context.Context, wrapped []byte) ([]byte, error) {
	out, err := p.kmsClient.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob: wrapped,
		KeyId:          aws.String(p.keyID),
	})
	if err != nil {
		return nil, fmt.Errorf("kms: Decrypt: %w", err)
	}
	return out.Plaintext, nil
}
