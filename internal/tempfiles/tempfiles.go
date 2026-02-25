package tempfiles

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Create makes a temp file in the provided directory, creating the directory if needed.
func Create(dir string, pattern string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create temp dir %q: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	return f, nil
}

// NewDeleteOnClose wraps an open file and removes it when the reader is closed.
func NewDeleteOnClose(file *os.File) io.ReadCloser {
	return &deleteOnCloseReadCloser{
		file: file,
		path: file.Name(),
	}
}

type deleteOnCloseReadCloser struct {
	file *os.File
	path string
	once sync.Once
}

func (d *deleteOnCloseReadCloser) Read(p []byte) (int, error) {
	return d.file.Read(p)
}

func (d *deleteOnCloseReadCloser) Close() error {
	var closeErr error
	var removeErr error
	d.once.Do(func() {
		closeErr = d.file.Close()
		if err := os.Remove(d.path); err != nil && !os.IsNotExist(err) {
			removeErr = err
		}
	})
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
