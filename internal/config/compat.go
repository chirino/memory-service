package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ApplyJavaCompatFromEnv reads Java-style environment variables that are not
// represented by dedicated CLI flags in the Go serve command.
func (c *Config) ApplyJavaCompatFromEnv() error {
	if c == nil {
		return nil
	}

	var err error
	if err = applyBoolEnv("MEMORY_SERVICE_DB_MIGRATE_AT_START", &c.DatastoreMigrateAtStart); err != nil {
		return err
	}
	if err = applyDurationEnv("MEMORY_SERVICE_CACHE_EPOCH_TTL", &c.CacheEpochTTL); err != nil {
		return err
	}
	applyStringEnv("MEMORY_SERVICE_CACHE_REDIS_CLIENT", &c.CacheRedisClient)
	if err = applyDurationEnv("MEMORY_SERVICE_CACHE_INFINISPAN_STARTUP_TIMEOUT", &c.InfinispanStartupTimeout); err != nil {
		return err
	}
	applyStringEnv("MEMORY_SERVICE_CACHE_INFINISPAN_MEMORY_ENTRIES_CACHE_NAME", &c.InfinispanMemoryEntriesCacheName)
	applyStringEnv("MEMORY_SERVICE_CACHE_INFINISPAN_RESPONSE_RECORDINGS_CACHE_NAME", &c.InfinispanResponseRecordingsCacheName)
	if err = applyDurationEnv("MEMORY_SERVICE_RESPONSE_RESUMER_TEMP_FILE_RETENTION", &c.ResumerTempFileRetention); err != nil {
		return err
	}
	if err = applyBoolEnv("MEMORY_SERVICE_VECTOR_MIGRATE_AT_START", &c.VectorMigrateAtStart); err != nil {
		return err
	}
	if err = applyBoolEnv("MEMORY_SERVICE_SEARCH_SEMANTIC_ENABLED", &c.SearchSemanticEnabled); err != nil {
		return err
	}
	if err = applyBoolEnv("MEMORY_SERVICE_SEARCH_FULLTEXT_ENABLED", &c.SearchFulltextEnabled); err != nil {
		return err
	}

	if raw := strings.TrimSpace(os.Getenv("MEMORY_SERVICE_ATTACHMENTS_MAX_SIZE")); raw != "" {
		size, parseErr := parseMemorySize(raw)
		if parseErr != nil {
			return fmt.Errorf("invalid MEMORY_SERVICE_ATTACHMENTS_MAX_SIZE: %w", parseErr)
		}
		c.AttachmentMaxSize = size
	}
	if err = applyDurationEnv("MEMORY_SERVICE_ATTACHMENTS_DEFAULT_EXPIRES_IN", &c.AttachmentDefaultExpiresIn); err != nil {
		return err
	}
	if err = applyDurationEnv("MEMORY_SERVICE_ATTACHMENTS_MAX_EXPIRES_IN", &c.AttachmentMaxExpiresIn); err != nil {
		return err
	}
	if err = applyDurationEnv("MEMORY_SERVICE_ATTACHMENTS_CLEANUP_INTERVAL", &c.AttachmentCleanupInterval); err != nil {
		return err
	}
	if err = applyDurationEnv("MEMORY_SERVICE_ATTACHMENTS_DOWNLOAD_URL_EXPIRES_IN", &c.AttachmentDownloadURLExpiresIn); err != nil {
		return err
	}
	applyStringEnv("MEMORY_SERVICE_ATTACHMENTS_S3_PREFIX", &c.S3Prefix)
	if err = applyBoolEnv("MEMORY_SERVICE_ATTACHMENTS_S3_DIRECT_DOWNLOAD", &c.S3DirectDownload); err != nil {
		return err
	}
	applyStringEnv("MEMORY_SERVICE_ATTACHMENTS_S3_EXTERNAL_ENDPOINT", &c.S3ExternalEndpoint)

	applyStringEnv("MEMORY_SERVICE_EMBEDDING_OPENAI_MODEL_NAME", &c.OpenAIModelName)
	applyStringEnv("MEMORY_SERVICE_EMBEDDING_OPENAI_BASE_URL", &c.OpenAIBaseURL)
	if err = applyIntEnv("MEMORY_SERVICE_EMBEDDING_OPENAI_DIMENSIONS", &c.OpenAIDimensions); err != nil {
		return err
	}

	if err = applyIntEnv("MEMORY_SERVICE_VECTOR_QDRANT_PORT", &c.QdrantPort); err != nil {
		return err
	}
	applyStringEnv("MEMORY_SERVICE_VECTOR_QDRANT_HOST", &c.QdrantHost)
	applyStringEnv("MEMORY_SERVICE_VECTOR_QDRANT_COLLECTION_PREFIX", &c.QdrantCollectionPrefix)
	applyStringEnv("MEMORY_SERVICE_VECTOR_QDRANT_COLLECTION_NAME", &c.QdrantCollectionName)
	applyStringEnv("MEMORY_SERVICE_VECTOR_QDRANT_API_KEY", &c.QdrantAPIKey)
	if err = applyBoolEnv("MEMORY_SERVICE_VECTOR_QDRANT_USE_TLS", &c.QdrantUseTLS); err != nil {
		return err
	}
	if err = applyDurationEnv("MEMORY_SERVICE_VECTOR_QDRANT_STARTUP_TIMEOUT", &c.QdrantStartupTimeout); err != nil {
		return err
	}

	applyStringEnv("MEMORY_SERVICE_ROLES_INDEXER_OIDC_ROLE", &c.IndexerOIDCRole)
	if err = applyBoolEnv("MEMORY_SERVICE_CORS_ENABLED", &c.CORSEnabled); err != nil {
		return err
	}
	applyStringEnv("MEMORY_SERVICE_CORS_ORIGINS", &c.CORSOrigins)
	applyStringEnv("MEMORY_SERVICE_ENCRYPTION_KIND", &c.EncryptionProviders)
	applyStringEnv("MEMORY_SERVICE_ENCRYPTION_PROVIDER_DEK_TYPE", &c.EncryptionProviderDEKType)
	if err = applyBoolEnv("MEMORY_SERVICE_ENCRYPTION_PROVIDER_DEK_ENABLED", &c.EncryptionProviderDEKEnabled); err != nil {
		return err
	}
	applyStringEnv("MEMORY_SERVICE_ENCRYPTION_VAULT_TRANSIT_KEY", &c.EncryptionVaultTransitKey)

	// API keys: MEMORY_SERVICE_API_KEYS_<CLIENT_ID>=<key-value> (Java parity).
	c.APIKeys = loadAPIKeysFromEnv()

	return nil
}

// loadAPIKeysFromEnv scans env vars matching MEMORY_SERVICE_API_KEYS_<CLIENT_ID>=<key>[,<key>...]
// and returns a map from key value → clientId.  Comma-separated values are supported
// to match Java/Quarkus SmallRyeConfig behaviour.
func loadAPIKeysFromEnv() map[string]string {
	const prefix = "MEMORY_SERVICE_API_KEYS_"
	result := map[string]string{}
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, prefix) {
			continue
		}
		eqIdx := strings.IndexByte(env, '=')
		if eqIdx < 0 {
			continue
		}
		clientID := strings.ToLower(strings.TrimSpace(env[len(prefix):eqIdx]))
		if clientID == "" {
			continue
		}
		for _, key := range strings.Split(env[eqIdx+1:], ",") {
			keyValue := strings.TrimSpace(key)
			if keyValue == "" {
				continue
			}
			result[keyValue] = clientID
		}
	}
	return result
}

// QdrantAddress returns host:port for qdrant gRPC dialing.
func (c *Config) QdrantAddress() string {
	if c == nil {
		return "localhost:6334"
	}
	host := strings.TrimSpace(c.QdrantHost)
	port := c.QdrantPort
	if parsedHost, parsedPort, ok := splitHostPortCompat(host); ok {
		host = parsedHost
		port = parsedPort
	}
	if host == "" {
		host = "localhost"
	}
	if port <= 0 {
		port = 6334
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func splitHostPortCompat(raw string) (string, int, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", 0, false
	}

	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err == nil && strings.TrimSpace(u.Host) != "" {
			v = u.Host
		}
	}

	if host, port, err := net.SplitHostPort(v); err == nil {
		p, err := strconv.Atoi(port)
		if err == nil {
			return host, p, true
		}
	}

	idx := strings.LastIndex(v, ":")
	if idx <= 0 || idx == len(v)-1 {
		return "", 0, false
	}
	portPart := v[idx+1:]
	p, err := strconv.Atoi(portPart)
	if err != nil {
		return "", 0, false
	}
	hostPart := strings.Trim(v[:idx], "[]")
	if hostPart == "" {
		return "", 0, false
	}
	return hostPart, p, true
}

func applyStringEnv(key string, dest *string) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	*dest = raw
}

func applyIntEnv(key string, dest *int) error {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	*dest = v
	return nil
}

func applyBoolEnv(key string, dest *bool) error {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	*dest = v
	return nil
}

func applyDurationEnv(key string, dest *time.Duration) error {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	v, err := parseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	*dest = v
	return nil
}

func parseDuration(raw string) (time.Duration, error) {
	v := strings.TrimSpace(strings.ToUpper(raw))
	if v == "" {
		return 0, fmt.Errorf("empty duration")
	}

	// Go duration first (e.g. 30s, 5m).
	if d, err := time.ParseDuration(strings.ToLower(v)); err == nil {
		return d, nil
	}

	// Minimal ISO-8601 support: PT#H#M#S
	if !strings.HasPrefix(v, "PT") {
		return 0, fmt.Errorf("unsupported format %q", raw)
	}
	rest := strings.TrimPrefix(v, "PT")
	if rest == "" {
		return 0, fmt.Errorf("invalid format %q", raw)
	}
	total := time.Duration(0)
	for len(rest) > 0 {
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 || i >= len(rest) {
			return 0, fmt.Errorf("invalid format %q", raw)
		}
		n, err := strconv.Atoi(rest[:i])
		if err != nil {
			return 0, fmt.Errorf("invalid format %q", raw)
		}
		switch rest[i] {
		case 'H':
			total += time.Duration(n) * time.Hour
		case 'M':
			total += time.Duration(n) * time.Minute
		case 'S':
			total += time.Duration(n) * time.Second
		default:
			return 0, fmt.Errorf("invalid format %q", raw)
		}
		rest = rest[i+1:]
	}
	if total <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return total, nil
}

func parseMemorySize(raw string) (int64, error) {
	v := strings.TrimSpace(strings.ToUpper(raw))
	if v == "" {
		return 0, fmt.Errorf("empty size")
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(v, "KB"), strings.HasSuffix(v, "K"):
		multiplier = 1024
		v = strings.TrimSuffix(strings.TrimSuffix(v, "KB"), "K")
	case strings.HasSuffix(v, "MB"), strings.HasSuffix(v, "M"):
		multiplier = 1024 * 1024
		v = strings.TrimSuffix(strings.TrimSuffix(v, "MB"), "M")
	case strings.HasSuffix(v, "GB"), strings.HasSuffix(v, "G"):
		multiplier = 1024 * 1024 * 1024
		v = strings.TrimSuffix(strings.TrimSuffix(v, "GB"), "G")
	case strings.HasSuffix(v, "B"):
		v = strings.TrimSuffix(v, "B")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	return n * multiplier, nil
}
