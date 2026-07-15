package serve

import (
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

	if cfg.CORSEnabled {
		origins := parseOrigins(cfg.CORSOrigins)
		if origins["*"] {
			return fmt.Errorf("credentialed CORS cannot use wildcard origins; set MEMORY_SERVICE_CORS_ORIGINS to explicit origins")
		}
	}

	if !strings.EqualFold(strings.TrimSpace(cfg.Mode), config.ModeTesting) && primaryEncryptionProvider(cfg.EncryptionProviders) == "plain" && !cfg.EncryptionAllowPlain {
		return fmt.Errorf("primary encryption provider is plain; set MEMORY_SERVICE_ENCRYPTION_KIND to dek, kms, or vault, or explicitly set MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN=true for an unsafe plaintext deployment")
	}

	if _, err := parseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs); err != nil {
		return err
	}

	if err := validateListenerLimits("main listener", cfg.Listener); err != nil {
		return err
	}
	if err := validateListenerLimits("management listener", cfg.ManagementListener); err != nil {
		return err
	}

	if cfg.DeveloperFrontendEnabled {
		baseURL := strings.TrimSpace(cfg.BaseURL)
		if strings.EqualFold(strings.TrimSpace(cfg.Mode), config.ModeProd) && baseURL == "" {
			return fmt.Errorf("MEMORY_SERVICE_BASE_URL is required when the developer frontend is enabled in production mode")
		}
		if baseURL != "" {
			parsed, err := url.Parse(baseURL)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				return fmt.Errorf("MEMORY_SERVICE_BASE_URL must be an absolute http(s) URL")
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				return fmt.Errorf("MEMORY_SERVICE_BASE_URL must use http or https")
			}
			if parsed.RawQuery != "" || parsed.Fragment != "" {
				return fmt.Errorf("MEMORY_SERVICE_BASE_URL must not include query or fragment components")
			}
		}
	}

	return nil
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
