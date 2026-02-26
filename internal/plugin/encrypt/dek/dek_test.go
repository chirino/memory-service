package dek_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/registry/encrypt"
	"github.com/stretchr/testify/require"
)

// 32-byte AES-256 keys encoded as hex.
const testKeyHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
const legacyKeyHex = "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2"

// makeCfg builds a Config with EncryptionKey as a comma-separated key list.
// keys[0] is primary; any additional keys are legacy (decryption-only).
func makeCfg(keys ...string) *config.Config {
	return &config.Config{
		EncryptionKey: joinKeys(keys),
	}
}

func joinKeys(keys []string) string {
	result := ""
	for i, k := range keys {
		if k == "" {
			continue
		}
		if i > 0 && result != "" {
			result += ","
		}
		result += k
	}
	return result
}

func newProvider(t *testing.T, keys ...string) encrypt.Provider {
	t.Helper()
	plugin, err := encrypt.Select("dek")
	require.NoError(t, err)
	p, err := plugin.Loader(context.Background(), makeCfg(keys...))
	require.NoError(t, err)
	return p
}

// TestEncryptDecryptRoundTrip verifies basic encrypt→decrypt.
func TestEncryptDecryptRoundTrip(t *testing.T) {
	p := newProvider(t, testKeyHex)
	plaintext := []byte("hello, MSEH encryption")

	ct, err := p.Encrypt(plaintext)
	require.NoError(t, err)
	require.True(t, dataencryption.HasMagic(ct), "encrypted output must have MSEH magic")

	got, err := p.Decrypt(ct)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

// TestDecryptWithKeyRotation verifies that a ciphertext encrypted with the legacy key
// can be decrypted by a provider that has the old key as the second entry.
func TestDecryptWithKeyRotation(t *testing.T) {
	// Encrypt with the legacy key as primary.
	legacyProvider := newProvider(t, legacyKeyHex)
	plaintext := []byte("key rotation test")
	ct, err := legacyProvider.Encrypt(plaintext)
	require.NoError(t, err)

	// Decrypt with new primary + old key as legacy second entry.
	rotatedProvider := newProvider(t, testKeyHex, legacyKeyHex)
	got, err := rotatedProvider.Decrypt(ct)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

// TestEncryptStreamRoundTrip verifies EncryptStream + DecryptStream.
func TestEncryptStreamRoundTrip(t *testing.T) {
	p := newProvider(t, testKeyHex)
	plaintext := []byte("streaming encrypt/decrypt test payload")

	var encBuf bytes.Buffer
	w, err := p.EncryptStream(&encBuf)
	require.NoError(t, err)
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	require.True(t, dataencryption.HasMagic(encBuf.Bytes()))

	// DecryptStream via the service (which parses the MSEH header and routes back to dek).
	cfg := makeCfg(testKeyHex)
	cfg.EncryptionProviders = "dek"
	svc, err := dataencryption.New(context.Background(), cfg)
	require.NoError(t, err)

	reader, err := svc.DecryptStream(&encBuf)
	require.NoError(t, err)
	var plainBuf bytes.Buffer
	_, err = plainBuf.ReadFrom(reader)
	require.NoError(t, err)
	require.Equal(t, plaintext, plainBuf.Bytes())
}

// TestMSEHProviderIDField verifies the MSEH header contains provider_id="dek".
func TestMSEHProviderIDField(t *testing.T) {
	p := newProvider(t, testKeyHex)
	ct, err := p.Encrypt([]byte("probe"))
	require.NoError(t, err)

	h, hasMagic, err := dataencryption.ReadHeader(bytes.NewReader(ct))
	require.NoError(t, err)
	require.True(t, hasMagic)
	require.Equal(t, "dek", h.ProviderID)
	require.Equal(t, uint32(1), h.Version)
	require.Len(t, h.Nonce, 12)
}
