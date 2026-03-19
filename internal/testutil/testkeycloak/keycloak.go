package testkeycloak

import (
	"bytes"
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
	AdminURL     string
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
		AdminURL:     baseURL + "/admin/realms/" + defaultRealm,
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

func (s *Server) EnsureUser(ctx context.Context, username, password string, realmRoles []string) error {
	adminToken, err := s.adminAccessToken(ctx)
	if err != nil {
		return err
	}

	userID, err := s.lookupUserID(ctx, adminToken, username)
	if err != nil {
		return err
	}
	if userID == "" {
		userID, err = s.createUser(ctx, adminToken, username, password)
		if err != nil {
			return err
		}
	}

	if err := s.setUserPassword(ctx, adminToken, userID, password); err != nil {
		return err
	}
	if err := s.assignRealmRoles(ctx, adminToken, userID, realmRoles); err != nil {
		return err
	}
	return nil
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

func (s *Server) adminAccessToken(ctx context.Context) (string, error) {
	token, err := s.AccessToken(ctx, "alice", "alice")
	if err != nil {
		return "", fmt.Errorf("get realm admin token: %w", err)
	}
	return token, nil
}

func (s *Server) lookupUserID(ctx context.Context, adminToken, username string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.AdminURL+"/users?username="+url.QueryEscape(username)+"&exact=true", nil)
	if err != nil {
		return "", fmt.Errorf("build user lookup request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("lookup user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read user lookup response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("user lookup returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var users []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &users); err != nil {
		return "", fmt.Errorf("decode user lookup response: %w", err)
	}
	if len(users) == 0 {
		return "", nil
	}
	return users[0].ID, nil
}

func (s *Server) createUser(ctx context.Context, adminToken, username, password string) (string, error) {
	payload := map[string]any{
		"username":        username,
		"enabled":         true,
		"firstName":       username,
		"lastName":        "BDD",
		"email":           username + "@example.com",
		"emailVerified":   true,
		"requiredActions": []string{},
		"credentials": []map[string]any{
			{
				"type":      "password",
				"value":     password,
				"temporary": false,
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal create user payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.AdminURL+"/users", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("build create user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read create user response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		return "", fmt.Errorf("create user returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	userID, err := s.lookupUserID(ctx, adminToken, username)
	if err != nil {
		return "", err
	}
	if userID == "" {
		return "", fmt.Errorf("created user %q but could not resolve its id", username)
	}
	return userID, nil
}

func (s *Server) setUserPassword(ctx context.Context, adminToken, userID, password string) error {
	data, err := json.Marshal(map[string]any{
		"type":      "password",
		"value":     password,
		"temporary": false,
	})
	if err != nil {
		return fmt.Errorf("marshal reset password payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.AdminURL+"/users/"+userID+"/reset-password", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build reset password request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read reset password response: %w", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("reset password returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *Server) assignRealmRoles(ctx context.Context, adminToken, userID string, roleNames []string) error {
	if len(roleNames) == 0 {
		return nil
	}

	roles := make([]map[string]any, 0, len(roleNames))
	for _, roleName := range roleNames {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.AdminURL+"/roles/"+url.PathEscape(roleName), nil)
		if err != nil {
			return fmt.Errorf("build role lookup request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+adminToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("lookup role %q: %w", roleName, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read role lookup response for %q: %w", roleName, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("role lookup for %q returned %d: %s", roleName, resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var role map[string]any
		if err := json.Unmarshal(body, &role); err != nil {
			return fmt.Errorf("decode role %q: %w", roleName, err)
		}
		roles = append(roles, role)
	}

	data, err := json.Marshal(roles)
	if err != nil {
		return fmt.Errorf("marshal role mapping payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.AdminURL+"/users/"+userID+"/role-mappings/realm", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build role mapping request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("assign realm roles: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read role mapping response: %w", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("assign realm roles returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
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
