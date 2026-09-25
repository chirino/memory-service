package bdd

import "github.com/chirino/memory-service/internal/config"

// testEncryptionKey is a 64-hex-char (32-byte) AES-256 key for testing.
const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// defaultBDDConfig is the starting point for every BDD server runner. It selects the
// dek provider with the fixed test key (DefaultConfig leaves plain as the primary
// provider, which writes no MSEH envelopes, so field encryption would go untested),
// acknowledges management routes on the ephemeral main listener (prod-mode startup
// requires an explicit listener choice), and turns off process-local rate limiting so
// functional runs are not order or load sensitive; rate limits are covered in
// internal/security/rate_limit_test.go.
func defaultBDDConfig() config.Config {
	cfg := config.DefaultConfig()
	cfg.EncryptionProviders = "dek"
	cfg.EncryptionKey = testEncryptionKey
	cfg.ManagementOnMainListener = true
	cfg.RateLimitMode = "off"
	return cfg
}
