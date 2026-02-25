package config

import (
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
