package developer

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterRoutesDoesNotConflictWithConfigAndSPAFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testConfig(t)
	router := gin.New()

	require.NotPanics(t, func() {
		require.NoError(t, RegisterRoutes(router, cfg))
	})

	rec := performDeveloperRequest(router, "/developer/config.json")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"apiUrl": "http://memory.example",
		"auth": {
			"mode": "oidc",
			"authority": "http://keycloak.example/realms/memory-service",
			"clientId": "developer-frontend",
			"redirectUri": "http://memory.example/developer/"
		}
	}`, rec.Body.String())
	require.Equal(t, "no-cache, no-store, must-revalidate", rec.Header().Get("Cache-Control"))
}

func TestRegisterRoutesServesStaticAssetsThroughNoRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testConfig(t)
	require.NoError(t, os.MkdirAll(filepath.Join(cfg.DeveloperFrontendDir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.DeveloperFrontendDir, "assets", "app-abc12345.js"), []byte("console.log('ok')"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.DeveloperFrontendDir, "assets", "manifest.json"), []byte("{}"), 0o644))

	router := gin.New()
	require.NoError(t, RegisterRoutes(router, cfg))

	rec := performDeveloperRequest(router, "/developer/assets/app-abc12345.js")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "connect-src 'self' http://keycloak.example")
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com")
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "font-src 'self' data: https://fonts.gstatic.com")
	require.Contains(t, rec.Body.String(), "console.log")

	rec = performDeveloperRequest(router, "/developer/assets/manifest.json")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	require.JSONEq(t, `{}`, rec.Body.String())
}

func TestConfigUsesRequestOriginWhenBaseURLIsUnset(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testConfig(t)
	cfg.BaseURL = ""
	router := gin.New()
	require.NoError(t, RegisterRoutes(router, cfg))

	rec := performDeveloperRequestWithHost(router, "/developer/config.json", "localhost:49152")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"apiUrl": "http://localhost:49152",
		"auth": {
			"mode": "oidc",
			"authority": "http://keycloak.example/realms/memory-service",
			"clientId": "developer-frontend",
			"redirectUri": "http://localhost:49152/developer/"
		}
	}`, rec.Body.String())
}

func TestConfigIgnoresForwardedOriginWhenBaseURLIsUnset(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testConfig(t)
	cfg.BaseURL = ""
	router := gin.New()
	require.NoError(t, RegisterRoutes(router, cfg))

	req := httptest.NewRequest(http.MethodGet, "/developer/config.json", nil)
	req.Host = "localhost:49152"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"apiUrl": "http://localhost:49152",
		"auth": {
			"mode": "oidc",
			"authority": "http://keycloak.example/realms/memory-service",
			"clientId": "developer-frontend",
			"redirectUri": "http://localhost:49152/developer/"
		}
	}`, rec.Body.String())
}

func TestConfigReturnsAPIKeyAuthWithoutOIDCSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testConfig(t)
	cfg.DeveloperFrontendAuthMode = config.DeveloperFrontendAuthAPIKey
	cfg.DeveloperFrontendAPIKey = "local-developer-key"
	router := gin.New()
	require.NoError(t, RegisterRoutes(router, cfg))

	rec := performDeveloperRequest(router, "/developer/config.json")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"apiUrl": "http://memory.example",
		"auth": {
			"mode": "api-key",
			"apiKey": "local-developer-key",
			"clientId": "developer-frontend"
		}
	}`, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "authority")
	require.NotContains(t, rec.Body.String(), "redirectUri")
}

func TestRegisterRoutesFallsBackToSPAForExtensionlessDeveloperPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testConfig(t)
	router := gin.New()
	require.NoError(t, RegisterRoutes(router, cfg))

	for _, target := range []string{"/developer", "/developer/", "/developer/conversations/123"} {
		rec := performDeveloperRequest(router, target)
		require.Equal(t, http.StatusOK, rec.Code, target)
		require.Equal(t, "no-cache, no-store, must-revalidate", rec.Header().Get("Cache-Control"), target)
		require.Contains(t, rec.Body.String(), "<!doctype html>", target)
	}
}

func TestRegisterRoutesReturnsNotFoundForMissingAssetsAndNonDeveloperPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testConfig(t)
	router := gin.New()
	require.NoError(t, RegisterRoutes(router, cfg))

	rec := performDeveloperRequest(router, "/developer/assets/missing.js")
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = performDeveloperRequest(router, "/not-developer")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRegisterRoutesFailsWhenIndexIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		DeveloperFrontendEnabled: true,
		DeveloperFrontendDir:     t.TempDir(),
	}

	err := RegisterRoutes(gin.New(), cfg)
	require.ErrorContains(t, err, "developer frontend index.html not found")
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><div id=\"root\"></div>"), 0o644))

	return &config.Config{
		DeveloperFrontendEnabled:  true,
		DeveloperFrontendDir:      dir,
		DeveloperFrontendAuthMode: config.DeveloperFrontendAuthOIDC,
		DeveloperFrontendClientID: "developer-frontend",
		BaseURL:                   "http://memory.example/",
		OIDCIssuer:                "http://keycloak.example/realms/memory-service",
	}
}

func performDeveloperRequest(router http.Handler, target string) *httptest.ResponseRecorder {
	return performDeveloperRequestWithHost(router, target, "example.com")
}

func performDeveloperRequestWithHost(router http.Handler, target, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
