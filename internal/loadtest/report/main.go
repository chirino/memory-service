// Package main implements the load-test results aggregator.
// It reads loadtest/results/seed-manifest.json, loadtest/results/hyperfoil-*.json,
// loadtest/results/correctness-report.json, and optionally loadtest/results/sse-report.json,
// then produces loadtest/results/report.md and loadtest/results/report.json.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---- input structures -------------------------------------------------------

type conversationRecord struct {
	ID              string `json:"id"`
	OwnerID         string `json:"ownerID"`
	EntryCount      int    `json:"entryCount"`
	ParticipantType string `json:"participantType"`
}

type seedManifest struct {
	BaseURL            string               `json:"baseURL"`
	TotalConversations int                  `json:"totalConversations"`
	Conversations      []conversationRecord `json:"conversations"`
	Forks              []any                `json:"forks"`
}

type correctnessResult struct {
	Name         string `json:"name"`
	Passed       bool   `json:"passed"`
	ItemsChecked int    `json:"itemsChecked"`
	Detail       string `json:"detail,omitempty"`
}

type correctnessReport struct {
	BaseURL   string              `json:"baseURL"`
	Results   []correctnessResult `json:"results"`
	AllPassed bool                `json:"allPassed"`
}

// ---- output structures ------------------------------------------------------

type benchmarkRow struct {
	Name    string  `json:"name"`
	RPS     float64 `json:"rps"`
	P50ms   float64 `json:"p50ms"`
	P95ms   float64 `json:"p95ms"`
	P99ms   float64 `json:"p99ms"`
	SLOPass bool    `json:"sloPass"`
	// hasData indicates whether actual benchmark data was found.
	// When false the row is shown as "-" and excluded from the overall pass/fail.
	hasData bool
}

// sloThresholds defines p99 SLO limits in milliseconds per benchmark name.
// A benchmark row passes SLO when its measured p99 is <= the threshold.
// Rows not listed here use defaultSLOP99Ms.
var sloThresholds = map[string]float64{
	"append-throughput":        500,
	"list-conversations":       300,
	"list-entries":             300,
	"search-conversations":     1000,
	"list-forks":               300,
	"sse-event-delay/users-1":  500,
	"sse-event-delay/users-10": 1000,
	"sse-event-delay/users-50": 2000,
}

const defaultSLOP99Ms = 1000

// sloPass returns true when p99ms satisfies the SLO threshold for name.
func sloPass(name string, p99ms float64) bool {
	limit, ok := sloThresholds[name]
	if !ok {
		limit = defaultSLOP99Ms
	}
	return p99ms <= limit
}

type correctnessRow struct {
	Name         string `json:"name"`
	Passed       bool   `json:"passed"`
	ItemsChecked int    `json:"itemsChecked"`
}

type seedInfo struct {
	TotalConversations int            `json:"totalConversations"`
	ForkChains         int            `json:"forkChains"`
	TotalEntries       int            `json:"totalEntries"`
	ShortConvs         int            `json:"shortConversations"`
	MediumConvs        int            `json:"mediumConversations"`
	LongConvs          int            `json:"longConversations"`
	ParticipantTypes   map[string]int `json:"participantTypes"`
	UniqueOwners       int            `json:"uniqueOwners"`
	MinEntries         int            `json:"minEntriesPerConv"`
	MaxEntries         int            `json:"maxEntriesPerConv"`
	AvgEntries         float64        `json:"avgEntriesPerConv"`
	MedianEntries      int            `json:"medianEntriesPerConv"`
	LongTailAvgEntries float64        `json:"longTailAvgEntries"`
	LongTailMaxEntries int            `json:"longTailMaxEntries"`
}

type reportJSON struct {
	Timestamp   string           `json:"timestamp"`
	BaseURL     string           `json:"baseURL"`
	Seed        seedInfo         `json:"seed"`
	Benchmarks  []benchmarkRow   `json:"benchmarks"`
	Correctness []correctnessRow `json:"correctness"`
	AllPassed   bool             `json:"allPassed"`
}

// ---- helpers ----------------------------------------------------------------

// repoRoot returns the path to the repository root. The binary is expected to
// be run from the repo root (go run ./internal/loadtest/report/ or task loadtest:report).
func repoRoot() string {
	if root, err := os.Getwd(); err == nil {
		return root
	}
	return "."
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

// boolVal extracts a bool from a map[string]any, returning false if absent.
func boolVal(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// dash returns "-" if v is zero, otherwise formats it as %.0f.
func dash(v float64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f", v)
}

// sloIcon returns "✅ PASS" or "❌ FAIL".
func sloIcon(pass bool) string {
	if pass {
		return "✅ PASS"
	}
	return "❌ FAIL"
}

// passIcon returns "✅ PASS" or "❌ FAIL".
func passIcon(pass bool) string {
	return sloIcon(pass)
}

// ---- loaders ----------------------------------------------------------------

func loadSeedManifest(root string) (seedManifest, bool) {
	path := filepath.Join(root, "loadtest", "results", "seed-manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: seed-manifest.json not found (%v); continuing with empty seed info\n", err)
		return seedManifest{}, false
	}
	var m seedManifest
	if err := json.Unmarshal(data, &m); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not parse seed-manifest.json (%v); continuing with empty seed info\n", err)
		return seedManifest{}, false
	}
	return m, true
}

// mergedMetricBenchmarks lists benchmark names whose multiple Hyperfoil metric
// labels should be collapsed into a single report row (pooling request counts
// for RPS, averaging percentiles across all metrics).  Use this for benchmarks
// where metrics are sequential steps within one VU (e.g. append-user-entry
// then append-ai-entry) rather than independent parallel workloads.
var mergedMetricBenchmarks = map[string]bool{
	"append-throughput": true,
}

// loadHyperfoilResults reads all hyperfoil-*.json files.
// It supports the /run/{id}/stats/total JSON format returned by Hyperfoil 0.27.2
// (field: statistics[].summary.percentileResponseTime + requestCount),
// as well as the older flat format (fields: throughput, p50, p99).
//
// For benchmarks that use multiple named metrics across phases, each distinct
// metric name is emitted as a separate row named "<benchmark>/<metric>".
// Benchmarks listed in mergedMetricBenchmarks are always collapsed into a
// single row regardless of metric count.
// Single-metric benchmarks continue to be reported under the bare benchmark name.
func loadHyperfoilResults(root string) []benchmarkRow {
	resultsDir := filepath.Join(root, "loadtest", "results")
	var files []string
	for _, glob := range []string{"hyperfoil-*.json", "sse-event-delay-*.json"} {
		matched, err := filepath.Glob(filepath.Join(resultsDir, glob))
		if err == nil {
			files = append(files, matched...)
		}
	}
	if len(files) == 0 {
		return nil
	}

	// De-duplicate: keep only the latest file per benchmark name.
	latest := make(map[string]string) // name -> filepath
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".json")
		// Normalise known prefixes to a stable benchmark name before
		// stripping the trailing timestamp, so de-duplication works correctly
		// regardless of which prefix pattern the file uses.
		switch {
		case strings.HasPrefix(base, "hyperfoil-"):
			base = strings.TrimPrefix(base, "hyperfoil-")
		case strings.HasPrefix(base, "sse-event-delay-"):
			base = "sse-event-delay"
			// timestamp is the only remaining segment; skip the strip below
			if prev, ok := latest[base]; !ok || f > prev {
				latest[base] = f
			}
			continue
		}
		// Strip trailing -YYYYMMDDTHHMMSS timestamp if present.
		if idx := strings.LastIndex(base, "-"); idx >= 0 && len(base)-idx > 8 {
			base = base[:idx]
		}
		if prev, ok := latest[base]; !ok || f > prev {
			latest[base] = f
		}
	}

	var rows []benchmarkRow
	for name, f := range latest {
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not read %s (%v); skipping\n", f, err)
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not parse %s (%v); skipping\n", f, err)
			continue
		}

		// Use embedded name field if present.
		if n := strVal(raw, "name"); n != "" {
			name = n
		}

		// Skip stub files (stats-unavailable or parse-failed).
		// hasData=false excludes this row from the summary pass/fail.
		// SLOPass=false: a benchmark that produced no stats is not a pass.
		if note := strVal(raw, "note"); note != "" {
			fmt.Fprintf(os.Stderr, "info: %s has note=%q (benchmark ran but stats not available)\n", name, note)
			rows = append(rows, benchmarkRow{Name: name, SLOPass: false, hasData: false})
			continue
		}

		// --- Hyperfoil 0.27.2 /stats/total format ---
		// {"status":"TERMINATED","statistics":[{"phase":"...","metric":"...","summary":{...}},...]}
		//
		// Group statistics entries by their "metric" field.  If all entries share
		// the same (or empty) metric the result is a single row; if there are
		// multiple distinct metrics each gets its own row named "<benchmark>/<metric>".
		if statsArr, ok := raw["statistics"].([]any); ok && len(statsArr) > 0 {
			// Collect per-metric accumulators.
			type metricAcc struct {
				totalRequests   float64
				totalDurationNs float64
				p50sum          float64
				p95sum          float64
				p99sum          float64
				count           int
			}
			byMetric := make(map[string]*metricAcc)
			metricOrder := []string{} // preserve insertion order for stable output

			for _, s := range statsArr {
				stat, ok := s.(map[string]any)
				if !ok {
					continue
				}
				summary, ok := stat["summary"].(map[string]any)
				if !ok {
					continue
				}
				metric := strVal(stat, "metric")
				if _, exists := byMetric[metric]; !exists {
					byMetric[metric] = &metricAcc{}
					metricOrder = append(metricOrder, metric)
				}
				acc := byMetric[metric]
				if rc, ok := floatVal(summary, "requestCount"); ok {
					acc.totalRequests += rc
				}
				if et, ok := floatVal(summary, "endTime"); ok {
					if st, ok := floatVal(summary, "startTime"); ok {
						acc.totalDurationNs += et - st
					}
				}
				if pcts, ok := summary["percentileResponseTime"].(map[string]any); ok {
					if v, ok := floatVal(pcts, "50.0"); ok {
						acc.p50sum += v / 1e6 // ns → ms
						acc.count++
					}
					// Prefer 95.0 (emitted by ssedelay); fall back to 90.0 (emitted by Hyperfoil).
					if v, ok := floatVal(pcts, "95.0"); ok {
						acc.p95sum += v / 1e6
					} else if v, ok := floatVal(pcts, "90.0"); ok {
						acc.p95sum += v / 1e6
					}
					if v, ok := floatVal(pcts, "99.0"); ok {
						acc.p99sum += v / 1e6
					}
				}
			}

			multiMetric := len(byMetric) > 1 && !mergedMetricBenchmarks[name]

			if !multiMetric {
				// Emit a single merged row: pool requests/duration, average percentiles.
				var merged metricAcc
				for _, metric := range metricOrder {
					acc := byMetric[metric]
					merged.totalRequests += acc.totalRequests
					merged.totalDurationNs += acc.totalDurationNs
					merged.p50sum += acc.p50sum
					merged.p95sum += acc.p95sum
					merged.p99sum += acc.p99sum
					merged.count += acc.count
				}
				var rps, p50, p95, p99 float64
				if merged.count > 0 {
					p50 = merged.p50sum / float64(merged.count)
					p95 = merged.p95sum / float64(merged.count)
					p99 = merged.p99sum / float64(merged.count)
				}
				if merged.totalDurationNs > 0 {
					rps = merged.totalRequests / (merged.totalDurationNs / 1e3)
				}
				rows = append(rows, benchmarkRow{
					Name:    name,
					RPS:     rps,
					P50ms:   p50,
					P95ms:   p95,
					P99ms:   p99,
					SLOPass: sloPass(name, p99),
					hasData: true,
				})
			} else {
				for _, metric := range metricOrder {
					acc := byMetric[metric]
					var rps, p50, p95, p99 float64
					if acc.count > 0 {
						p50 = acc.p50sum / float64(acc.count)
						p95 = acc.p95sum / float64(acc.count)
						p99 = acc.p99sum / float64(acc.count)
					}
					if acc.totalDurationNs > 0 {
						rps = acc.totalRequests / (acc.totalDurationNs / 1e3)
					}
					rowName := name
					if metric != "" {
						rowName = name + "/" + metric
					}
					rows = append(rows, benchmarkRow{
						Name:    rowName,
						RPS:     rps,
						P50ms:   p50,
						P95ms:   p95,
						P99ms:   p99,
						SLOPass: sloPass(rowName, p99),
						hasData: true,
					})
				}
			}
			continue
		}

		// --- Fallback: flat format (throughput, p50, p99) ---
		var rps, p50, p99 float64
		rps, _ = floatVal(raw, "throughput")
		p50, _ = floatVal(raw, "p50")
		p99, _ = floatVal(raw, "p99")

		rows = append(rows, benchmarkRow{
			Name:    name,
			RPS:     rps,
			P50ms:   p50,
			P99ms:   p99,
			SLOPass: sloPass(name, p99),
			hasData: true,
		})
	}
	return rows
}

func loadCorrectnessReport(root string) (correctnessReport, bool) {
	path := filepath.Join(root, "loadtest", "results", "correctness-report.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return correctnessReport{}, false
	}
	var r correctnessReport
	if err := json.Unmarshal(data, &r); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not parse correctness-report.json (%v)\n", err)
		return correctnessReport{}, false
	}
	return r, true
}

// ---- report generation ------------------------------------------------------

// knownEndpoints is the canonical ordered list of benchmark endpoints shown in
// the report table. Entries absent from the loaded hyperfoil results appear as
// "-" rows.
//
// SSE coverage is handled entirely by the Go ssedelay benchmark:
//   - append throughput under SSE load: ssedelay users-50 (50 senders + 50 subscribers open)
//   - end-to-end event delivery latency: ssedelay users-1/10/50
// sse-fan-out.hf.yaml was removed because it permanently wrote entries to the
// seeded Postgres conversations on every benchmark run (disk-fill risk) and
// provided no coverage not already in ssedelay or append-throughput.
var knownEndpoints = []string{
	"append-throughput",
	"list-conversations",
	"list-entries",
	"search-conversations",
	"list-forks",
	// SSE end-to-end event delivery latency at each concurrency ramp level.
	// Produced by internal/loadtest/ssedelay/.
	"sse-event-delay/users-1",
	"sse-event-delay/users-10",
	"sse-event-delay/users-50",
}

func buildBenchmarkRows(loaded []benchmarkRow) []benchmarkRow {
	byName := make(map[string]benchmarkRow, len(loaded))
	for _, r := range loaded {
		byName[r.Name] = r
	}
	rows := make([]benchmarkRow, 0, len(knownEndpoints))
	for _, ep := range knownEndpoints {
		if r, ok := byName[ep]; ok {
			rows = append(rows, r)
		} else {
			// Benchmark not yet run — hasData=false so it is excluded from the
			// overall pass/fail summary and shown as "-" in the report table.
			// SLOPass is explicitly false: absent data is not a pass.
			rows = append(rows, benchmarkRow{Name: ep, SLOPass: false, hasData: false})
		}
	}
	return rows
}

func computeSeedInfo(seed seedManifest) seedInfo {
	info := seedInfo{
		TotalConversations: seed.TotalConversations,
		ForkChains:         len(seed.Forks),
		ParticipantTypes:   make(map[string]int),
	}
	if len(seed.Conversations) == 0 {
		return info
	}

	owners := make(map[string]struct{})
	counts := make([]int, 0, len(seed.Conversations))
	totalEC := 0
	longTailTotal := 0
	info.MinEntries = seed.Conversations[0].EntryCount
	info.MaxEntries = 0

	for _, c := range seed.Conversations {
		ec := c.EntryCount
		info.TotalEntries += ec // manifest already stores actual entry count
		totalEC += ec
		counts = append(counts, ec)

		if ec < info.MinEntries {
			info.MinEntries = ec
		}
		if ec > info.MaxEntries {
			info.MaxEntries = ec
		}

		switch {
		case ec <= 10:
			info.ShortConvs++
		case ec <= 100:
			info.MediumConvs++
		default:
			info.LongConvs++
			longTailTotal += ec
			if ec > info.LongTailMaxEntries {
				info.LongTailMaxEntries = ec
			}
		}
		info.ParticipantTypes[c.ParticipantType]++
		owners[c.OwnerID] = struct{}{}
	}

	info.UniqueOwners = len(owners)
	info.AvgEntries = float64(totalEC) / float64(len(seed.Conversations))
	if info.LongConvs > 0 {
		info.LongTailAvgEntries = float64(longTailTotal) / float64(info.LongConvs)
	}

	// Median — sort a copy of counts.
	sorted := make([]int, len(counts))
	copy(sorted, counts)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	info.MedianEntries = sorted[len(sorted)/2]

	return info
}

func writeMD(root, ts, baseURL string, seed seedManifest, hasSeed bool,
	benchRows []benchmarkRow, benchNotRun bool,
	correctRows []correctnessRow, correctNotRun bool) error {

	var sb strings.Builder

	sb.WriteString("# Load Test Report\n\n")
	sb.WriteString(fmt.Sprintf("Generated: %s\n\n", ts))
	sb.WriteString(fmt.Sprintf("BaseURL: %s\n\n", baseURL))

	// --- Seed Data Statistics ---
	sb.WriteString("## Seed Data\n\n")
	if !hasSeed {
		sb.WriteString("> seed not yet run\n\n")
	} else {
		si := computeSeedInfo(seed)
		sb.WriteString("| Metric | Value |\n")
		sb.WriteString("|---|---|\n")
		sb.WriteString(fmt.Sprintf("| Total conversations | %d |\n", si.TotalConversations))
		sb.WriteString(fmt.Sprintf("| Total entries seeded (USER+AI pairs) | %d |\n", si.TotalEntries))
		sb.WriteString(fmt.Sprintf("| Fork chains | %d |\n", si.ForkChains))
		sb.WriteString(fmt.Sprintf("| Unique conversation owners | %d |\n", si.UniqueOwners))
		sb.WriteString("\n")

		sb.WriteString("**Entry count distribution per conversation:**\n\n")
		sb.WriteString("| Metric | Value |\n")
		sb.WriteString("|---|---|\n")
		sb.WriteString(fmt.Sprintf("| Min entries | %d |\n", si.MinEntries))
		sb.WriteString(fmt.Sprintf("| Max entries | %d |\n", si.MaxEntries))
		sb.WriteString(fmt.Sprintf("| Avg entries | %.1f |\n", si.AvgEntries))
		sb.WriteString(fmt.Sprintf("| Median entries | %d |\n", si.MedianEntries))
		sb.WriteString(fmt.Sprintf("| Short (≤10 entries) | %d conversations |\n", si.ShortConvs))
		sb.WriteString(fmt.Sprintf("| Medium (11–100 entries) | %d conversations |\n", si.MediumConvs))
		sb.WriteString(fmt.Sprintf("| Long tail (>100 entries) | %d conversations |\n", si.LongConvs))
		sb.WriteString(fmt.Sprintf("| Long tail avg entries | %.0f |\n", si.LongTailAvgEntries))
		sb.WriteString(fmt.Sprintf("| Long tail max entries | %d |\n", si.LongTailMaxEntries))
		sb.WriteString("\n")

		sb.WriteString("**Participant type breakdown:**\n\n")
		sb.WriteString("| Type | Count |\n")
		sb.WriteString("|---|---|\n")
		for _, pt := range []string{"single-user", "two-user", "two-agent"} {
			sb.WriteString(fmt.Sprintf("| %s | %d |\n", pt, si.ParticipantTypes[pt]))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Throughput & Latency\n\n")
	if benchNotRun {
		sb.WriteString("> benchmarks not yet run\n\n")
	}
	sb.WriteString("| Endpoint | RPS | p50 (ms) | p90/p95 (ms) | p99 (ms) | SLO |\n")
	sb.WriteString("|---|---|---|---|---|---|\n")
	for _, r := range benchRows {
		rpsStr := dash(r.RPS)
		p50Str := dash(r.P50ms)
		p95Str := dash(r.P95ms)
		p99Str := dash(r.P99ms)
		slo := sloIcon(r.SLOPass)
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n", r.Name, rpsStr, p50Str, p95Str, p99Str, slo))
	}
	sb.WriteString("\n")

	sb.WriteString("## Pagination Correctness\n\n")
	if correctNotRun {
		sb.WriteString("> correctness not yet run\n\n")
	}
	sb.WriteString("| Test | Items Checked | Result |\n")
	sb.WriteString("|---|---|---|\n")
	for _, r := range correctRows {
		sb.WriteString(fmt.Sprintf("| %s | %d | %s |\n", r.Name, r.ItemsChecked, passIcon(r.Passed)))
	}
	sb.WriteString("\n")

	sb.WriteString("## Summary\n\n")
	benchSummary := "benchmarks not yet run"
	if !benchNotRun {
		allBenchPass := true
		for _, r := range benchRows {
			if !r.SLOPass {
				allBenchPass = false
				break
			}
		}
		if allBenchPass {
			benchSummary = "All benchmarks: PASS"
		} else {
			benchSummary = "All benchmarks: FAIL"
		}
	}
	correctSummary := "correctness not yet run"
	if !correctNotRun {
		if len(correctRows) == 0 {
			correctSummary = "correctness not yet run"
		} else {
			allPass := true
			for _, r := range correctRows {
				if !r.Passed {
					allPass = false
					break
				}
			}
			if allPass {
				correctSummary = "All correctness checks: PASS"
			} else {
				correctSummary = "All correctness checks: FAIL"
			}
		}
	}
	sb.WriteString(benchSummary + "\n")
	sb.WriteString(correctSummary + "\n")

	out := filepath.Join(root, "loadtest", "results", "report.md")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(out), err)
	}
	if err := os.WriteFile(out, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	return nil
}

func writeJSON(root, ts, baseURL string, seed seedManifest,
	benchRows []benchmarkRow, correctRows []correctnessRow) error {

	allPassed := true
	for _, r := range benchRows {
		if !r.SLOPass {
			allPassed = false
			break
		}
	}
	for _, r := range correctRows {
		if !r.Passed {
			allPassed = false
			break
		}
	}

	out := reportJSON{
		Timestamp:   ts,
		BaseURL:     baseURL,
		Seed:        computeSeedInfo(seed),
		Benchmarks:  benchRows,
		Correctness: correctRows,
		AllPassed:   allPassed,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report.json: %w", err)
	}
	path := filepath.Join(root, "loadtest", "results", "report.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ---- main -------------------------------------------------------------------

func main() {
	root := repoRoot()
	ts := time.Now().UTC().Format(time.RFC3339)

	// 1. Seed manifest (optional).
	seed, hasSeed := loadSeedManifest(root)

	baseURL := seed.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:8082"
	}

	// 2. Hyperfoil benchmark results (optional).
	loadedBench := loadHyperfoilResults(root)
	benchNotRun := len(loadedBench) == 0
	benchRows := buildBenchmarkRows(loadedBench)

	// 3. Correctness report (optional).
	cr, hasCorrectness := loadCorrectnessReport(root)
	correctNotRun := !hasCorrectness
	var correctRows []correctnessRow
	for _, r := range cr.Results {
		correctRows = append(correctRows, correctnessRow{
			Name:         r.Name,
			Passed:       r.Passed,
			ItemsChecked: r.ItemsChecked,
		})
	}

	// 4. Write report.md.
	if err := writeMD(root, ts, baseURL, seed, hasSeed, benchRows, benchNotRun, correctRows, correctNotRun); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// 5. Write report.json — also computes allPassed.
	if err := writeJSON(root, ts, baseURL, seed, benchRows, correctRows); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("loadtest/results/report.md written")
	fmt.Println("loadtest/results/report.json written")

	// 6. Exit nonzero when any benchmark or correctness check failed so
	//    task loadtest:all and CI pipelines detect SLO regressions.
	allPassed := true
	for _, r := range benchRows {
		if !r.SLOPass {
			allPassed = false
			break
		}
	}
	for _, r := range correctRows {
		if !r.Passed {
			allPassed = false
			break
		}
	}
	if !allPassed {
		fmt.Fprintln(os.Stderr, "report: one or more benchmarks or correctness checks failed")
		os.Exit(1)
	}
}
