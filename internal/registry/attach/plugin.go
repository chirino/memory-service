package attach

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"
)

// FileStoreResult is the result of a file store operation.
type FileStoreResult struct {
	StorageKey string
	Size       int64
	SHA256     string
}

// AttachmentStore defines the interface for file storage backends.
type AttachmentStore interface {
	// Store writes file data and returns storage key, size, and SHA256.
	Store(ctx context.Context, data io.Reader, maxSize int64, contentType string) (*FileStoreResult, error)
	// Retrieve returns a reader for the stored file.
	Retrieve(ctx context.Context, storageKey string) (io.ReadCloser, error)
	// Delete removes the stored file.
	Delete(ctx context.Context, storageKey string) error
	// GetSignedURL returns a time-limited signed download URL, if supported.
	GetSignedURL(ctx context.Context, storageKey string, expiry time.Duration) (*url.URL, error)
}

// Loader creates an AttachmentStore from config.
type Loader func(ctx context.Context) (AttachmentStore, error)

// Plugin represents an attachment store plugin.
type Plugin struct {
	Name   string
	Loader Loader
}

var plugins []Plugin

// Register adds an attachment store plugin.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

// Names returns all registered attachment store plugin names.
func Names() []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

// Select returns the loader for the named attachment store plugin.
func Select(name string) (Loader, error) {
	for _, p := range plugins {
		if p.Name == name {
			return p.Loader, nil
		}
	}
	return nil, fmt.Errorf("unknown attachment store %q; valid: %v", name, Names())
}
