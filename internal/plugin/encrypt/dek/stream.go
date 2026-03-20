package dek

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

const (
	gcmNonceSize = 12
	ctrNonceSize = aes.BlockSize
	ctrKeyIDSize = 8
)

// NewCTREncryptWriter returns a WriteCloser that encrypts bytes to dst using AES-CTR.
func NewCTREncryptWriter(dst io.Writer, key, nonce []byte) (io.WriteCloser, error) {
	stream, err := newCTR(key, nonce)
	if err != nil {
		return nil, err
	}
	return &streamWriteCloser{Writer: &cipher.StreamWriter{S: stream, W: dst}}, nil
}

// NewCTRDecryptReader returns a Reader that decrypts bytes from src using AES-CTR.
func NewCTRDecryptReader(src io.Reader, key, nonce []byte) (io.Reader, error) {
	stream, err := newCTR(key, nonce)
	if err != nil {
		return nil, err
	}
	return &cipher.StreamReader{S: stream, R: src}, nil
}

// NewCTRNonce generates a v2 stream nonce. The leading bytes encode a stable key ID
// so rotated providers can select the correct key for decryption before streaming.
func NewCTRNonce(key []byte) ([]byte, error) {
	nonce := make([]byte, ctrNonceSize)
	copy(nonce[:ctrKeyIDSize], ctrKeyID(key))
	if _, err := rand.Read(nonce[ctrKeyIDSize:]); err != nil {
		return nil, fmt.Errorf("dek: generating CTR nonce: %w", err)
	}
	return nonce, nil
}

// SelectCTRKey chooses the key encoded into a v2 stream nonce.
func SelectCTRKey(keys [][]byte, nonce []byte) ([]byte, error) {
	if len(nonce) != ctrNonceSize {
		return nil, fmt.Errorf("dek: AES-CTR nonce must be %d bytes, got %d", ctrNonceSize, len(nonce))
	}
	want := nonce[:ctrKeyIDSize]
	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		if bytes.Equal(ctrKeyID(key), want) {
			return key, nil
		}
	}
	return nil, fmt.Errorf("dek: no matching key for AES-CTR stream nonce")
}

func newCTR(key, nonce []byte) (cipher.Stream, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("dek: AES cipher: %w", err)
	}
	if len(nonce) != ctrNonceSize {
		return nil, fmt.Errorf("dek: AES-CTR nonce must be %d bytes, got %d", ctrNonceSize, len(nonce))
	}
	return cipher.NewCTR(block, nonce), nil
}

type streamWriteCloser struct {
	io.Writer
}

func (w *streamWriteCloser) Close() error { return nil }

func ctrKeyID(key []byte) []byte {
	sum := sha256.Sum256(key)
	return sum[:ctrKeyIDSize]
}
