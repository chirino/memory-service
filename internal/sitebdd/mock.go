//go:build site_tests

package sitebdd

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
)

// MockServer is an in-process HTTP server used during site tests.
// It acts as a stand-in for external services (OpenAI API, OIDC provider, etc.).
//
// Routing: checkpoint subprocesses are given a per-scenario API key of the form
// "sitebdd-<uid>". The mock reads the Authorization header to identify the
// active scenario and serve the appropriate recorded fixtures.
type MockServer struct {
	fixturesDir string
	projectRoot string
	server      *httptest.Server
	jwtKey      *rsa.PrivateKey // RSA key pair for JWT signing/verification

	mu       sync.RWMutex
	registry map[string]*mockScenarioState // uid → state
}

// NewMockServer creates a new mock server. Call Start() before using it.
func NewMockServer(fixturesDir, projectRoot string) *MockServer {
	return &MockServer{
		fixturesDir: fixturesDir,
		projectRoot: projectRoot,
		registry:    map[string]*mockScenarioState{},
	}
}

// Start starts the mock HTTP server.
func (m *MockServer) Start() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate RSA key: %w", err)
	}
	m.jwtKey = key

	mux := http.NewServeMux()
	// OpenAI API endpoints
	mux.HandleFunc("/v1/models", m.handleModels)
	mux.HandleFunc("/v1/chat/completions", m.handleChatCompletions)
	// OIDC mock — used by Spring (opaque token introspection) and Quarkus (OIDC discovery + introspect)
	mux.HandleFunc("/.well-known/openid-configuration", m.handleOIDCDiscovery)
	mux.HandleFunc("/introspect", m.handleIntrospect)
	mux.HandleFunc("/jwks", m.handleJWKS)
	mux.HandleFunc("/", m.handleFallback)
	m.server = httptest.NewServer(mux)
	return nil
}

// Stop stops the mock HTTP server.
func (m *MockServer) Stop() {
	if m.server != nil {
		m.server.Close()
	}
}

// URL returns the base URL of the mock server.
func (m *MockServer) URL() string {
	if m.server == nil {
		return ""
	}
	return m.server.URL
}

func (m *MockServer) handleFallback(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("[mock] Unhandled request: %s %s\n", r.Method, r.URL.Path)
	http.Error(w, "not found", 404)
}
