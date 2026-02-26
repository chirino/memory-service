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
// A domain-specific 32-byte key is derived from the first (primary) key in EncryptionKey
// via HKDF-SHA256. Returns (nil, nil) when EncryptionKey is not set.
func (c *Config) AttachmentSigningKey() ([]byte, error) {
	if c.EncryptionKey == "" {
		return nil, nil
	}
	first := strings.SplitN(c.EncryptionKey, ",", 2)[0]
	return deriveTokenSigningKey(strings.TrimSpace(first))
}

// AttachmentSigningKeys returns all signing keys for token verification, supporting rolling
// key rotation. EncryptionKey is a comma-separated list; a signing key is derived from each
// entry via HKDF-SHA256, primary first. Returns (nil, nil) when EncryptionKey is not set.
func (c *Config) AttachmentSigningKeys() ([][]byte, error) {
	if c.EncryptionKey == "" {
		return nil, nil
	}
	raws, err := DecodeEncryptionKeysCSV(c.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("invalid encryption key list: %w", err)
	}
	keys := make([][]byte, 0, len(raws))
	for _, raw := range raws {
		derived, derivedErr := hkdf.Key(sha256.New, raw, nil, "attachment-download-tokens", 32)
		if derivedErr != nil {
			return nil, fmt.Errorf("HKDF derivation failed: %w", derivedErr)
		}
		keys = append(keys, derived)
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
