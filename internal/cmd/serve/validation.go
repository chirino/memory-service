package serve

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/chirino/memory-service/internal/config"
)

func validateStartupConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("missing server config")
	}

	var problems []error
	if cfg.CORSEnabled {
		if err := validateCORSOrigins(cfg.CORSOrigins); err != nil {
			problems = append(problems, err)
		}
	}

	if !strings.EqualFold(strings.TrimSpace(cfg.Mode), config.ModeTesting) && primaryEncryptionProvider(cfg.EncryptionProviders) == "plain" && !cfg.EncryptionAllowPlain {
		problems = append(problems, fmt.Errorf("primary encryption provider is plain; set MEMORY_SERVICE_ENCRYPTION_KIND to dek, kms, or vault, or explicitly set MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN=true for an unsafe plaintext deployment"))
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Mode), config.ModeTesting) && cfg.OIDCTLSSkipCertificateVerify {
		problems = append(problems, fmt.Errorf("MEMORY_SERVICE_OIDC_TLS_INSECURE_SKIP_VERIFY is not allowed outside testing mode; install the issuer CA instead"))
	}

	if _, err := parseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs); err != nil {
		problems = append(problems, err)
	}

	if err := validateListenerLimits("main listener", cfg.Listener); err != nil {
		problems = append(problems, err)
	}
	if err := validateListenerLimits("management listener", cfg.ManagementListener); err != nil {
		problems = append(problems, err)
	}
	if cfg.BodyReadTimeout < 0 {
		problems = append(problems, fmt.Errorf("MEMORY_SERVICE_BODY_READ_TIMEOUT must not be negative"))
	}
	if cfg.AttachmentBodyReadTimeout < 0 {
		problems = append(problems, fmt.Errorf("MEMORY_SERVICE_ATTACHMENT_BODY_READ_TIMEOUT must not be negative"))
	}

	if cfg.DeveloperFrontendEnabled {
		baseURL := strings.TrimSpace(cfg.BaseURL)
		if strings.EqualFold(strings.TrimSpace(cfg.Mode), config.ModeProd) && baseURL == "" {
			problems = append(problems, fmt.Errorf("MEMORY_SERVICE_BASE_URL is required when the developer frontend is enabled in production mode"))
		} else if baseURL != "" {
			parsed, err := url.Parse(baseURL)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				problems = append(problems, fmt.Errorf("MEMORY_SERVICE_BASE_URL must be an absolute http(s) URL"))
			} else if parsed.Scheme != "http" && parsed.Scheme != "https" {
				problems = append(problems, fmt.Errorf("MEMORY_SERVICE_BASE_URL must use http or https"))
			} else if parsed.RawQuery != "" || parsed.Fragment != "" {
				problems = append(problems, fmt.Errorf("MEMORY_SERVICE_BASE_URL must not include query or fragment components"))
			}
		}
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Mode), config.ModeTesting) {
		if err := validateKnownDemoSecrets(cfg); err != nil {
			problems = append(problems, err)
		}
	}

	return errors.Join(problems...)
}

func validateKnownDemoSecrets(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	var problems []error
	if password := databaseURLPassword(cfg.DBURL); isKnownDemoSecret(password) {
		problems = append(problems, fmt.Errorf("MEMORY_SERVICE_DB_URL uses a known repository demo password; configure a generated database password"))
	}
	for keyValue := range cfg.APIKeys {
		if isKnownDemoSecret(keyValue) {
			problems = append(problems, fmt.Errorf("MEMORY_SERVICE_API_KEYS_* contains a known repository demo API key; configure generated API keys"))
			break
		}
	}
	for _, key := range strings.Split(cfg.EncryptionKey, ",") {
		if isKnownDemoSecret(key) {
			problems = append(problems, fmt.Errorf("MEMORY_SERVICE_ENCRYPTION_DEK_KEY uses known repository demo key material; configure generated encryption keys"))
			break
		}
	}
	if isKnownDemoSecret(cfg.QdrantAPIKey) {
		problems = append(problems, fmt.Errorf("MEMORY_SERVICE_VECTOR_QDRANT_API_KEY uses a known repository demo value; configure a generated Qdrant API key"))
	}
	if isKnownDemoSecret(cfg.InfinispanPassword) {
		problems = append(problems, fmt.Errorf("MEMORY_SERVICE_INFINISPAN_PASSWORD uses a known repository demo value; configure a generated Infinispan password"))
	}
	if isKnownDemoSecret(cfg.InfinispanVectorPassword) {
		problems = append(problems, fmt.Errorf("MEMORY_SERVICE_VECTOR_INFINISPAN_PASSWORD uses a known repository demo value; configure a generated Infinispan vector password"))
	}
	if isKnownDemoSecret(cfg.OpenAIAPIKey) {
		problems = append(problems, fmt.Errorf("MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY uses a placeholder value; configure a real API key or disable OpenAI embeddings"))
	}
	return errors.Join(problems...)
}

func databaseURLPassword(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || !strings.Contains(value, "://") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.User == nil {
		return ""
	}
	password, ok := parsed.User.Password()
	if !ok {
		return ""
	}
	return password
}

func isKnownDemoSecret(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return false
	}
	return knownDemoSecrets[value]
}

var knownDemoSecrets = map[string]bool{
	"0000000000000000000000000000000000000000000000000000000000000000": true,
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": true,
	"admin":           true,
	"agent-api-key-1": true,
	"change-me":       true,
	"changeme":        true,
	"minioadmin":      true,
	"mongo":           true,
	"none":            true,
	"password":        true,
	"postgres":        true,
	"secret":          true,
	"secret-key-1":    true,
}

func validateManagementRouteExposure(cfg *config.Config) error {
	if cfg == nil || strings.EqualFold(strings.TrimSpace(cfg.Mode), config.ModeTesting) {
		return nil
	}
	if cfg.ManagementListenerEnabled && cfg.ManagementOnMainListener {
		return fmt.Errorf("MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER cannot be combined with a dedicated management listener")
	}
	if !cfg.ManagementListenerEnabled && !cfg.ManagementOnMainListener {
		return fmt.Errorf("management routes require a dedicated listener outside testing mode; set MEMORY_SERVICE_MANAGEMENT_PORT, MEMORY_SERVICE_MANAGEMENT_UNIX_SOCKET, or explicitly set MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER=true")
	}
	return nil
}

func validateNetworkTransportConfig(cfg *config.Config) error {
	if cfg == nil || strings.EqualFold(strings.TrimSpace(cfg.Mode), config.ModeTesting) {
		return nil
	}
	if err := validateTCPTransportSelection("main listener", cfg.Listener); err != nil {
		return err
	}
	if isTCPListener(cfg.Listener) && cfg.Listener.EnablePlainText && !listenerHostIsLoopback(cfg.Listener.Host) && !cfg.AllowNonLoopbackPlainText {
		return fmt.Errorf("main listener host %q serves plaintext beyond loopback; set MEMORY_SERVICE_ALLOW_NON_LOOPBACK_PLAINTEXT=true only when ingress, TLS termination, or network policy restricts access", effectiveListenerHost(cfg.Listener.Host))
	}
	if cfg.ManagementListenerEnabled {
		if err := validateTCPTransportSelection("management listener", cfg.ManagementListener); err != nil {
			return err
		}
		if isTCPListener(cfg.ManagementListener) && !listenerHostIsLoopback(cfg.ManagementListener.Host) && !cfg.ManagementAllowNonLoopback {
			return fmt.Errorf("management listener host %q is not loopback; set MEMORY_SERVICE_MANAGEMENT_ALLOW_NON_LOOPBACK=true only when network policy or firewall rules restrict access", effectiveListenerHost(cfg.ManagementListener.Host))
		}
	}
	return nil
}

func validateTCPTransportSelection(name string, listener config.ListenerConfig) error {
	if !isTCPListener(listener) {
		return nil
	}
	if listener.EnablePlainText && listener.EnableTLS {
		return fmt.Errorf("%s cannot enable both plaintext and TLS on the same TCP listener; disable one of the transports", name)
	}
	return nil
}

func validateCORSOrigins(raw string) error {
	count := 0
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		count++
		if value == "*" {
			return fmt.Errorf("credentialed CORS cannot use wildcard origins; set MEMORY_SERVICE_CORS_ORIGINS to explicit http(s) origins")
		}
		if strings.EqualFold(value, "null") {
			return fmt.Errorf("credentialed CORS cannot allow the null origin; set MEMORY_SERVICE_CORS_ORIGINS to explicit http(s) origins")
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("MEMORY_SERVICE_CORS_ORIGINS entry %q must be an absolute http(s) origin", value)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("MEMORY_SERVICE_CORS_ORIGINS entry %q must use http or https", value)
		}
		if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return fmt.Errorf("MEMORY_SERVICE_CORS_ORIGINS entry %q must not include userinfo, path, query, or fragment components", value)
		}
	}
	if count == 0 {
		return fmt.Errorf("credentialed CORS requires at least one explicit origin in MEMORY_SERVICE_CORS_ORIGINS")
	}
	return nil
}

func isTCPListener(listener config.ListenerConfig) bool {
	return strings.TrimSpace(listener.UnixSocket) == ""
}

func effectiveListenerHost(raw string) string {
	host := strings.TrimSpace(raw)
	if host == "" {
		return "127.0.0.1"
	}
	return host
}

func listenerHostIsLoopback(raw string) bool {
	host := effectiveListenerHost(raw)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && addr.IsLoopback()
}

func validateListenerLimits(name string, listener config.ListenerConfig) error {
	if listener.MaxHeaderBytes > 0 && listener.MaxHeaderBytes < 1024 {
		return fmt.Errorf("%s max header bytes must be at least 1024", name)
	}
	if listener.IdleTimeout < 0 {
		return fmt.Errorf("%s idle timeout must not be negative", name)
	}
	if listener.IdleTimeout > 0 && (listener.IdleTimeout < time.Second || listener.IdleTimeout > 30*time.Minute) {
		return fmt.Errorf("%s idle timeout must be between 1s and 30m", name)
	}
	return nil
}

func primaryEncryptionProvider(raw string) string {
	for _, part := range strings.Split(raw, ",") {
		value := strings.ToLower(strings.TrimSpace(part))
		if value != "" {
			return value
		}
	}
	return ""
}

func parseTrustedProxyCIDRs(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	trusted := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if strings.Contains(value, "/") {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, fmt.Errorf("invalid MEMORY_SERVICE_TRUSTED_PROXY_CIDRS value %q: %w", value, err)
			}
			prefix = prefix.Masked()
			if prefix.Bits() == 0 {
				return nil, fmt.Errorf("MEMORY_SERVICE_TRUSTED_PROXY_CIDRS must not trust universal range %q", value)
			}
			trusted = append(trusted, prefix.String())
			continue
		}

		addr, err := netip.ParseAddr(value)
		if err != nil {
			return nil, fmt.Errorf("invalid MEMORY_SERVICE_TRUSTED_PROXY_CIDRS value %q: %w", value, err)
		}
		if addr.IsUnspecified() {
			return nil, fmt.Errorf("MEMORY_SERVICE_TRUSTED_PROXY_CIDRS must not trust unspecified address %q", value)
		}
		trusted = append(trusted, addr.String())
	}
	if len(trusted) == 0 {
		return nil, nil
	}
	return trusted, nil
}
