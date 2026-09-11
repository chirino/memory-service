// ssedelay measures end-to-end SSE event delivery latency: the time from a
// POST /v1/conversations/{id}/entries completing until the corresponding
// "conversation/updated" event arrives on the user's SSE subscriber.
//
// Topology (mirrors Hiram's model):
//
//	Per user:       1 sender goroutine  +  1 SSE subscriber connection
//	Admin level:    2 persistent admin SSE subscriber connections
//	                (simulating cognition-style all-events consumers)
//
// So each append fans out to exactly 3 SSE connections: the owning user's
// subscriber plus 2 admin subscribers.
//
// The benchmark runs three ramp levels sequentially: 1, 10, and 50 concurrent
// users.  At each level it runs for --duration seconds, records every
// measured latency sample, then computes p50/p95/p99.
//
// Output: a JSON file compatible with the loadtest report aggregator.
//
// Usage:
//
//	go run ./internal/loadtest/ssedelay/ \
//	    --base-url=http://localhost:8082 \
//	    --api-key=agent-api-key-1 \
//	    --admin-api-key=admin-api-key-1 \
//	    --duration=30 \
//	    --out=loadtest/results/sse-event-delay-TIMESTAMP.json
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// rampLevel describes one concurrency tier to measure.
type rampLevel struct {
	users int
	label string // used as the metric name in the output JSON
}

var rampLevels = []rampLevel{
	{1, "users-1"},
	{10, "users-10"},
	{50, "users-50"},
}

// sample records one append→event latency measurement.
type sample struct {
	latencyNs int64
	timedOut  bool
}

// pendingAppend is sent from a sender goroutine to its paired SSE subscriber
// goroutine so the subscriber knows when the POST completed and can measure
// the event arrival latency.
type pendingAppend struct {
	convID    string
	appendEnd time.Time // when the POST completed
	resultCh  chan sample
}

// levelResult holds the collected samples for one ramp level.
type levelResult struct {
	label     string
	samples   []sample
	startTime time.Time
	endTime   time.Time
}

// maxTimeoutPct is the maximum acceptable fraction of timed-out samples for
// any single ramp level before the benchmark is considered failed.  A value
// above this threshold means either the SSE subscriber never connected, the
// server is not routing events, or the service is completely overloaded.
const maxTimeoutPct = 0.10 // 10 %

func main() {
	baseURL := flag.String("base-url", "http://localhost:8082", "Memory service base URL")
	apiKey := flag.String("api-key", "agent-api-key-1", "Agent API key (X-API-Key)")
	adminKey := flag.String("admin-api-key", "admin-api-key-1", "Admin API key for the 2 admin SSE subscribers")
	duration := flag.Int("duration", 30, "Seconds to run at each concurrency level")
	eventTimeout := flag.Duration("event-timeout", 10*time.Second, "Max wait for an SSE event before recording a timeout")
	out := flag.String("out", "", "Output JSON file path (required)")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: ssedelay --out=<file> [flags]")
		os.Exit(1)
	}

	// 1. Provision one conversation per max-users so each user has a dedicated
	//    conversation — this avoids needing cross-user conversation sharing and
	//    keeps the SSE routing realistic (only the owner's subscriber receives
	//    the event).
	maxUsers := rampLevels[len(rampLevels)-1].users
	fmt.Printf("[ssedelay] provisioning %d conversations...\n", maxUsers)
	convIDs, err := provisionConversations(*baseURL, *apiKey, maxUsers)
	if err != nil {
		fatalf("provision conversations: %v", err)
	}
	fmt.Printf("[ssedelay] %d conversations ready\n", len(convIDs))

	// 2. Start 2 persistent admin subscribers.  They stay open for the entire
	//    benchmark run and receive every event, simulating cognition consumers.
	adminCtx, cancelAdmins := context.WithCancel(context.Background())
	defer cancelAdmins()
	for i := 0; i < 2; i++ {
		go runAdminSubscriber(adminCtx, *baseURL, *adminKey, i+1)
	}
	// Give admin streams a moment to connect before load starts.
	time.Sleep(300 * time.Millisecond)

	// 3. Run each ramp level sequentially.
	var results []levelResult
	failed := false
	for _, level := range rampLevels {
		fmt.Printf("[ssedelay] === ramp level: %d user(s), %ds ===\n", level.users, *duration)
		res := runLevel(
			*baseURL, *apiKey,
			level, convIDs[:level.users],
			time.Duration(*duration)*time.Second,
			*eventTimeout,
		)
		results = append(results, res)
		printLevelSummary(res)

		// Fail if too many events timed out — indicates subscriber not connected,
		// events not routed, or zero samples (no appends succeeded at all).
		total := len(res.samples)
		timeouts := 0
		for _, s := range res.samples {
			if s.timedOut {
				timeouts++
			}
		}
		if total == 0 {
			fmt.Fprintf(os.Stderr, "[ssedelay] FAIL %s: no samples recorded (SSE subscriber likely never connected)\n", level.label)
			failed = true
		} else {
			pct := float64(timeouts) / float64(total)
			if pct > maxTimeoutPct {
				fmt.Fprintf(os.Stderr, "[ssedelay] FAIL %s: timeout rate %.0f%% > %.0f%% threshold (%d/%d samples timed out)\n",
					level.label, pct*100, maxTimeoutPct*100, timeouts, total)
				failed = true
			}
		}

		// Brief pause between levels so SSE connections can drain.
		time.Sleep(500 * time.Millisecond)
	}

	// 4. Write output JSON regardless of failure so partial results are visible.
	if err := writeOutput(*out, results); err != nil {
		fatalf("write output: %v", err)
	}
	fmt.Printf("[ssedelay] results written to %s\n", *out)

	if failed {
		fatalf("one or more ramp levels exceeded the timeout threshold — SSE delivery is broken")
	}
}

// runLevel opens one SSE subscriber per user, runs senders for the given
// duration, and collects latency samples.
func runLevel(
	baseURL, apiKey string,
	level rampLevel,
	convIDs []string,
	dur, eventTimeout time.Duration,
) levelResult {
	res := levelResult{label: level.label, startTime: time.Now()}

	// Each user gets a buffered channel of pending appends.
	pendingChs := make([]chan pendingAppend, level.users)
	for i := range pendingChs {
		pendingChs[i] = make(chan pendingAppend, 64)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dur+10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var allSamples []sample

	// readyChs receives a signal from each subscriber once its SSE connection
	// is established (HTTP 200 received).  Senders wait for all readyChs to
	// fire before starting, eliminating the fixed-sleep startup race.
	readyChs := make([]chan struct{}, level.users)
	for i := range readyChs {
		readyChs[i] = make(chan struct{}, 1)
	}

	// Start per-user SSE subscriber goroutines.
	for i := 0; i < level.users; i++ {
		userID := fmt.Sprintf("loadtest-sse-user-%d", i+1)
		convID := convIDs[i]
		ch := pendingChs[i]
		ready := readyChs[i]

		wg.Add(1)
		go func(userID, convID string, pending <-chan pendingAppend, ready chan<- struct{}) {
			defer wg.Done()
			subscriberLoop(ctx, baseURL, apiKey, userID, convID, pending, eventTimeout, ready,
				func(s sample) {
					mu.Lock()
					allSamples = append(allSamples, s)
					mu.Unlock()
				})
		}(userID, convID, ch, ready)
	}

	// Wait for every SSE subscriber to signal it is connected before starting
	// senders.  Each subscriber closes its readyCh once the HTTP 200 is received.
	// Use a timeout so a failed connection does not hang the benchmark forever.
	connDeadline := time.NewTimer(10 * time.Second)
	defer connDeadline.Stop()
	for i, rch := range readyChs {
		select {
		case <-rch:
		case <-connDeadline.C:
			fmt.Fprintf(os.Stderr, "[ssedelay] WARN: subscriber %d did not connect within 10s\n", i+1)
		case <-ctx.Done():
			return levelResult{label: level.label}
		}
	}

	// Start per-user sender goroutines.
	deadline := time.Now().Add(dur)
	var timedOut atomic.Bool
	for i := 0; i < level.users; i++ {
		userID := fmt.Sprintf("loadtest-sse-user-%d", i+1)
		convID := convIDs[i]
		ch := pendingChs[i]

		wg.Add(1)
		go func(userID, convID string, pending chan<- pendingAppend) {
			defer wg.Done()
			senderLoop(ctx, baseURL, apiKey, userID, convID, pending, deadline, &timedOut)
		}(userID, convID, ch)
	}

	wg.Wait()
	res.endTime = time.Now()
	res.samples = allSamples
	return res
}

// subscriberLoop opens a persistent SSE connection for userID and for each
// pending append waits for a conversation event on convID, recording the
// latency from POST completion to event arrival.
//
// ready is closed once the HTTP 200 response is received, signalling the
// caller that this subscriber is live and senders may start.
func subscriberLoop(
	ctx context.Context,
	baseURL, apiKey, userID, convID string,
	pending <-chan pendingAppend,
	eventTimeout time.Duration,
	ready chan<- struct{},
	record func(sample),
) {
	// No conversationIds filter — the server ignores it at the handler level
	// for user-scoped streams; we match on the conversation field in the event
	// payload instead.
	url := fmt.Sprintf("%s/v1/events", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		close(ready)
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-User-ID", userID)

	// Use a transport with disabled keep-alive compression so we get raw SSE.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ssedelay] SSE connect error user=%s: %v\n", userID, err)
		close(ready)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "[ssedelay] SSE connect failed user=%s status=%d body=%s\n",
			userID, resp.StatusCode, body)
		close(ready)
		return
	}

	// sseEnvelope matches the JSON structure written by writeSSEEvent:
	// {"event":"created","kind":"entry","data":{"conversation":"<id>",...}}
	type sseEnvelope struct {
		Kind string `json:"kind"`
		Data struct {
			Conversation string `json:"conversation"`
		} `json:"data"`
	}

	// Start the scanner goroutine BEFORE signalling ready so it is already
	// calling bufio.Scanner.Scan() by the time the first sender fires.
	// If we close(ready) first, the sender can POST and the server can
	// dispatch the SSE event before the scanner has called Read() even once,
	// causing the event to be lost at the select{default:} drop branch.
	eventCh := make(chan sseEnvelope, 256)
	scannerReady := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		// Signal that the scanner is running and blocking on Scan() before
		// the first line arrives.  We do this by closing scannerReady inside
		// the goroutine, just before entering the read loop.  The goroutine
		// is scheduled and reaches this point before the first Scan() blocks.
		close(scannerReady)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			raw := strings.TrimPrefix(line, "data: ")
			var ev sseEnvelope
			if err := json.Unmarshal([]byte(raw), &ev); err != nil {
				continue
			}
			if ev.Data.Conversation == "" {
				continue // keepalive, stream/phase events — not what we measure
			}
			select {
			case eventCh <- ev:
			default:
			}
		}
		close(eventCh)
	}()

	// Wait for the scanner goroutine to reach its read loop, then signal the
	// caller that this subscriber is ready to receive events.
	<-scannerReady

	// Signal that the HTTP connection is live — senders may now start.
	close(ready)

	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-pending:
			if !ok {
				return
			}
			// Wait for a conversation or entry event on our specific convID.
			deadline := time.NewTimer(eventTimeout)
		waitLoop:
			for {
				select {
				case ev, ok := <-eventCh:
					if !ok {
						// Subscriber disconnected while waiting for an event.
						// Signal the in-flight pending append as a timeout so the
						// sender records a failure instead of silently disappearing.
						deadline.Stop()
						p.resultCh <- sample{timedOut: true}
						record(sample{timedOut: true})
						return
					}
					// Match any conversation or entry event for our convID.
					// The primary signal is kind=entry/event=created which fires
					// on every append; kind=conversation/event=updated only fires
					// when a conversationPatch is applied alongside the append.
					if (ev.Kind == "conversation" || ev.Kind == "entry") && ev.Data.Conversation == convID {
						deadline.Stop()
						latency := time.Since(p.appendEnd).Nanoseconds()
						p.resultCh <- sample{latencyNs: latency}
						record(sample{latencyNs: latency})
						break waitLoop
					}
				case <-deadline.C:
					p.resultCh <- sample{timedOut: true}
					record(sample{timedOut: true})
					break waitLoop
				case <-ctx.Done():
					deadline.Stop()
					return
				}
			}
		}
	}
}

// senderLoop repeatedly appends entries to convID until the deadline, sending
// each pending append to the subscriber via the pending channel.
func senderLoop(
	ctx context.Context,
	baseURL, apiKey, userID, convID string,
	pending chan<- pendingAppend,
	deadline time.Time,
	timedOut *atomic.Bool,
) {
	client := &http.Client{Timeout: 10 * time.Second}
	body := []byte(fmt.Sprintf(
		`{"contentType":"history","content":[{"role":"USER","text":"sse delay benchmark entry"}],"userId":%q}`,
		userID,
	))
	url := fmt.Sprintf("%s/v1/conversations/%s/entries", baseURL, convID)

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", apiKey)
		req.Header.Set("X-User-ID", userID)

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "[ssedelay] append error user=%s conv=%s: %v\n", userID, convID, err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
		// Capture appendEnd after the response body is fully consumed.
		// The server has already committed the write and dispatched the SSE
		// event before returning the response; taking the timestamp here gives
		// the cleanest possible start point for the append→event latency window.
		appendEnd := time.Now()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			fmt.Fprintf(os.Stderr, "[ssedelay] append status=%d user=%s\n", resp.StatusCode, userID)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		resultCh := make(chan sample, 1)
		select {
		case pending <- pendingAppend{convID: convID, appendEnd: appendEnd, resultCh: resultCh}:
		case <-ctx.Done():
			return
		}

		// Wait for subscriber to confirm event received (or time out) before
		// firing the next append — this keeps the measurement clean: one
		// outstanding append per user at a time.
		select {
		case <-resultCh:
		case <-ctx.Done():
			return
		}
	}
	timedOut.Store(false) // mark clean completion
}

// runAdminSubscriber opens a persistent GET /v1/admin/events connection and
// discards all events — it exists only to create realistic fan-out pressure.
func runAdminSubscriber(ctx context.Context, baseURL, adminKey string, n int) {
	for {
		if ctx.Err() != nil {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			baseURL+"/v1/admin/events?justification=loadtest-ssedelay", nil)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("X-API-Key", adminKey)

		client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "[ssedelay] admin subscriber %d connect error: %v — retrying\n", n, err)
			time.Sleep(time.Second)
			continue
		}
		fmt.Printf("[ssedelay] admin subscriber %d connected\n", n)
		// Drain and discard.
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if ctx.Err() != nil {
				break
			}
		}
		resp.Body.Close()
		if ctx.Err() != nil {
			return
		}
		// Reconnect on disconnect.
		time.Sleep(500 * time.Millisecond)
	}
}

// provisionConversations creates n conversations owned by per-user IDs and
// returns their IDs.  Each user owns exactly one conversation so SSE routing
// only delivers their events to their own subscriber.
func provisionConversations(baseURL, apiKey string, n int) ([]string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		userID := fmt.Sprintf("loadtest-sse-user-%d", i+1)
		id, err := createConv(client, baseURL, apiKey, userID,
			fmt.Sprintf("sse-delay-bench-user-%d", i+1))
		if err != nil {
			return nil, fmt.Errorf("user %d: %w", i+1, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func createConv(client *http.Client, baseURL, apiKey, userID, title string) (string, error) {
	body, _ := json.Marshal(map[string]string{"title": title})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/conversations", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-User-ID", userID)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("createConv status=%d body=%s", resp.StatusCode, respBody)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	return result.ID, nil
}

// percentileNs returns the p-th percentile (0–100) of nanosecond latencies.
func percentileNs(sorted []int64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100.0)
	return float64(sorted[idx])
}

func printLevelSummary(res levelResult) {
	good := collectGoodNs(res.samples)
	timeouts := 0
	for _, s := range res.samples {
		if s.timedOut {
			timeouts++
		}
	}
	sort.Slice(good, func(i, j int) bool { return good[i] < good[j] })
	p50 := percentileNs(good, 50) / 1e6
	p95 := percentileNs(good, 95) / 1e6
	p99 := percentileNs(good, 99) / 1e6
	fmt.Printf("[ssedelay] %s — samples=%d timeouts=%d  p50=%.0fms  p95=%.0fms  p99=%.0fms\n",
		res.label, len(good), timeouts, p50, p95, p99)
}

func collectGoodNs(samples []sample) []int64 {
	out := make([]int64, 0, len(samples))
	for _, s := range samples {
		if !s.timedOut {
			out = append(out, s.latencyNs)
		}
	}
	return out
}

// writeOutput writes a JSON file consumable by the report aggregator.
// Format mirrors the Hyperfoil /stats/total response so the existing
// loadHyperfoilResults() parser handles it with a multi-metric path.
func writeOutput(path string, results []levelResult) error {
	type percentiles struct {
		P50 float64 `json:"50.0"`
		P95 float64 `json:"95.0"`
		P99 float64 `json:"99.0"`
	}
	type summary struct {
		RequestCount           float64     `json:"requestCount"`
		StartTime              float64     `json:"startTime"`
		EndTime                float64     `json:"endTime"`
		PercentileResponseTime percentiles `json:"percentileResponseTime"`
		Timeouts               int         `json:"timeouts"`
	}
	type statEntry struct {
		Phase   string  `json:"phase"`
		Metric  string  `json:"metric"`
		Summary summary `json:"summary"`
	}

	var stats []statEntry
	for _, res := range results {
		good := collectGoodNs(res.samples)
		sort.Slice(good, func(i, j int) bool { return good[i] < good[j] })
		timeouts := 0
		for _, s := range res.samples {
			if s.timedOut {
				timeouts++
			}
		}

		durMs := res.endTime.Sub(res.startTime).Seconds() * 1000
		rps := 0.0
		if durMs > 0 {
			rps = float64(len(good)) / (durMs / 1000.0)
		}
		_ = rps

		stats = append(stats, statEntry{
			Phase:  res.label,
			Metric: res.label,
			Summary: summary{
				RequestCount: float64(len(good)),
				StartTime:    float64(res.startTime.UnixMilli()),
				EndTime:      float64(res.endTime.UnixMilli()),
				PercentileResponseTime: percentiles{
					P50: percentileNs(good, 50),
					P95: percentileNs(good, 95),
					P99: percentileNs(good, 99),
				},
				Timeouts: timeouts,
			},
		})
	}

	out := map[string]any{
		"name":       "sse-event-delay",
		"status":     "TERMINATED",
		"statistics": stats,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[ssedelay] FATAL: "+format+"\n", args...)
	os.Exit(1)
}
