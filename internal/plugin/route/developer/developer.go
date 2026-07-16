package developer

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes registers the developer frontend routes when enabled.
// Returns an error if the frontend directory or index.html is missing.
func RegisterRoutes(router *gin.Engine, cfg *config.Config) error {
	if !cfg.DeveloperFrontendEnabled {
		return nil
	}

	// Validate that the directory exists
	distDir := cfg.DeveloperFrontendDir
	if _, err := os.Stat(distDir); os.IsNotExist(err) {
		return fmt.Errorf("developer frontend directory does not exist: %s", distDir)
	}

	// Validate that index.html exists
	indexPath := filepath.Join(distDir, "index.html")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return fmt.Errorf("developer frontend index.html not found: %s", indexPath)
	}

	// Register root path redirect to developer frontend FIRST
	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/developer/")
	})

	// Register runtime config endpoint before wildcard
	router.GET("/developer/config.json", configHandler(cfg))

	// Register explicit shell paths. Gin catch-all routes cannot coexist with
	// /developer/config.json, so deeper SPA/static paths are handled by NoRoute.
	router.GET("/developer", spaHandler(distDir, cfg))
	router.GET("/developer/", spaHandler(distDir, cfg))
	router.NoRoute(func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/developer/") {
			c.Status(http.StatusNotFound)
			return
		}
		staticHandler(distDir, cfg)(c)
	})

	return nil
}

// configHandler returns the runtime configuration for the developer frontend.
func configHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		baseURL := resolveBaseURL(c, cfg)
		authMode := strings.ToLower(strings.TrimSpace(cfg.DeveloperFrontendAuthMode))
		auth := gin.H{"mode": authMode}
		if authMode == config.DeveloperFrontendAuthAPIKey {
			auth["apiKey"] = cfg.DeveloperFrontendAPIKey
			auth["clientId"] = cfg.DeveloperFrontendClientID
		} else {
			auth["authority"] = cfg.OIDCIssuer
			auth["clientId"] = cfg.DeveloperFrontendClientID
			auth["redirectUri"] = strings.TrimRight(baseURL, "/") + "/developer/"
		}

		// Set security and cache headers
		c.Header("Content-Type", "application/json")
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("X-Content-Type-Options", "nosniff")

		c.JSON(http.StatusOK, gin.H{
			"apiUrl": baseURL,
			"auth":   auth,
		})
	}
}

// resolveBaseURL determines the base URL for the developer frontend.
func resolveBaseURL(c *gin.Context, cfg *config.Config) string {
	// 1. Use explicit base URL if set
	if cfg.BaseURL != "" {
		return strings.TrimRight(cfg.BaseURL, "/")
	}

	// 2. Use the direct request origin when available. Forwarded headers are
	// intentionally ignored here; production deployments must set BaseURL.
	if c != nil && c.Request != nil {
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		host := c.Request.Host
		if host != "" {
			return fmt.Sprintf("%s://%s", scheme, host)
		}
	}

	// 3. Use advertised address if set
	if cfg.ResumerAdvertisedAddress != "" {
		scheme := "http"
		if cfg.Listener.EnableTLS {
			scheme = "https"
		}
		return fmt.Sprintf("%s://%s", scheme, cfg.ResumerAdvertisedAddress)
	}

	// 4. Fallback to listener
	scheme := "http"
	if cfg.Listener.EnableTLS {
		scheme = "https"
	}
	return fmt.Sprintf("%s://localhost:%d", scheme, cfg.Listener.Port)
}

// spaHandler serves the index.html for the root developer path.
func spaHandler(distDir string, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		indexPath := filepath.Join(distDir, "index.html")
		serveFile(c, indexPath, "index.html", cfg)
	}
}

// staticHandler serves static files with SPA fallback.
func staticHandler(distDir string, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestPath := strings.TrimPrefix(c.Request.URL.Path, "/developer")

		// Resolve and validate the asset path
		fullPath, ok := resolveAssetPath(distDir, requestPath)
		if !ok {
			log.Warn("Path traversal attempt blocked", "path", requestPath)
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// Check if file exists
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			// SPA fallback: serve index.html for extensionless paths
			if !hasFileExtension(requestPath) {
				indexPath := filepath.Join(distDir, "index.html")
				serveFile(c, indexPath, "index.html", cfg)
				return
			}
			// Return 404 for missing files with extensions
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// Serve the file
		serveFile(c, fullPath, requestPath, cfg)
	}
}

// resolveAssetPath resolves and validates an asset path, preventing directory traversal.
func resolveAssetPath(distDir, requestPath string) (string, bool) {
	clean := path.Clean("/" + requestPath)
	relative := strings.TrimPrefix(clean, "/")
	if relative == "" {
		relative = "index.html"
	}

	fullPath := filepath.Join(distDir, filepath.FromSlash(relative))
	rel, err := filepath.Rel(distDir, fullPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", false
	}
	return fullPath, true
}

// hasFileExtension checks if a path has a file extension.
func hasFileExtension(p string) bool {
	base := path.Base(p)
	return strings.Contains(base, ".")
}

// serveFile serves a file with appropriate headers.
func serveFile(c *gin.Context, fullPath, requestPath string, cfg *config.Config) {
	// Set security headers
	setSecurityHeaders(c, cfg)

	// Set cache headers
	setCacheHeaders(c, requestPath)

	// Serve the file
	c.File(fullPath)
}

// setSecurityHeaders sets security headers for static responses.
func setSecurityHeaders(c *gin.Context, cfg *config.Config) {
	// Build CSP connect-src directive
	connectSrc := "'self'"
	if cfg != nil && cfg.OIDCIssuer != "" {
		if issuerURL, err := url.Parse(cfg.OIDCIssuer); err == nil {
			oidcOrigin := fmt.Sprintf("%s://%s", issuerURL.Scheme, issuerURL.Host)
			connectSrc = fmt.Sprintf("'self' %s", oidcOrigin)
		}
	}

	csp := fmt.Sprintf(
		"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; img-src 'self' data: blob:; font-src 'self' data: https://fonts.gstatic.com; connect-src %s; frame-ancestors 'none'",
		connectSrc,
	)

	c.Header("Content-Security-Policy", csp)
	c.Header("X-Frame-Options", "DENY")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
}

// setCacheHeaders sets cache control headers based on the file type.
func setCacheHeaders(c *gin.Context, requestPath string) {
	// index.html and config.json: no-cache
	if requestPath == "index.html" || strings.HasSuffix(requestPath, "/config.json") {
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		return
	}

	// Vite emits content-hashed assets as assets/name-<hash>.ext. Only those
	// get immutable caching; other static files should revalidate.
	if isImmutableAsset(requestPath) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		return
	}

	// Other static files: no-cache
	c.Header("Cache-Control", "no-cache")
}

func isImmutableAsset(requestPath string) bool {
	rel := strings.TrimPrefix(filepath.ToSlash(requestPath), "/")
	if !strings.HasPrefix(rel, "assets/") {
		return false
	}

	base := path.Base(rel)
	ext := path.Ext(base)
	if ext == "" {
		return false
	}

	name := strings.TrimSuffix(base, ext)
	idx := strings.LastIndexByte(name, '-')
	if idx < 0 || len(name[idx+1:]) < 8 {
		return false
	}

	for _, r := range name[idx+1:] {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// Made with Bob
