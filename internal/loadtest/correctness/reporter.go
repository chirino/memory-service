package correctness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TestResult holds the outcome of a single correctness test.
type TestResult struct {
	Name         string `json:"name"`
	Passed       bool   `json:"passed"`
	ItemsChecked int    `json:"itemsChecked"`
	Detail       string `json:"detail,omitempty"`
}

// CorrectnessReport is the top-level structure written to correctness-report.json.
type CorrectnessReport struct {
	Timestamp string       `json:"timestamp"`
	BaseURL   string       `json:"baseURL"`
	Results   []TestResult `json:"results"`
	AllPassed bool         `json:"allPassed"`
}

var (
	resultsMu sync.Mutex
	results   []TestResult
)

// recordResult appends a test result to the package-level results slice.
// It is safe to call from parallel sub-tests.
func recordResult(name string, passed bool, itemsChecked int, detail string) {
	resultsMu.Lock()
	defer resultsMu.Unlock()
	results = append(results, TestResult{
		Name:         name,
		Passed:       passed,
		ItemsChecked: itemsChecked,
		Detail:       detail,
	})
}

// writeReport serialises the collected results to loadtest/results/correctness-report.json.
func writeReport(url, repoRoot string) {
	allPassed := true
	for _, r := range results {
		if !r.Passed {
			allPassed = false
			break
		}
	}
	report := CorrectnessReport{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		BaseURL:   url,
		Results:   results,
		AllPassed: allPassed,
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	resultsDir := filepath.Join(repoRoot, "loadtest", "results")
	_ = os.MkdirAll(resultsDir, 0o755)
	_ = os.WriteFile(filepath.Join(resultsDir, "correctness-report.json"), data, 0o644)
}
