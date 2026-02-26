package config

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// DecodeEncryptionKey supports both legacy hex keys and Java-style base64 keys.
func DecodeEncryptionKey(raw string) ([]byte, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("encryption key is empty")
	}
	if b, err := hex.DecodeString(value); err == nil && validAESKeyLen(len(b)) {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(value); err == nil && validAESKeyLen(len(b)) {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(value); err == nil && validAESKeyLen(len(b)) {
		return b, nil
	}
	return nil, fmt.Errorf("key must be hex or base64 encoded 16/24/32-byte value")
}

// DecodeEncryptionKeysCSV parses comma-separated encryption keys.
func DecodeEncryptionKeysCSV(raw string) ([][]byte, error) {
	parts := strings.Split(raw, ",")
	result := make([][]byte, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, err := DecodeEncryptionKey(part)
		if err != nil {
			return nil, err
		}
		result = append(result, key)
	}
	return result, nil
}

func validAESKeyLen(n int) bool {
	return n == 16 || n == 24 || n == 32
}

// AttachmentSigningKey returns the HMAC key used to sign new attachment download tokens.
// A domain-specific 32-byte key is derived from EncryptionKey via HKDF-SHA256.
// Returns (nil, nil) when EncryptionKey is not set — download token signing is disabled.
func (c *Config) AttachmentSigningKey() ([]byte, error) {
	if c.EncryptionKey == "" {
		return nil, nil
	}
	return deriveTokenSigningKey(c.EncryptionKey)
}

// AttachmentSigningKeys returns all signing keys for token verification, supporting rolling
// key rotation. The primary key (derived from EncryptionKey) is first; keys derived from
// EncryptionDecryptionKeys follow so tokens signed under old keys remain valid during rotation.
// Returns (nil, nil) when EncryptionKey is not set.
func (c *Config) AttachmentSigningKeys() ([][]byte, error) {
	primary, err := c.AttachmentSigningKey()
	if err != nil || primary == nil {
		return nil, err
	}
	keys := [][]byte{primary}
	legacyRaws, err := DecodeEncryptionKeysCSV(c.EncryptionDecryptionKeys)
	if err != nil {
		return nil, fmt.Errorf("invalid decryption key list: %w", err)
	}
	for _, raw := range legacyRaws {
		legacyKey, legacyErr := hkdf.Key(sha256.New, raw, nil, "attachment-download-tokens", 32)
		if legacyErr != nil {
			return nil, fmt.Errorf("HKDF derivation failed for legacy key: %w", legacyErr)
		}
		keys = append(keys, legacyKey)
	}
	return keys, nil
}

func deriveTokenSigningKey(encryptionKey string) ([]byte, error) {
	raw, err := DecodeEncryptionKey(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("cannot derive attachment signing key from encryption key: %w", err)
	}
	key, err := hkdf.Key(sha256.New, raw, nil, "attachment-download-tokens", 32)
	if err != nil {
		return nil, fmt.Errorf("HKDF derivation failed: %w", err)
	}
	return key, nil
}
