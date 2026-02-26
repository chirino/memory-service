package testkeycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultRealm        = "memory-service"
	defaultClientID     = "memory-service-client"
	defaultClientSecret = "change-me"
)

// Server exposes a running Keycloak test instance.
type Server struct {
	BaseURL      string
	IssuerURL    string
	DiscoveryURL string
	TokenURL     string
	Realm        string
	ClientID     string
	ClientSecret string
	httpClient   *http.Client
}

// StartKeycloak starts a disposable Keycloak container with the memory-service realm imported.
func StartKeycloak(tb testing.TB) *Server {
	tb.Helper()

	realmImportPath := resolveRealmImportPath(tb)
	ctx := context.Background()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "quay.io/keycloak/keycloak:24.0.5",
			ExposedPorts: []string{"8080/tcp"},
			Cmd:          []string{"start-dev", "--import-realm"},
			Env: map[string]string{
				"KEYCLOAK_ADMIN":          "admin",
				"KEYCLOAK_ADMIN_PASSWORD": "admin",
				"KC_HTTP_RELATIVE_PATH":   "/",
				"KC_HEALTH_ENABLED":       "true",
			},
			Files: []testcontainers.ContainerFile{
				{
					HostFilePath:      realmImportPath,
					ContainerFilePath: "/opt/keycloak/data/import/memory-service-realm.json",
					FileMode:          0o644,
				},
			},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("8080/tcp"),
				wait.ForHTTP("/health/ready").WithPort("8080/tcp"),
			).WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		tb.Fatalf("start keycloak container: %v", err)
	}

	tb.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := container.Terminate(termCtx); err != nil {
			tb.Errorf("terminate keycloak container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		tb.Fatalf("get keycloak host: %v", err)
	}
	port, err := container.MappedPort(ctx, "8080")
	if err != nil {
		tb.Fatalf("get keycloak mapped port: %v", err)
	}

	baseURL := fmt.Sprintf("http://%s:%s", host, port.Port())
	server := &Server{
		BaseURL:      baseURL,
		IssuerURL:    baseURL + "/realms/" + defaultRealm,
		DiscoveryURL: baseURL + "/realms/" + defaultRealm,
		TokenURL:     baseURL + "/realms/" + defaultRealm + "/protocol/openid-connect/token",
		Realm:        defaultRealm,
		ClientID:     defaultClientID,
		ClientSecret: defaultClientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}

	if err := server.waitUntilReady(ctx); err != nil {
		tb.Fatalf("keycloak token endpoint not ready: %v", err)
	}
	return server
}

// AccessToken gets an OIDC access token using password grant for a Keycloak realm user.
func (s *Server) AccessToken(ctx context.Context, username, password string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", s.ClientID)
	form.Set("client_secret", s.ClientSecret)
	form.Set("username", username)
	form.Set("password", password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := tr.Error
		if tr.Description != "" {
			msg += ": " + tr.Description
		}
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, msg)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token response missing access_token")
	}
	return tr.AccessToken, nil
}

func (s *Server) waitUntilReady(ctx context.Context) error {
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := s.AccessToken(reqCtx, "alice", "alice")
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(1 * time.Second)
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return lastErr
}

func resolveRealmImportPath(tb testing.TB) string {
	tb.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatalf("resolve keycloak realm import path: runtime caller failed")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "deploy", "keycloak", "memory-service-realm.json"))
	if _, err := os.Stat(path); err != nil {
		tb.Fatalf("resolve keycloak realm import path: %v", err)
	}
	return path
}
