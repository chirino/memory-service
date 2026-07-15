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

func TestValidateStartupConfigReturnsAllDetectedProblems(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CORSEnabled = true
	cfg.CORSOrigins = "*"
	cfg.TrustedProxyCIDRs = "not-a-cidr"
	cfg.DeveloperFrontendEnabled = true
	cfg.BaseURL = ""
	cfg.Listener.MaxHeaderBytes = 512

	err := validateStartupConfig(&cfg)
	require.ErrorContains(t, err, "credentialed CORS cannot use wildcard origins")
	require.ErrorContains(t, err, "primary encryption provider is plain")
	require.ErrorContains(t, err, "invalid MEMORY_SERVICE_TRUSTED_PROXY_CIDRS")
	require.ErrorContains(t, err, "MEMORY_SERVICE_BASE_URL is required")
	require.ErrorContains(t, err, "main listener max header bytes")
}

func TestValidateStartupConfigRejectsCredentialedCORSWithoutExplicitOrigins(t *testing.T) {
	cfg := validationTestConfig()
	cfg.CORSEnabled = true
	cfg.CORSOrigins = " , "

	err := validateStartupConfig(&cfg)
	require.ErrorContains(t, err, "requires at least one explicit origin")
}

func TestValidateStartupConfigRejectsUnsafeCORSOrigins(t *testing.T) {
	for _, origin := range []string{
		"null",
		"file://tmp/index.html",
		"https://memory.example/path",
		"https://memory.example?debug=true",
		"https://memory.example#fragment",
		"https://user@memory.example",
	} {
		cfg := validationTestConfig()
		cfg.CORSEnabled = true
		cfg.CORSOrigins = origin

		err := validateStartupConfig(&cfg)
		require.Error(t, err, origin)
		require.ErrorContains(t, err, "MEMORY_SERVICE_CORS_ORIGINS", origin)
	}
}

func TestValidateStartupConfigAllowsExplicitCORSOrigins(t *testing.T) {
	cfg := validationTestConfig()
	cfg.CORSEnabled = true
	cfg.CORSOrigins = "https://memory.example,http://localhost:3000"

	require.NoError(t, validateStartupConfig(&cfg))
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

func TestValidateStartupConfigAllowsUniversalTrustedProxyCIDR(t *testing.T) {
	for _, value := range []string{"0.0.0.0/0", "::/0"} {
		cfg := validationTestConfig()
		cfg.TrustedProxyCIDRs = value

		require.NoError(t, validateStartupConfig(&cfg))
	}
}

func TestValidateStartupConfigRejectsUnspecifiedTrustedProxyIP(t *testing.T) {
	for _, value := range []string{"0.0.0.0", "::"} {
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

func TestValidateStartupConfigRejectsOIDCTLSSkipVerifyOutsideTesting(t *testing.T) {
	cfg := validationTestConfig()
	cfg.OIDCTLSSkipCertificateVerify = true

	err := validateStartupConfig(&cfg)
	require.ErrorContains(t, err, "MEMORY_SERVICE_OIDC_TLS_INSECURE_SKIP_VERIFY is not allowed")
}

func TestValidateStartupConfigAllowsOIDCTLSSkipVerifyInTesting(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.OIDCTLSSkipCertificateVerify = true

	require.NoError(t, validateStartupConfig(&cfg))
}

func TestValidateStartupConfigAllowsDemoCredentials(t *testing.T) {
	cfg := validationTestConfig()
	cfg.DBURL = "postgresql://postgres:postgres@postgresql:5432/memory_service"
	cfg.APIKeys = map[string]string{"agent-api-key-1": "agent"}
	cfg.QdrantAPIKey = "change-me"
	cfg.InfinispanPassword = "admin"
	cfg.InfinispanVectorPassword = "password"
	cfg.OpenAIAPIKey = "none"

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

	t.Run("negative body read timeout", func(t *testing.T) {
		cfg := validationTestConfig()
		cfg.BodyReadTimeout = -time.Second

		err := validateStartupConfig(&cfg)
		require.ErrorContains(t, err, "MEMORY_SERVICE_BODY_READ_TIMEOUT")
	})

	t.Run("negative attachment body read timeout", func(t *testing.T) {
		cfg := validationTestConfig()
		cfg.AttachmentBodyReadTimeout = -time.Second

		err := validateStartupConfig(&cfg)
		require.ErrorContains(t, err, "MEMORY_SERVICE_ATTACHMENT_BODY_READ_TIMEOUT")
	})
}

func TestValidateManagementRouteExposureRequiresExplicitProductionDecision(t *testing.T) {
	cfg := validationTestConfig()
	cfg.ManagementListenerEnabled = false
	cfg.ManagementOnMainListener = false

	err := validateManagementRouteExposure(&cfg)
	require.ErrorContains(t, err, "management routes require a dedicated listener")
}

func TestValidateManagementRouteExposureAllowsDedicatedListener(t *testing.T) {
	cfg := validationTestConfig()
	cfg.ManagementListenerEnabled = true

	require.NoError(t, validateManagementRouteExposure(&cfg))
}

func TestValidateManagementRouteExposureAllowsExplicitMainListener(t *testing.T) {
	cfg := validationTestConfig()
	cfg.ManagementOnMainListener = true

	require.NoError(t, validateManagementRouteExposure(&cfg))
}

func TestValidateManagementRouteExposureRejectsConflictingSelection(t *testing.T) {
	cfg := validationTestConfig()
	cfg.ManagementListenerEnabled = true
	cfg.ManagementOnMainListener = true

	err := validateManagementRouteExposure(&cfg)
	require.ErrorContains(t, err, "MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER cannot be combined")
}

func TestValidateManagementRouteExposureAllowsTestingFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting

	require.NoError(t, validateManagementRouteExposure(&cfg))
}

func TestValidateNetworkTransportRejectsDualTCPTransportsOutsideTesting(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = true

	err := validateNetworkTransportConfig(&cfg)
	require.ErrorContains(t, err, "main listener cannot enable both plaintext and TLS")
}

func TestValidateNetworkTransportAllowsSingleTCPTransport(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = false

	require.NoError(t, validateNetworkTransportConfig(&cfg))
}

func TestValidateNetworkTransportRejectsNonLoopbackPlaintextMainWithoutOptIn(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.Host = "0.0.0.0"
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = false

	err := validateNetworkTransportConfig(&cfg)
	require.ErrorContains(t, err, "MEMORY_SERVICE_ALLOW_NON_LOOPBACK_PLAINTEXT")
}

func TestValidateNetworkTransportAllowsNonLoopbackPlaintextMainWithOptIn(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.Host = "0.0.0.0"
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = false
	cfg.AllowNonLoopbackPlainText = true

	require.NoError(t, validateNetworkTransportConfig(&cfg))
}

func TestValidateNetworkTransportAllowsNonLoopbackTLSMainWithoutPlaintextOptIn(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.Host = "0.0.0.0"
	cfg.Listener.EnablePlainText = false
	cfg.Listener.EnableTLS = true

	require.NoError(t, validateNetworkTransportConfig(&cfg))
}

func TestValidateNetworkTransportAllowsDualUnixSocketTransports(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.UnixSocket = "/tmp/memory-service.sock"
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = true

	require.NoError(t, validateNetworkTransportConfig(&cfg))
}

func TestValidateNetworkTransportAllowsTestingDualTCPTransports(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting

	require.NoError(t, validateNetworkTransportConfig(&cfg))
}

func TestValidateNetworkTransportRejectsNonLoopbackManagementWithoutOptIn(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = false
	cfg.ManagementListenerEnabled = true
	cfg.ManagementListener.Host = "0.0.0.0"
	cfg.ManagementListener.EnablePlainText = true
	cfg.ManagementListener.EnableTLS = false

	err := validateNetworkTransportConfig(&cfg)
	require.ErrorContains(t, err, "MEMORY_SERVICE_MANAGEMENT_ALLOW_NON_LOOPBACK")
}

func TestValidateNetworkTransportAllowsNonLoopbackManagementWithOptIn(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = false
	cfg.ManagementListenerEnabled = true
	cfg.ManagementListener.Host = "0.0.0.0"
	cfg.ManagementListener.EnablePlainText = true
	cfg.ManagementListener.EnableTLS = false
	cfg.ManagementAllowNonLoopback = true

	require.NoError(t, validateNetworkTransportConfig(&cfg))
}

func TestValidateNetworkTransportRejectsDualManagementTCPTransports(t *testing.T) {
	cfg := validationTestConfig()
	cfg.Listener.EnablePlainText = true
	cfg.Listener.EnableTLS = false
	cfg.ManagementListenerEnabled = true
	cfg.ManagementListener.EnablePlainText = true
	cfg.ManagementListener.EnableTLS = true

	err := validateNetworkTransportConfig(&cfg)
	require.ErrorContains(t, err, "management listener cannot enable both plaintext and TLS")
}

func TestValidateStartupConfigRejectsInvalidRateLimitSyntax(t *testing.T) {
	cfg := validationTestConfig()
	cfg.RateLimitSource = "0/1m,burst=1"

	err := validateStartupConfig(&cfg)
	require.ErrorContains(t, err, "MEMORY_SERVICE_RATE_LIMIT_SOURCE")
}

func validationTestConfig() config.Config {
	cfg := config.DefaultConfig()
	cfg.EncryptionAllowPlain = true
	return cfg
}
