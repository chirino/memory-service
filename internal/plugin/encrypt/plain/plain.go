// Package plain registers the "plain" no-op encryption provider.
// It passes all data through unchanged and does not write MSEH headers.
package plain

import (
	"context"
	"io"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/registry/encrypt"
)

func init() {
	encrypt.Register(encrypt.Plugin{
		Name: "plain",
		Loader: func(_ context.Context, cfg *config.Config) (encrypt.Provider, error) {
			return &plainProvider{cfg: cfg}, nil
		},
	})
}

type plainProvider struct {
	cfg *config.Config
}

func (p *plainProvider) ID() string { return "plain" }

func (p *plainProvider) Encrypt(plaintext []byte) ([]byte, error) { return plaintext, nil }

func (p *plainProvider) Decrypt(ciphertext []byte) ([]byte, error) { return ciphertext, nil }

func (p *plainProvider) EncryptStream(dst io.Writer) (io.WriteCloser, error) {
	return &nopWriteCloser{dst}, nil
}

func (p *plainProvider) DecryptStream(src io.Reader, _ *encrypt.Header) (io.Reader, error) {
	return src, nil
}

// AttachmentSigningKeys derives signing keys from cfg.EncryptionKey (HKDF-SHA256)
// when set. Returns nil when no encryption key is configured.
func (p *plainProvider) AttachmentSigningKeys(_ context.Context) ([][]byte, error) {
	return p.cfg.AttachmentSigningKeys()
}

// nopWriteCloser wraps an io.Writer and provides a no-op Close.
type nopWriteCloser struct{ io.Writer }

func (n *nopWriteCloser) Close() error { return nil }
