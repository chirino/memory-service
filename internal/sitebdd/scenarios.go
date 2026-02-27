//go:build site_tests

package sitebdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ScenarioData mirrors the JSON structure produced by the Astro build.
type ScenarioData struct {
	Checkpoint string        `json:"checkpoint"`
	SourceFile string        `json:"sourceFile"`
	Scenarios  []ScenarioCmd `json:"scenarios"`
}

type ScenarioCmd struct {
	Bash         string        `json:"bash"`
	Expectations []Expectation `json:"expectations"`
	CustomSteps  []string      `json:"customSteps"`
}

type Expectation struct {
	Type  string `json:"type"`  // "contains", "not_contains", "status_code"
	Value string `json:"value"` // expected value (always a string in JSON)
}

// loadScenarios reads the JSON file produced by the Astro site build.
func loadScenarios(path string) ([]ScenarioData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenarios file: %w", err)
	}
	var scenarios []ScenarioData
	if err := json.Unmarshal(data, &scenarios); err != nil {
		return nil, fmt.Errorf("parse scenarios JSON: %w", err)
	}
	return scenarios, nil
}

// findProjectRoot walks parent directories from this file's location until
// it finds a go.mod file.
func findProjectRoot() (string, error) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("go.mod not found walking up from %s", filepath.Dir(file))
}

// findScenariosJSON returns the path to test-scenarios.json.
// Checks SITE_TESTS_JSON env var first, then the default Astro output location.
func findScenariosJSON(projectRoot string) (string, error) {
	if p := os.Getenv("SITE_TESTS_JSON"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("SITE_TESTS_JSON=%s does not exist", p)
	}
	defaultPath := filepath.Join(projectRoot, "site", "dist", "test-scenarios.json")
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath, nil
	}
	return "", fmt.Errorf("test-scenarios.json not found at %s; run 'cd site && npm run build' first", defaultPath)
}

// generateFeatureFiles writes one .feature file per unique sourceFile into dir.
func generateFeatureFiles(scenarios []ScenarioData, dir string) error {
	// Group by sourceFile
	type group struct {
		sourceFile string
		items      []ScenarioData
	}
	var order []string
	grouped := map[string][]ScenarioData{}
	for _, s := range scenarios {
		if _, seen := grouped[s.SourceFile]; !seen {
			order = append(order, s.SourceFile)
		}
		grouped[s.SourceFile] = append(grouped[s.SourceFile], s)
	}

	for _, sourceFile := range order {
		items := grouped[sourceFile]
		featureName := deriveFeatureName(sourceFile)
		filename := sourceFileToFilename(sourceFile)

		var sb strings.Builder
		sb.WriteString("# Generated from test-scenarios.json — DO NOT EDIT\n")
		sb.WriteString(fmt.Sprintf("Feature: %s\n", featureName))

		for _, item := range items {
			if err := writeScenario(&sb, item); err != nil {
				return err
			}
		}

		outPath := filepath.Join(dir, filename)
		if err := os.WriteFile(outPath, []byte(sb.String()), 0o644); err != nil {
			return fmt.Errorf("write feature file %s: %w", outPath, err)
		}
	}
	return nil
}

func writeScenario(sb *strings.Builder, s ScenarioData) error {
	tags := deriveTags(s)
	if len(tags) > 0 {
		sb.WriteString("\n  " + strings.Join(tags, " ") + "\n")
	} else {
		sb.WriteString("\n")
	}

	framework := deriveFramework(s.SourceFile)
	featureName := deriveFeatureName(s.SourceFile)
	checkpointName := lastSegment(s.Checkpoint)
	sb.WriteString(fmt.Sprintf("  Scenario: [%s] %s - %s\n", framework, featureName, checkpointName))
	sb.WriteString(fmt.Sprintf("    # From %s\n", s.SourceFile))

	sb.WriteString(fmt.Sprintf("    Given checkpoint %q is active\n", s.Checkpoint))
	sb.WriteString("    When I build the checkpoint\n")
	sb.WriteString("    Then the build should succeed\n\n")
	sb.WriteString("    When I start the checkpoint\n")
	sb.WriteString("    Then the application should be running\n\n")

	for _, cmd := range s.Scenarios {
		if !containsCurl(cmd.Bash) {
			continue
		}
		sb.WriteString("    When I execute curl command:\n")
		sb.WriteString("      \"\"\"\n")
		for _, line := range strings.Split(strings.TrimSpace(cmd.Bash), "\n") {
			sb.WriteString("      " + strings.TrimRight(line, " \t") + "\n")
		}
		sb.WriteString("      \"\"\"\n")

		for _, exp := range cmd.Expectations {
			switch exp.Type {
			case "contains":
				sb.WriteString(fmt.Sprintf("    Then the response should contain %q\n", exp.Value))
			case "not_contains":
				sb.WriteString(fmt.Sprintf("    Then the response should not contain %q\n", exp.Value))
			case "status_code":
				sb.WriteString(fmt.Sprintf("    Then the response status should be %s\n", exp.Value))
			}
		}

		for _, step := range cmd.CustomSteps {
			if strings.TrimSpace(step) == "" {
				continue
			}
			sb.WriteString("    " + step + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("    When I stop the checkpoint\n")
	return nil
}

func containsCurl(bash string) bool {
	for _, line := range strings.Split(bash, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "curl") {
			return true
		}
	}
	return false
}

func deriveFeatureName(sourceFile string) string {
	clean := strings.TrimSuffix(sourceFile, "/")
	last := lastSegment(clean)
	parts := strings.Split(last, "-")
	var words []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		words = append(words, strings.ToUpper(p[:1])+p[1:])
	}
	return strings.Join(words, " ") + " Tutorial"
}

func sourceFileToFilename(sourceFile string) string {
	clean := strings.TrimSuffix(sourceFile, "/")
	// "/docs/quarkus/getting-started" → "docs-quarkus-getting-started.feature"
	safe := strings.NewReplacer("/", "-", " ", "-").Replace(strings.TrimPrefix(clean, "/"))
	if safe == "" {
		safe = "unknown"
	}
	return safe + ".feature"
}

func deriveTags(s ScenarioData) []string {
	var tags []string
	fw := deriveFramework(s.SourceFile)
	if fw != "unknown" {
		tags = append(tags, "@"+fw)
	}
	if s.Checkpoint != "" {
		tag := "@checkpoint_" + strings.ToLower(strings.NewReplacer("/", "_", "-", "_", ".", "_").Replace(s.Checkpoint))
		tag = strings.Trim(tag, "_")
		tags = append(tags, tag)
	}
	return tags
}

func deriveFramework(sourceFile string) string {
	switch {
	case strings.HasPrefix(sourceFile, "/docs/python/"):
		return "python"
	case strings.HasPrefix(sourceFile, "/docs/quarkus/"):
		return "quarkus"
	case strings.HasPrefix(sourceFile, "/docs/spring/"):
		return "spring"
	default:
		return "unknown"
	}
}

func lastSegment(path string) string {
	path = strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return path
	}
	return path[idx+1:]
}
