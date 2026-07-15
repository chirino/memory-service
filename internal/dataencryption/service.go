package dataencryption

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/registry/encrypt"
)

type contextKey struct{}

// WithContext returns a new context carrying the given Service.
func WithContext(ctx context.Context, svc *Service) context.Context {
	return context.WithValue(ctx, contextKey{}, svc)
}

// FromContext retrieves the Service from the context. Returns nil if none was set.
func FromContext(ctx context.Context) *Service {
	svc, _ := ctx.Value(contextKey{}).(*Service)
	return svc
}

// Service orchestrates encryption providers. The primary provider is used for new
// encryptions; all registered providers are available for decryption routing via
// the MSEH ProviderID field.
type Service struct {
	primary                encrypt.Provider
	byID                   map[string]encrypt.Provider
	legacyPlainReadEnabled bool
}

// New constructs a Service from cfg.EncryptionProviders (comma-separated list).
// The first named provider becomes the primary (used for encryption).
func New(ctx context.Context, cfg *config.Config) (*Service, error) {
	names := strings.Split(cfg.EncryptionProviders, ",")
	svc := &Service{
		byID:                   make(map[string]encrypt.Provider),
		legacyPlainReadEnabled: cfg.EncryptionLegacyPlainReadEnabled,
	}

	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		plugin, err := encrypt.Select(name)
		if err != nil {
			return nil, err
		}
		provider, err := plugin.Loader(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("encryption provider %q: %w", name, err)
		}
		svc.byID[provider.ID()] = provider
		if i == 0 || svc.primary == nil {
			svc.primary = provider
		}
	}

	if svc.primary == nil {
		return nil, fmt.Errorf("no encryption providers configured in MEMORY_SERVICE_ENCRYPTION_KIND")
	}
	return svc, nil
}

// IsPrimaryReal returns true when the primary provider performs actual encryption
// (i.e. is not the "plain" no-op provider).
func (s *Service) IsPrimaryReal() bool {
	return s.primary.ID() != "plain"
}

// PrimaryProviderID returns the configured primary provider ID.
func (s *Service) PrimaryProviderID() string {
	if s == nil || s.primary == nil {
		return ""
	}
	return s.primary.ID()
}

// PrimarySupportsFieldEncryption reports whether the primary provider can write
// MSEH v4 persisted-field envelopes with domain/identity AAD.
func (s *Service) PrimarySupportsFieldEncryption() bool {
	if s == nil || s.primary == nil {
		return false
	}
	_, ok := s.primary.(encrypt.FieldProvider)
	return ok
}

// Encrypt delegates to the primary provider.
func (s *Service) Encrypt(plaintext []byte) ([]byte, error) {
	return s.primary.Encrypt(plaintext)
}

// Decrypt routes to the provider named in the MSEH header when present. When
// "plain" is registered in the provider list and legacy plaintext reads are
// explicitly enabled, one additional case is handled:
//
//   - Scenario 1 (migration): no MSEH header → return bytes as-is via "plain".
//     Covers data written before encryption was enabled (e.g. providers = "dek,plain"):
//     old rows have no MSEH header and must not be routed to the primary ("dek"),
//     which would fail expecting an envelope.
//
// Malformed MSEH is always a hard error. It never falls back to plaintext.
func (s *Service) Decrypt(ciphertext []byte) ([]byte, error) {
	plain := s.byID["plain"]

	if HasMagic(ciphertext) {
		h, _, err := ReadHeader(bytes.NewReader(ciphertext))
		if err != nil {
			return nil, err
		}
		if h != nil {
			provider, ok := s.byID[h.ProviderID]
			if !ok {
				return nil, fmt.Errorf("dataencryption: unknown provider %q in MSEH header", h.ProviderID)
			}
			return provider.Decrypt(ciphertext)
		}
	}

	// No MSEH header — Scenario 1: route to "plain" only when explicitly enabled.
	if plain != nil && s.legacyPlainReadEnabled {
		return plain.Decrypt(ciphertext)
	}
	return s.primary.Decrypt(ciphertext)
}

// EncryptField encrypts a persisted field with MSEH v4 domain/identity AAD binding.
func (s *Service) EncryptField(plaintext []byte, domain, identity string) ([]byte, error) {
	provider, ok := s.primary.(encrypt.FieldProvider)
	if !ok {
		return nil, fmt.Errorf("dataencryption: primary provider %q does not support MSEH v4 field encryption", s.primary.ID())
	}
	return provider.EncryptField(plaintext, domain, identity)
}

// DecryptField decrypts a persisted field. MSEH v4 values are authenticated with
// the supplied domain and identity; legacy MSEH v1 values remain readable for migration.
func (s *Service) DecryptField(ciphertext []byte, domain, identity string) ([]byte, error) {
	plain := s.byID["plain"]

	if HasMagic(ciphertext) {
		h, _, err := ReadHeader(bytes.NewReader(ciphertext))
		if err != nil {
			return nil, err
		}
		provider, ok := s.byID[h.ProviderID]
		if !ok {
			return nil, fmt.Errorf("dataencryption: unknown provider %q in MSEH header", h.ProviderID)
		}
		if h.Version != VersionFieldAESGCM {
			return provider.Decrypt(ciphertext)
		}
		fieldProvider, ok := provider.(encrypt.FieldProvider)
		if !ok {
			return nil, fmt.Errorf("dataencryption: provider %q does not support MSEH v4 field decryption", h.ProviderID)
		}
		return fieldProvider.DecryptField(ciphertext, domain, identity)
	}

	if plain != nil && s.legacyPlainReadEnabled {
		return plain.Decrypt(ciphertext)
	}
	return s.primary.Decrypt(ciphertext)
}

// EncryptStream delegates to the primary provider.
func (s *Service) EncryptStream(dst io.Writer) (io.WriteCloser, error) {
	return s.primary.EncryptStream(dst)
}

// DecryptStream peeks at the first 4 bytes to detect MSEH magic. If found, reads
// the full header and routes to the matching provider. The same explicit legacy
// plaintext fallback as Decrypt applies here:
//
//   - Scenario 1 (migration): no MSEH magic + "plain" registered → pass stream through.
//
// Malformed MSEH is always a hard error. It never falls back to plaintext.
func (s *Service) DecryptStream(src io.Reader) (io.Reader, error) {
	plain := s.byID["plain"]

	buf := make([]byte, 4)
	n, _ := io.ReadFull(src, buf)
	peeked := buf[:n]
	combined := io.MultiReader(bytes.NewReader(peeked), src)

	if HasMagic(peeked) {
		h, _, err := ReadHeader(combined)
		if err != nil {
			return nil, err
		}
		provider, ok := s.byID[h.ProviderID]
		if !ok {
			return nil, fmt.Errorf("dataencryption: unknown provider %q in MSEH header", h.ProviderID)
		}
		encHeader := &encrypt.Header{
			Version:    h.Version,
			ProviderID: h.ProviderID,
			Nonce:      h.Nonce,
		}
		// combined is now positioned at the ciphertext (header consumed via rec above)
		return provider.DecryptStream(combined, encHeader)
	}

	// No MSEH magic — Scenario 1: route to "plain" only when explicitly enabled.
	if plain != nil && s.legacyPlainReadEnabled {
		return plain.DecryptStream(combined, nil)
	}
	return s.primary.DecryptStream(combined, nil)
}

// AttachmentSigningKeys returns signing keys from the primary provider for attachment
// download URL HMAC signing. Returns nil when the primary provider does not support
// signed URLs (e.g. "plain" with no key configured).
func (s *Service) AttachmentSigningKeys(ctx context.Context) ([][]byte, error) {
	return s.primary.AttachmentSigningKeys(ctx)
}
