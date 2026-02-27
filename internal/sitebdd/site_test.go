//go:build site_tests

package sitebdd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/cucumber/godog"
	"github.com/stretchr/testify/require"

	// Import plugins so they register themselves via init()
	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	_ "github.com/chirino/memory-service/internal/plugin/cache/noop"
	_ "github.com/chirino/memory-service/internal/plugin/embed/disabled"
	_ "github.com/chirino/memory-service/internal/plugin/route/system"
	_ "github.com/chirino/memory-service/internal/plugin/store/postgres"
)

func TestSiteDocs(t *testing.T) {
	globalScenarioUUIDRegistry.Reset()
	globalCheckpointPathRegistry.Reset()
	t.Cleanup(func() {
		killAllActiveCheckpoints("suite cleanup")
	})

	// Find project root
	projectRoot, err := findProjectRoot()
	require.NoError(t, err, "find project root")

	// Ensure test-scenarios.json exists, running the Astro build if needed.
	scenariosFile := ensureScenariosJSON(t, projectRoot)

	// Load test scenarios
	scenarios, err := loadScenarios(scenariosFile)
	require.NoError(t, err, "load scenarios")

	// Java checkpoint docs depend on local 999-SNAPSHOT artifacts. Install them
	// once up front so parallel scenario builds can resolve dependencies reliably.
	ensureJavaCheckpointArtifacts(t, projectRoot, scenarios)

	// Python checkpoint docs depend on locally-built wheels (memory-service-langchain,
	// memory-service-langgraph). Build them once up front so uv sync can find them.
	ensurePythonPackages(t, projectRoot, scenarios)

	if len(scenarios) == 0 {
		t.Skip("no scenarios found in test-scenarios.json")
		return
	}

	t.Logf("Loaded %d scenario(s) from %s", len(scenarios), scenariosFile)

	// Generate .feature files into a temp directory
	featureDir := t.TempDir()
	require.NoError(t, generateFeatureFiles(scenarios, featureDir), "generate feature files")

	// Log generated feature file paths
	entries, _ := os.ReadDir(featureDir)
	for _, e := range entries {
		t.Logf("  Generated: %s", e.Name())
	}

	// Start in-process mock server (OpenAI + OIDC) first — memory service
	// fetches OIDC discovery at startup so the mock must be running beforehand.
	fixturesDir := filepath.Join(projectRoot, "internal", "sitebdd", "testdata", "openai-mock", "fixtures")
	mock := NewMockServer(fixturesDir, projectRoot)
	require.NoError(t, mock.Start(), "start mock server")
	t.Cleanup(mock.Stop)
	t.Logf("Mock server: %s", mock.URL())

	// Start shared Postgres + in-process memory service
	dbURL := testpg.StartPostgres(t)

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting // allows X-Client-ID header in BDD tests
	cfg.OIDCIssuer = mock.URL()   // enables JWT validation using mock JWKS
	cfg.DBURL = dbURL
	// API key used by all checkpoint frameworks (Quarkus/Spring/Python) to authenticate
	// as agent clients. Required for memory-channel access (clientID must be non-empty).
	cfg.APIKeys = map[string]string{
		"agent-api-key-1": "checkpoint-agent",
	}
	cfg.CacheType = "none"
	cfg.AttachType = "db"
	cfg.SearchSemanticEnabled = false
	cfg.SearchFulltextEnabled = true
	// Fixed 32-byte test key (hex); enables HMAC-signed attachment download URLs.
	cfg.EncryptionKey = "0000000000000000000000000000000000000000000000000000000000000000"
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	ctx := config.WithContext(context.Background(), &cfg)
	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err, "start memory service")
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	memServiceURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	t.Logf("Memory service: %s", memServiceURL)

	// Build godog options
	opts := godog.Options{
		Format:      "progress",
		Paths:       []string{featureDir},
		Concurrency: runtime.NumCPU(),
		Randomize:   0, // deterministic order for reproducibility
	}

	// Pretty output when -v is passed
	if testing.Verbose() {
		opts.Format = "pretty"
	}

	// Pass through godog tag filters via GODOG_TAGS env var
	// e.g. GODOG_TAGS=@quarkus go test -tags=site_tests ./internal/sitebdd/
	if tags := os.Getenv("GODOG_TAGS"); tags != "" {
		opts.Tags = tags
	}

	opts.TestingT = t

	// Apply JUnit XML reporting if GODOG_REPORT_DIR is set
	if reportDir := os.Getenv("GODOG_REPORT_DIR"); reportDir != "" {
		_ = os.MkdirAll(reportDir, 0o755)
		xmlPath := filepath.Join(reportDir, "site-docs.xml")
		if f, fErr := os.Create(xmlPath); fErr == nil {
			opts.Output = f
			opts.Format = "junit"
			t.Cleanup(func() { _ = f.Close() })
		}
	}

	status := godog.TestSuite{
		Name:    "site-docs",
		Options: &opts,
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &SiteScenario{
				ContextVars:   map[string]any{},
				ProjectRoot:   projectRoot,
				MemServiceURL: memServiceURL,
				Mock:          mock,
				t:             t,
			}
			registerCheckpointSteps(sc, s)
			registerCurlSteps(sc, s)
		},
	}.Run()

	if status != 0 {
		t.Fail()
	}
}

// ensureScenariosJSON returns the path to test-scenarios.json, running the
// Astro site build automatically if the file does not exist yet.
// The build runs once per test binary invocation; subsequent calls are no-ops
// because the file will exist after the first build.
func ensureScenariosJSON(t *testing.T, projectRoot string) string {
	t.Helper()

	path, err := findScenariosJSON(projectRoot)
	if err == nil {
		return path // already exists
	}

	// File missing — run the Astro build.
	siteDir := filepath.Join(projectRoot, "site")
	t.Logf("test-scenarios.json not found; running Astro build in %s ...", siteDir)

	runCmd := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = siteDir
		if err := runBuildCommand(t, cmd, "[npm] "); err != nil {
			t.Fatalf("site build command %q failed: %v\n"+
				"Fix the error above, or set SITE_TESTS_JSON to point to an existing test-scenarios.json.",
				name+" "+fmt.Sprint(args), err)
		}
	}

	// Always install deps first (fast no-op when already current, safe when stale).
	runCmd("npm", "install")
	runCmd("npm", "run", "build")

	// Re-resolve after the build
	path, err = findScenariosJSON(projectRoot)
	if err != nil {
		t.Fatalf("test-scenarios.json still missing after site build: %v", err)
	}
	return path
}

func ensureJavaCheckpointArtifacts(t *testing.T, projectRoot string, scenarios []ScenarioData) {
	t.Helper()
	needsJava := false
	for _, s := range scenarios {
		if strings.HasPrefix(s.Checkpoint, "quarkus/") || strings.HasPrefix(s.Checkpoint, "spring/") {
			needsJava = true
			break
		}
	}
	if !needsJava {
		return
	}

	mvnw := filepath.Join(projectRoot, "mvnw")
	args := []string{
		"-B", "-T", "1C",
		"-DskipTests",
		"install",
		"-pl", ":memory-service-extension-deployment,:memory-service-spring-boot-starter",
		"-am",
	}
	cmd := exec.Command(mvnw, args...)
	cmd.Dir = projectRoot

	t.Logf("Installing Java checkpoint artifacts: %s %s", mvnw, strings.Join(args, " "))
	if err := runBuildCommand(t, cmd, "[mvn] "); err != nil {
		t.Fatalf("failed to install Java checkpoint artifacts: %v", err)
	}
}

func ensurePythonPackages(t *testing.T, projectRoot string, scenarios []ScenarioData) {
	t.Helper()
	needsLangchain := false
	needsLanggraph := false
	for _, s := range scenarios {
		if strings.HasPrefix(s.Checkpoint, "python/examples/langchain/") {
			needsLangchain = true
		}
		if strings.HasPrefix(s.Checkpoint, "python/examples/langgraph/") {
			needsLanggraph = true
		}
	}
	runBuild := func(dir string) {
		t.Helper()
		cmd := exec.Command("uv", "--directory="+dir, "build")
		cmd.Dir = projectRoot
		cmd.Env = append(os.Environ(), "VIRTUAL_ENV=")
		t.Logf("Building Python package in %s", dir)
		if err := runBuildCommand(t, cmd, "[uv-build] "); err != nil {
			t.Fatalf("failed to build Python package in %s: %v", dir, err)
		}
	}
	if needsLangchain {
		runBuild("python/langchain")
	}
	if needsLanggraph {
		runBuild("python/langgraph")
	}
}

// testWriter adapts testing.T.Log as an io.Writer so we can stream
// subprocess output line by line during the test.
type testWriter struct {
	t      *testing.T
	prefix string
	buf    []byte
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := -1
		for i, b := range w.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		w.t.Log(w.prefix + string(w.buf[:idx]))
		w.buf = w.buf[idx+1:]
	}
	return len(p), nil
}

// Ensure testWriter satisfies io.Writer at compile time.
var _ io.Writer = (*testWriter)(nil)

func runBuildCommand(t *testing.T, cmd *exec.Cmd, prefix string) error {
	t.Helper()

	if shouldStreamBuildOutput() {
		t.Logf("  > %s", cmd.String())
		cmd.Stdout = &testWriter{t: t, prefix: prefix}
		cmd.Stderr = cmd.Stdout
		return cmd.Run()
	}

	f, err := os.CreateTemp("", "sitebdd-build-*.log")
	if err != nil {
		return fmt.Errorf("create temp build log: %w", err)
	}
	capturePath := f.Name()
	cmd.Stdout = &prefixWriter{prefix: prefix, dst: f}
	cmd.Stderr = cmd.Stdout

	runErr := cmd.Run()
	if closeErr := flushAndCloseBuildOutput(cmd.Stdout); closeErr != nil {
		if runErr == nil {
			return fmt.Errorf("flush/close build output: %w", closeErr)
		}
		fmt.Printf("[sitebdd] Warning: flush/close captured output failed: %v\n", closeErr)
	}
	defer os.Remove(capturePath)

	if runErr != nil {
		fmt.Printf("[sitebdd] Build command failed: %s\n", cmd.String())
		if shouldReplayCapturedBuildOutput() {
			if replayErr := replayBuildOutput(capturePath); replayErr != nil {
				fmt.Printf("[sitebdd] Warning: replay captured output failed: %v\n", replayErr)
			}
		}
		return runErr
	}
	return nil
}
