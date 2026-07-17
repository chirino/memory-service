package bdd

import "github.com/chirino/memory-service/internal/config"

// testEncryptionKey is a 64-hex-char (32-byte) AES-256 key for testing.
const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func defaultBDDConfig() config.Config {
	cfg := config.DefaultConfig()
	cfg.EncryptionProviders = "dek"
	cfg.EncryptionKey = testEncryptionKey
	cfg.ManagementOnMainListener = true
	cfg.RateLimitMode = "off"
	return cfg
}
