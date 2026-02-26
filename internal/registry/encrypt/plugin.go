package encrypt

import (
	"context"
	"fmt"
	"io"

	"github.com/chirino/memory-service/internal/config"
)

// Provider is the SPI for pluggable encryption providers.
// Each provider handles its own MSEH envelope writing on encrypt and
// accepts MSEH, legacy bare, or plaintext formats on decrypt.
type Provider interface {
	// ID returns the provider identifier written into the MSEH header (e.g. "plain", "dek", "vault").
	ID() string

	// Encrypt returns MSEH-wrapped ciphertext (or plaintext for the plain provider).
	Encrypt(plaintext []byte) ([]byte, error)

	// Decrypt accepts MSEH-wrapped ciphertext, legacy bare nonce||ciphertext, or plaintext.
	Decrypt(ciphertext []byte) ([]byte, error)

	// EncryptStream writes the MSEH header to dst then returns a WriteCloser that
	// encrypts written bytes and flushes the GCM tag on Close.
	EncryptStream(dst io.Writer) (io.WriteCloser, error)

	// DecryptStream returns a Reader that decrypts bytes from src.
	// header is the already-parsed MSEH header (read by DataEncryptionService).
	DecryptStream(src io.Reader, header *Header) (io.Reader, error)

	// AttachmentSigningKeys returns the ordered set of HMAC keys for attachment
	// download URL signing (primary first, legacy rotation keys after).
	// Returns nil if this provider does not support signed URLs.
	AttachmentSigningKeys(ctx context.Context) ([][]byte, error)
}

// Header is passed to DecryptStream after DataEncryptionService has parsed the
// MSEH envelope. Keeping it here avoids an import cycle with dataencryption.
type Header struct {
	Version    uint32
	ProviderID string
	Nonce      []byte
}

// Plugin bundles a provider name with its loader function.
type Plugin struct {
	Name   string
	Loader func(ctx context.Context, cfg *config.Config) (Provider, error)
}

var plugins []Plugin

// Register adds an encryption provider plugin.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

// Names returns all registered provider names.
func Names() []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

// Select returns the Plugin for the given name.
func Select(name string) (Plugin, error) {
	for _, p := range plugins {
		if p.Name == name {
			return p, nil
		}
	}
	return Plugin{}, fmt.Errorf("unknown encryption provider %q; registered: %v", name, Names())
}
