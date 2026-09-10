// hfrun starts a Hyperfoil in-vm controller, runs one benchmark, fetches
// stats/total JSON via the REST API while the controller is still alive,
// and writes the result to --out.
//
// Why a Go binary instead of a shell script:
//   - "wait-run" in the Hyperfoil CLI consumes buffered stdin, making it
//     impossible to pipe subsequent commands (export, exit) in one printf.
//   - Go goroutines let us write commands to jbang's stdin sequentially,
//     waiting for specific log output before sending the next command.
//
// Usage:
//
//	go run ./internal/loadtest/hfrun/ --yaml=path/to/bench.hf.yaml --out=result.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	hfDeps = "io.hyperfoil:hyperfoil-core:0.27.2,io.hyperfoil:hyperfoil-clustering:0.27.2,io.hyperfoil:hyperfoil-http:0.27.2"
	hfMain = "io.hyperfoil.cli.HyperfoilCli"
	hfCLI  = "io.hyperfoil:hyperfoil-cli:0.27.2"
	hfLog  = "/tmp/hyperfoil/hyperfoil.local.log"
)

var (
	rePort  = regexp.MustCompile(`listening on (?:http://)?127\.0\.0\.1:(\d+)`)
	reRunID = regexp.MustCompile(`Run ([0-9A-Fa-f]+) completed`)
)

func main() {
	yamlFlag := flag.String("yaml", "", "Path to Hyperfoil YAML benchmark file (required)")
	outFlag := flag.String("out", "", "Path to write JSON result file (required)")
	flag.Parse()

	if *yamlFlag == "" || *outFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: hfrun --yaml=<file> --out=<file>")
		os.Exit(1)
	}

	absYAML, _ := filepath.Abs(*yamlFlag)
	absOut, _ := filepath.Abs(*outFlag)
	benchmarkName := strings.TrimSuffix(filepath.Base(absYAML), ".hf.yaml")

	fmt.Printf("[hfrun] benchmark: %s\n", benchmarkName)

	// Clear the Hyperfoil log so extraction starts fresh.
	_ = os.Remove(hfLog)
	_ = os.Remove(absOut)

	// Build jbang command.
	cmd := exec.Command("jbang",
		"--deps", hfDeps,
		"--main", hfMain,
		hfCLI,
	)

	// Create a pipe for jbang's stdin — we write commands one at a time.
	pr, pw := io.Pipe()
	cmd.Stdin = pr
	cmd.Stdout = os.Stdout
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		fatalf("start jbang: %v", err)
	}
	fmt.Printf("[hfrun] jbang PID=%d\n", cmd.Process.Pid)

	// Phase 1: send start-local, upload, run, wait-run sequentially.
	// We write each command and then wait for jbang to process it before
	// sending the next, so wait-run doesn't consume the subsequent commands.
	go func() {
		// start-local — wait for "Connected"
		send(pw, "start-local\n")
		waitForLog(hfLog, "Connected to 127.0.0.1", 60*time.Second)

		// upload — wait for "done"
		send(pw, fmt.Sprintf("upload %s\n", absYAML))
		waitForLog(hfLog, "... done.", 30*time.Second)

		// run — starts the benchmark
		send(pw, fmt.Sprintf("run %s\n", benchmarkName))

		// wait-run — blocks until the run is complete
		// NOTE: we do NOT send this via the pipe because wait-run in the
		// Hyperfoil CLI reads from stdin while blocking, which would prevent
		// us from sending subsequent commands.
		// Instead, we poll the Hyperfoil log file directly.
	}()

	// Phase 2: poll Hyperfoil log for port + run completion.
	port, runID := pollLog(hfLog, 10*time.Minute)
	if port == "" || runID == "" {
		fatalf("timed out waiting for benchmark to complete")
	}
	fmt.Printf("[hfrun] port=%s runID=%s\n", port, runID)

	// Phase 3: fetch stats via REST API while controller is alive.
	url := fmt.Sprintf("http://127.0.0.1:%s/run/%s/stats/total", port, runID)
	fmt.Printf("[hfrun] fetching %s\n", url)

	var statsJSON []byte
	for attempt := 1; attempt <= 10; attempt++ {
		resp, err := http.Get(url) //nolint:noctx
		if err != nil {
			fmt.Printf("[hfrun] attempt %d: %v — retrying\n", attempt, err)
			time.Sleep(time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			statsJSON = body
			break
		}
		fmt.Printf("[hfrun] attempt %d: HTTP %d — retrying\n", attempt, resp.StatusCode)
		time.Sleep(time.Second)
	}

	// Phase 4: send exit to shut down the controller cleanly.
	send(pw, "exit\n")
	pw.Close()
	_ = cmd.Wait()

	// Write result file.
	if statsJSON == nil {
		stub := fmt.Sprintf(`{"name":%q,"note":"stats-unavailable"}`, benchmarkName)
		_ = os.WriteFile(absOut, []byte(stub), 0o644)
		fmt.Fprintf(os.Stderr, "[hfrun] WARNING: stats not available\n")
		os.Exit(1)
	}

	// Inject benchmark name.
	var raw map[string]any
	if err := json.Unmarshal(statsJSON, &raw); err != nil {
		fatalf("parse stats JSON: %v", err)
	}
	raw["name"] = benchmarkName
	out2, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(absOut, out2, 0o644); err != nil {
		fatalf("write result: %v", err)
	}
	fmt.Printf("[hfrun] written: %s\n", absOut)
	printSummary(raw, benchmarkName)

	// Inspect statistics for invalid requests and SLO violations.
	// We do this after writing the file so partial results are always saved.
	if err := checkStats(raw, benchmarkName); err != nil {
		fmt.Fprintf(os.Stderr, "[hfrun] FAIL %s: %v\n", benchmarkName, err)
		os.Exit(1)
	}
}

// checkStats inspects the parsed Hyperfoil statistics for error rates and p99
// SLO violations. Returns a non-nil error if either threshold is breached.
//
// Error rate: sum of invalid + connectionErrors + requestTimeouts +
// internalErrors across all statistics entries. If this exceeds 1% of total
// requests the benchmark is considered failed.
//
// SLO: p99 for each metric must not exceed the threshold defined in
// sloThresholds. Benchmarks with a single metric use the benchmark name as
// the key; multi-metric benchmarks use "<benchmark>/<metric>".
func checkStats(raw map[string]any, benchmarkName string) error {
	statsArr, ok := raw["statistics"].([]any)
	if !ok || len(statsArr) == 0 {
		return nil
	}

	// sloThresholds mirrors the values in report/main.go.
	sloThresholds := map[string]float64{
		"append-throughput":    500,
		"list-conversations":   300,
		"list-entries":         300,
		"search-conversations": 1000,
		"list-forks":           300,
	}
	defaultSLO := 1000.0

	var totalRequests, totalErrors float64
	var sloViolations []string

	// Collect unique metric names to decide whether to use "<bench>/<metric>" keys.
	metricNames := make(map[string]struct{})
	for _, s := range statsArr {
		stat, ok := s.(map[string]any)
		if !ok {
			continue
		}
		metricNames[strVal(stat, "metric")] = struct{}{}
	}
	multiMetric := len(metricNames) > 1

	for _, s := range statsArr {
		stat, ok := s.(map[string]any)
		if !ok {
			continue
		}
		summary, ok := stat["summary"].(map[string]any)
		if !ok {
			continue
		}

		rc, _ := floatVal(summary, "requestCount")
		invalid, _ := floatVal(summary, "invalid")
		connErr, _ := floatVal(summary, "connectionErrors")
		timeouts, _ := floatVal(summary, "requestTimeouts")
		internal, _ := floatVal(summary, "internalErrors")

		totalRequests += rc
		totalErrors += invalid + connErr + timeouts + internal

		// Determine the SLO key for this metric.
		metricName := strVal(stat, "metric")
		sloKey := benchmarkName
		if multiMetric && metricName != "" {
			sloKey = benchmarkName + "/" + metricName
		}
		limit, ok := sloThresholds[sloKey]
		if !ok {
			limit = defaultSLO
		}

		if pcts, ok := summary["percentileResponseTime"].(map[string]any); ok {
			if p99, ok := floatVal(pcts, "99.0"); ok {
				p99ms := p99 / 1e6
				if p99ms > limit {
					sloViolations = append(sloViolations,
						fmt.Sprintf("%s p99=%.0fms > SLO %.0fms", sloKey, p99ms, limit))
				}
			}
		}
	}

	// Check error rate (> 1% of total requests).
	if totalRequests > 0 && (totalErrors/totalRequests) > 0.01 {
		return fmt.Errorf("error rate %.1f%% (%.0f errors / %.0f requests) exceeds 1%% threshold",
			(totalErrors/totalRequests)*100, totalErrors, totalRequests)
	}

	if len(sloViolations) > 0 {
		return fmt.Errorf("SLO violations: %v", sloViolations)
	}
	return nil
}

// strVal extracts a string from a map[string]any, returning "" if absent.
func strVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// floatVal extracts a float64 from a map[string]any, returning 0 and false if absent.
func floatVal(m map[string]any, key string) (float64, bool) {
	if v, ok := m[key]; ok {
		switch f := v.(type) {
		case float64:
			return f, true
		case int:
			return float64(f), true
		}
	}
	return 0, false
}

// send writes a command string to the pipe, ignoring errors (jbang may exit).
func send(pw *io.PipeWriter, cmd string) {
	_, _ = pw.Write([]byte(cmd))
}

// waitForLog polls the Hyperfoil log file until the given substring appears
// or the timeout is reached.
func waitForLog(path, substr string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), substr) {
			return
		}
	}
}

// pollLog polls the Hyperfoil log file until both the controller port and
// the run completion message are found, returning (port, runID).
func pollLog(path string, timeout time.Duration) (string, string) {
	deadline := time.Now().Add(timeout)
	var port, runID string
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if port == "" {
			if m := rePort.FindStringSubmatch(content); m != nil {
				port = m[1]
				fmt.Printf("[hfrun] controller port: %s\n", port)
			}
		}
		if runID == "" {
			if m := reRunID.FindStringSubmatch(content); m != nil {
				runID = m[1]
				fmt.Printf("[hfrun] run completed: %s\n", runID)
			}
		}
		if port != "" && runID != "" {
			return port, runID
		}
	}
	return port, runID
}

func printSummary(raw map[string]any, name string) {
	stats, _ := raw["statistics"].([]any)
	if len(stats) == 0 {
		return
	}
	s, _ := stats[0].(map[string]any)
	summary, _ := s["summary"].(map[string]any)
	if summary == nil {
		return
	}
	rc, _ := summary["requestCount"].(float64)
	start, _ := summary["startTime"].(float64)
	end, _ := summary["endTime"].(float64)
	pcts, _ := summary["percentileResponseTime"].(map[string]any)
	p50, _ := pcts["50.0"].(float64)
	p99, _ := pcts["99.0"].(float64)
	rps := 0.0
	if end > start {
		rps = rc / ((end - start) / 1000.0)
	}
	fmt.Printf("[hfrun] %s — RPS=%.0f  p50=%.0fms  p99=%.0fms\n",
		name, rps, p50/1e6, p99/1e6)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[hfrun] FATAL: "+format+"\n", args...)
	os.Exit(1)
}
