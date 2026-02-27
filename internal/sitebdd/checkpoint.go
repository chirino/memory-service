//go:build site_tests

package sitebdd

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// portCounter allocates unique ports for checkpoint subprocesses.
// First call to allocatePort() returns 10090.
var portCounter atomic.Int32

func allocatePort() int {
	return 10089 + int(portCounter.Add(1))
}

// SiteScenario holds all per-scenario state for the site documentation test suite.
// One instance is created per godog scenario; no sharing across concurrent scenarios.
type SiteScenario struct {
	// Identity
	ScenarioUID string // short UUID prefix for user isolation (e.g. "a1b2c3d4")

	// Checkpoint state
	CheckpointID   string // e.g. "quarkus/examples/chat-quarkus/01-basic-agent"
	CheckpointPath string // absolute filesystem path
	CheckpointPort int    // dynamically allocated TCP port
	checkpointCmd  *exec.Cmd
	buildExitCode  int

	// OpenAI mock recording
	Recording bool
	// (journal is held inside the mock's per-scenario state, not here)

	// Last HTTP response (from curl steps)
	LastRespBody   string
	LastStatusCode int
	lastCurlReq    *curlRequest

	// Named variables set by "set X to the json response field Y" steps
	ContextVars map[string]any

	// Shared services (set once before godog runs)
	ProjectRoot   string
	MemServiceURL string
	Mock          *MockServer

	t testing.TB
}

// apiKey returns the fake API key used to route requests inside the mock.
func (s *SiteScenario) apiKey() string {
	return "sitebdd-" + s.ScenarioUID
}

// canonicalUser rewrites a canonical user name ("alice") to the scenario-isolated name.
func (s *SiteScenario) isolatedUser(name string) string {
	return name + "-" + s.ScenarioUID
}

// normalizeUsers replaces isolated user names back to canonical names in a string.
func (s *SiteScenario) normalizeUsers(text string) string {
	for _, base := range []string{"alice", "bob", "charlie"} {
		text = strings.ReplaceAll(text, base+"-"+s.ScenarioUID, base)
	}
	return text
}

// startCheckpoint builds the ProcessBuilder for the checkpoint subprocess.
func (s *SiteScenario) startCheckpoint() error {
	if s.CheckpointPath == "" {
		return fmt.Errorf("checkpoint not set; call 'Given checkpoint X is active' first")
	}

	s.Mock.RegisterScenario(s.ScenarioUID, s.CheckpointID, s.Recording)

	// Determine application type
	isPython := fileExists(filepath.Join(s.CheckpointPath, "pyproject.toml"))
	quarkusJar := filepath.Join(s.CheckpointPath, "target", "quarkus-app", "quarkus-run.jar")
	isQuarkus := !isPython && fileExists(quarkusJar)

	var cmd *exec.Cmd
	switch {
	case isPython:
		venvPython := filepath.Join(s.CheckpointPath, ".venv", "bin", "python")
		python := "python3"
		if fileExists(venvPython) {
			python = venvPython
		}
		cmd = exec.Command(python, "-m", "uvicorn", "app:app",
			"--host", "0.0.0.0", "--port", fmt.Sprintf("%d", s.CheckpointPort))

	case isQuarkus:
		cmd = exec.Command("java",
			fmt.Sprintf("-Dquarkus.http.port=%d", s.CheckpointPort),
			"-jar", quarkusJar)

	default:
		// Spring Boot — find any JAR in target/
		jar, err := findJar(filepath.Join(s.CheckpointPath, "target"))
		if err != nil {
			return fmt.Errorf("find checkpoint JAR: %w", err)
		}
		springArgs := []string{
			"-jar", jar,
			fmt.Sprintf("--server.port=%d", s.CheckpointPort),
			// Override memory service URL via highest-priority Spring command-line arg
			"--memory-service.client.base-url=" + s.MemServiceURL,
		}
		if s.checkpointHasProperty("spring.security.oauth2") {
			// Point the OAuth2 client provider (login + ClientRegistrationRepository) at the
			// mock OIDC server instead of Keycloak.
			// Point JWT resource-server validation at the mock too — Spring will fetch
			// /.well-known/openid-configuration and use the mock JWKS for signature checks.
			springArgs = append(springArgs,
				"--spring.security.oauth2.client.provider.memory-service-client.issuer-uri="+s.Mock.URL(),
				"--spring.security.oauth2.resourceserver.jwt.issuer-uri="+s.Mock.URL(),
			)
		}
		cmd = exec.Command("java", springArgs...)
	}

	cmd.Dir = s.CheckpointPath
	// Base env: OpenAI mock routing + memory service URL in all supported forms.
	cmd.Env = append(os.Environ(),
		"OPENAI_BASE_URL="+s.Mock.URL(),
		"OPENAI_API_KEY="+s.apiKey(),
		"OPENAI_MODEL=mock-gpt-markdown",
		// Memory service URL env vars for each framework
		"MEMORY_SERVICE_URL="+s.MemServiceURL,             // Python apps
		"MEMORY_SERVICE_CLIENT_URL="+s.MemServiceURL,      // Quarkus: memory-service.client.url
		"MEMORY_SERVICE_CLIENT_BASE_URL="+s.MemServiceURL, // Spring fallback (cmd arg takes precedence)
	)

	// Quarkus with OIDC: bypass Keycloak using mock introspection endpoint (no JWT needed).
	if isQuarkus && s.checkpointHasProperty("quarkus.oidc.auth-server-url") {
		cmd.Env = append(cmd.Env,
			"QUARKUS_OIDC_AUTH_SERVER_URL="+s.Mock.URL(),
			"QUARKUS_OIDC_DISCOVERY_ENABLED=false",
			"QUARKUS_OIDC_INTROSPECTION_PATH=/introspect",
		)
	}

	if s.Recording {
		realKey := os.Getenv("OPENAI_API_KEY")
		if realKey == "" {
			return fmt.Errorf("OPENAI_API_KEY must be set in recording mode")
		}
		model := os.Getenv("OPENAI_MODEL")
		if model == "" {
			model = "gpt-4o"
		}
		// Override to use real model; mock will proxy using the real key from env
		cmd.Env = appendEnv(cmd.Env, "OPENAI_MODEL="+model)
	}

	// Pipe stdout+stderr to test output
	cmd.Stdout = &prefixWriter{prefix: fmt.Sprintf("[checkpoint:%d] ", s.CheckpointPort), dst: os.Stdout}
	cmd.Stderr = cmd.Stdout

	fmt.Printf("=== Starting checkpoint %s on port %d ===\n", s.CheckpointID, s.CheckpointPort)
	fmt.Printf("    %s\n", cmd.String())

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start checkpoint process: %w", err)
	}
	s.checkpointCmd = cmd

	// Wait for port to become available (up to 90s)
	if err := waitForPort(s.CheckpointPort, 90*time.Second); err != nil {
		_ = s.killCheckpoint()
		return fmt.Errorf("checkpoint did not start on port %d: %w", s.CheckpointPort, err)
	}

	// Extra grace period for application context initialization
	fmt.Printf("[checkpoint:%d] Port open, waiting 10s for context initialization...\n", s.CheckpointPort)
	time.Sleep(10 * time.Second)
	return nil
}

// stopCheckpoint terminates the checkpoint process and optionally saves the journal.
func (s *SiteScenario) stopCheckpoint() {
	if s.checkpointCmd == nil {
		return
	}
	if s.Recording {
		journal := s.Mock.GetJournal(s.ScenarioUID)
		if err := s.Mock.SaveJournal(s.CheckpointID, journal); err != nil {
			fmt.Printf("[checkpoint] Warning: save journal: %v\n", err)
		}
	}
	s.Mock.UnregisterScenario(s.ScenarioUID)
	_ = s.killCheckpoint()
}

func (s *SiteScenario) killCheckpoint() error {
	if s.checkpointCmd == nil || s.checkpointCmd.Process == nil {
		return nil
	}
	pid := s.checkpointCmd.Process.Pid
	port := s.CheckpointPort

	_ = s.checkpointCmd.Process.Kill()
	_ = s.checkpointCmd.Wait()
	s.checkpointCmd = nil

	// Wait for the port to be released (up to 10s)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !portInUse(port) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Printf("[checkpoint] Killed PID %d (port %d)\n", pid, port)
	return nil
}

// buildCheckpoint builds the checkpoint using Maven (Java) or uv (Python).
func (s *SiteScenario) buildCheckpoint(extraArgs ...string) error {
	if s.CheckpointPath == "" {
		return fmt.Errorf("checkpoint not set")
	}

	isPython := fileExists(filepath.Join(s.CheckpointPath, "pyproject.toml"))

	var cmd *exec.Cmd
	if isPython {
		hasLock := fileExists(filepath.Join(s.CheckpointPath, "uv.lock")) ||
			fileExists(filepath.Join(s.CheckpointPath, "requirements.lock"))
		if hasLock {
			cmd = exec.Command("uv", "sync", "--frozen")
		} else {
			cmd = exec.Command("uv", "sync")
		}
		cmd.Dir = s.CheckpointPath
	} else {
		mvnw := filepath.Join(s.ProjectRoot, "mvnw")
		pom := filepath.Join(s.CheckpointPath, "pom.xml")
		args := []string{"-B", "clean", "package", "-DskipTests", "-f", pom}
		if len(extraArgs) > 0 {
			args = append(args, extraArgs...)
		}
		cmd = exec.Command(mvnw, args...)
		cmd.Dir = s.ProjectRoot
	}

	cmd.Stdout = &prefixWriter{prefix: "[build] ", dst: os.Stdout}
	cmd.Stderr = cmd.Stdout

	fmt.Printf("=== Building %s ===\n%s\n", s.CheckpointID, cmd.String())
	if err := cmd.Run(); err != nil {
		s.buildExitCode = 1
		if e, ok := err.(*exec.ExitError); ok {
			s.buildExitCode = e.ExitCode()
		}
		return nil // step returns success; "the build should succeed" asserts the exit code
	}
	s.buildExitCode = 0
	fmt.Printf("=== Build OK: %s ===\n", s.CheckpointID)
	return nil
}

// shouldRecord decides whether this checkpoint should be recorded based on
// SITE_TEST_RECORD env var and whether fixtures already exist.
func (s *SiteScenario) shouldRecord() bool {
	setting := strings.ToLower(os.Getenv("SITE_TEST_RECORD"))
	if setting == "" || setting == "false" {
		return false
	}
	if setting == "all" || setting == "force" {
		return true
	}
	// "true" → only record if no fixtures exist
	return !s.Mock.HasFixtures(s.CheckpointID)
}

// --- helpers ---

func waitForPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("port %d not reachable after %s", port, timeout)
}

func portInUse(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findJar(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read target dir %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".jar") && !strings.HasSuffix(name, "-sources.jar") {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("no JAR found in %s", dir)
}

// checkpointHasProperty returns true if the checkpoint's application.properties
// contains the given substring. Used to detect OIDC/OAuth2 configuration.
func (s *SiteScenario) checkpointHasProperty(substr string) bool {
	propsFile := filepath.Join(s.CheckpointPath, "src", "main", "resources", "application.properties")
	data, err := os.ReadFile(propsFile)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), substr)
}

// appendEnv appends or overrides an env var in a slice.
func appendEnv(env []string, kv string) []string {
	key := strings.SplitN(kv, "=", 2)[0] + "="
	for i, e := range env {
		if strings.HasPrefix(e, key) {
			env[i] = kv
			return env
		}
	}
	return append(env, kv)
}

// prefixWriter prefixes each line written to it with the given string.
type prefixWriter struct {
	prefix string
	dst    io.Writer
	buf    strings.Builder
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.buf.Write(b)
	for {
		s := p.buf.String()
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			break
		}
		line := s[:idx+1]
		p.buf.Reset()
		p.buf.WriteString(s[idx+1:])
		fmt.Fprintf(p.dst, "%s%s", p.prefix, line)
	}
	return len(b), nil
}
