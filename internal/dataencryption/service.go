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
	primary encrypt.Provider
	byID    map[string]encrypt.Provider
}

// New constructs a Service from cfg.EncryptionProviders (comma-separated list).
// The first named provider becomes the primary (used for encryption).
func New(ctx context.Context, cfg *config.Config) (*Service, error) {
	names := strings.Split(cfg.EncryptionProviders, ",")
	svc := &Service{
		byID: make(map[string]encrypt.Provider),
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

// EncryptField encrypts a persisted field with MSEH v4 domain/identity AAD binding.
func (s *Service) EncryptField(plaintext []byte, domain, identity string) ([]byte, error) {
	return s.primary.EncryptField(plaintext, domain, identity)
}

// DecryptField decrypts a persisted field. Encrypted values must use MSEH v4 and
// are authenticated with the supplied domain and identity. Headerless values are
// accepted only when plain is the primary provider.
func (s *Service) DecryptField(ciphertext []byte, domain, identity string) ([]byte, error) {
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
			return nil, fmt.Errorf("dataencryption: unsupported MSEH v%d field envelope", h.Version)
		}
		return provider.DecryptField(ciphertext, domain, identity)
	}
	if s.primary.ID() != "plain" {
		return nil, fmt.Errorf("dataencryption: expected MSEH v%d field envelope", VersionFieldAESGCM)
	}
	return s.primary.DecryptField(ciphertext, domain, identity)
}

// EncryptStream delegates to the primary provider.
func (s *Service) EncryptStream(dst io.Writer) (io.WriteCloser, error) {
	return s.primary.EncryptStream(dst)
}

// DecryptStream routes current MSEH v3 streams by provider. Headerless streams
// are accepted only when plain is the primary provider.
func (s *Service) DecryptStream(src io.Reader) (io.Reader, error) {
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
		if h.Version != VersionAttachmentStreamAESGCM {
			return nil, fmt.Errorf("dataencryption: unsupported MSEH v%d attachment stream", h.Version)
		}
		encHeader := &encrypt.Header{
			Version:    h.Version,
			ProviderID: h.ProviderID,
			Nonce:      h.Nonce,
		}
		// combined is now positioned at the ciphertext (header consumed via rec above)
		return provider.DecryptStream(combined, encHeader)
	}

	if s.primary.ID() != "plain" {
		return nil, fmt.Errorf("dataencryption: expected MSEH v%d attachment stream", VersionAttachmentStreamAESGCM)
	}
	return s.primary.DecryptStream(combined, nil)
}

// AttachmentSigningKeys returns signing keys from the primary provider for attachment
// download URL HMAC signing. Returns nil when the primary provider does not support
// signed URLs (e.g. "plain" with no key configured).
func (s *Service) AttachmentSigningKeys(ctx context.Context) ([][]byte, error) {
	return s.primary.AttachmentSigningKeys(ctx)
}
