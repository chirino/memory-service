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
	svc := &Service{byID: make(map[string]encrypt.Provider)}

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

// Encrypt delegates to the primary provider.
func (s *Service) Encrypt(plaintext []byte) ([]byte, error) {
	return s.primary.Encrypt(plaintext)
}

// Decrypt routes to the provider named in the MSEH header when present. When
// "plain" is registered in the provider list, two additional cases are handled:
//
//   - Scenario 1 (migration): no MSEH header → return bytes as-is via "plain".
//     Covers data written before encryption was enabled (e.g. providers = "dek,plain"):
//     old rows have no MSEH header and must not be routed to the primary ("dek"),
//     which would fail expecting an envelope.
//
//   - Scenario 2 (magic collision): MSEH magic present but header is malformed →
//     return bytes as-is via "plain". Raw plaintext that coincidentally starts with
//     the 4-byte MSEH sentinel is treated as plain data rather than returning an error.
//
// Without "plain" in the list, the primary provider handles header-less data and
// any header parse failure is a hard error.
func (s *Service) Decrypt(ciphertext []byte) ([]byte, error) {
	plain := s.byID["plain"]

	if HasMagic(ciphertext) {
		h, _, err := ReadHeader(bytes.NewReader(ciphertext))
		if err != nil {
			// Scenario 2: magic bytes present but header is malformed.
			if plain != nil {
				return plain.Decrypt(ciphertext)
			}
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

	// No MSEH header — Scenario 1: route to "plain" when registered.
	if plain != nil {
		return plain.Decrypt(ciphertext)
	}
	return s.primary.Decrypt(ciphertext)
}

// EncryptStream delegates to the primary provider.
func (s *Service) EncryptStream(dst io.Writer) (io.WriteCloser, error) {
	return s.primary.EncryptStream(dst)
}

// DecryptStream peeks at the first 4 bytes to detect MSEH magic. If found, reads
// the full header and routes to the matching provider. The same two "plain" fallback
// scenarios as Decrypt apply here:
//
//   - Scenario 1 (migration): no MSEH magic + "plain" registered → pass stream through.
//
//   - Scenario 2 (magic collision): MSEH magic present but header is malformed +
//     "plain" registered → reconstruct the original stream (using the bytes captured
//     by recordingReader during the failed header parse) and pass it through as-is.
func (s *Service) DecryptStream(src io.Reader) (io.Reader, error) {
	plain := s.byID["plain"]

	buf := make([]byte, 4)
	n, _ := io.ReadFull(src, buf)
	peeked := buf[:n]
	combined := io.MultiReader(bytes.NewReader(peeked), src)

	if HasMagic(peeked) {
		// Wrap combined in a recordingReader so that if header parsing fails we can
		// reconstruct the original stream by prepending the already-consumed bytes.
		rec := &recordingReader{src: combined}
		h, _, err := ReadHeader(rec)
		if err != nil {
			// Scenario 2: magic present but header is malformed.
			// rec.recorded holds every byte consumed from combined so far;
			// combined still holds the unconsumed tail — together they restore the stream.
			if plain != nil {
				restored := io.MultiReader(bytes.NewReader(rec.recorded), combined)
				return plain.DecryptStream(restored, nil)
			}
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

	// No MSEH magic — Scenario 1: route to "plain" when registered.
	if plain != nil {
		return plain.DecryptStream(combined, nil)
	}
	return s.primary.DecryptStream(combined, nil)
}

// recordingReader wraps an io.Reader and records every byte read into recorded.
// Used during MSEH header parsing so that consumed bytes can be replayed if
// parsing fails (Scenario 2 magic-collision recovery in DecryptStream).
type recordingReader struct {
	src      io.Reader
	recorded []byte
}

func (r *recordingReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.recorded = append(r.recorded, p[:n]...)
	}
	return n, err
}

// AttachmentSigningKeys returns signing keys from the primary provider for attachment
// download URL HMAC signing. Returns nil when the primary provider does not support
// signed URLs (e.g. "plain" with no key configured).
func (s *Service) AttachmentSigningKeys(ctx context.Context) ([][]byte, error) {
	return s.primary.AttachmentSigningKeys(ctx)
}
