package serve

import (
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestValidateStartupConfigRejectsCredentialedWildcardCORS(t *testing.T) {
	cfg := validationTestConfig()
	cfg.CORSEnabled = true
	cfg.CORSOrigins = "*"

	err := validateStartupConfig(&cfg)
	require.ErrorContains(t, err, "credentialed CORS cannot use wildcard origins")
}

func TestValidateStartupConfigRequiresDeveloperBaseURLInProd(t *testing.T) {
	cfg := validationTestConfig()
	cfg.DeveloperFrontendEnabled = true
	cfg.BaseURL = ""

	err := validateStartupConfig(&cfg)
	require.ErrorContains(t, err, "MEMORY_SERVICE_BASE_URL is required")
}

func TestValidateStartupConfigAllowsDeveloperBaseURL(t *testing.T) {
	cfg := validationTestConfig()
	cfg.DeveloperFrontendEnabled = true
	cfg.BaseURL = "https://memory.example"

	require.NoError(t, validateStartupConfig(&cfg))
}

func TestValidateStartupConfigAllowsDeveloperFallbackInTesting(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DeveloperFrontendEnabled = true
	cfg.BaseURL = ""

	require.NoError(t, validateStartupConfig(&cfg))
}

func TestParseTrustedProxyCIDRsDefaultsToTrustNone(t *testing.T) {
	trusted, err := parseTrustedProxyCIDRs("  , ")

	require.NoError(t, err)
	require.Nil(t, trusted)
}

func TestParseTrustedProxyCIDRsAcceptsIPsAndCIDRs(t *testing.T) {
	trusted, err := parseTrustedProxyCIDRs("10.0.0.1, 192.168.0.0/24, 2001:db8::1, 2001:db8::/64")

	require.NoError(t, err)
	require.Equal(t, []string{"10.0.0.1", "192.168.0.0/24", "2001:db8::1", "2001:db8::/64"}, trusted)
}

func TestValidateStartupConfigRejectsInvalidTrustedProxyCIDR(t *testing.T) {
	cfg := validationTestConfig()
	cfg.TrustedProxyCIDRs = "10.0.0.1,not-a-cidr"

	err := validateStartupConfig(&cfg)
	require.ErrorContains(t, err, "invalid MEMORY_SERVICE_TRUSTED_PROXY_CIDRS")
}

func TestValidateStartupConfigRejectsUniversalTrustedProxyCIDR(t *testing.T) {
	for _, value := range []string{"0.0.0.0/0", "::/0", "0.0.0.0", "::"} {
		cfg := validationTestConfig()
		cfg.TrustedProxyCIDRs = value

		err := validateStartupConfig(&cfg)
		require.ErrorContains(t, err, "MEMORY_SERVICE_TRUSTED_PROXY_CIDRS", value)
	}
}

func TestValidateStartupConfigRejectsPrimaryPlainInProduction(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.EncryptionProviders = "plain"

	err := validateStartupConfig(&cfg)
	require.ErrorContains(t, err, "primary encryption provider is plain")
}

func TestValidateStartupConfigAllowsPrimaryPlainWithExplicitUnsafeFlag(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.EncryptionProviders = "plain"
	cfg.EncryptionAllowPlain = true

	require.NoError(t, validateStartupConfig(&cfg))
}

func TestValidateStartupConfigAllowsPrimaryPlainInTesting(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.EncryptionProviders = "plain"

	require.NoError(t, validateStartupConfig(&cfg))
}

func TestValidateStartupConfigRejectsInvalidListenerLimits(t *testing.T) {
	t.Run("main max header too small", func(t *testing.T) {
		cfg := validationTestConfig()
		cfg.Listener.MaxHeaderBytes = 512

		err := validateStartupConfig(&cfg)
		require.ErrorContains(t, err, "main listener max header bytes")
	})

	t.Run("management idle timeout too small", func(t *testing.T) {
		cfg := validationTestConfig()
		cfg.ManagementListener.IdleTimeout = time.Millisecond

		err := validateStartupConfig(&cfg)
		require.ErrorContains(t, err, "management listener idle timeout")
	})

	t.Run("main idle timeout too large", func(t *testing.T) {
		cfg := validationTestConfig()
		cfg.Listener.IdleTimeout = 31 * time.Minute

		err := validateStartupConfig(&cfg)
		require.ErrorContains(t, err, "main listener idle timeout")
	})
}

func validationTestConfig() config.Config {
	cfg := config.DefaultConfig()
	cfg.EncryptionAllowPlain = true
	return cfg
}
