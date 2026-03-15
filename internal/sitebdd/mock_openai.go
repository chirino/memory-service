//go:build site_tests

package sitebdd

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// capturedCall holds one recorded OpenAI chat completion request/response pair.
type capturedCall struct {
	StatusCode int
	Headers    map[string]string
	Body       string
}

type chunkedDribbleDelay struct {
	NumberOfChunks int `json:"numberOfChunks"`
	TotalDuration  int `json:"totalDuration"`
}

type fixtureResponse struct {
	Body                string
	Headers             map[string]string
	Status              int
	ChunkedDribbleDelay *chunkedDribbleDelay `json:"chunkedDribbleDelay"`
}

// mockScenarioState is per-scenario state inside the shared mock server.
type mockScenarioState struct {
	mu           sync.Mutex
	checkpointID string
	recording    bool
	fixtureIndex int            // next fixture index (0-based) to serve in playback
	journal      []capturedCall // accumulated during recording
}

// RegisterScenario registers a scenario with the mock so it can serve fixture requests.
func (m *MockServer) RegisterScenario(uid, checkpointID string, recording bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registry[uid] = &mockScenarioState{
		checkpointID: checkpointID,
		recording:    recording,
	}
}

// UnregisterScenario removes a scenario from the registry.
func (m *MockServer) UnregisterScenario(uid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.registry, uid)
}

// GetJournal returns the captured calls for the given scenario UID.
func (m *MockServer) GetJournal(uid string) []capturedCall {
	m.mu.RLock()
	state := m.registry[uid]
	m.mu.RUnlock()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]capturedCall(nil), state.journal...)
}

// HasFixtures returns true if at least one fixture file exists for the checkpoint.
func (m *MockServer) HasFixtures(checkpointID string) bool {
	dir := m.fixtureDir(checkpointID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			return true
		}
	}
	return false
}

// SaveJournal writes WireMock-compatible fixture files for the given checkpoint.
func (m *MockServer) SaveJournal(checkpointID string, journal []capturedCall) error {
	if len(journal) == 0 {
		fmt.Printf("[openai-mock] No chat completions captured for %s, skipping save\n", checkpointID)
		return nil
	}

	dir := m.fixtureDir(checkpointID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create fixture dir: %w", err)
	}

	// Clear existing fixtures
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}

	for i, call := range journal {
		stub := map[string]any{
			"scenarioName": "chat-sequence",
			"request": map[string]any{
				"method":  "POST",
				"urlPath": "/v1/chat/completions",
			},
			"response": map[string]any{
				"status":  call.StatusCode,
				"headers": map[string]string{"Content-Type": "application/json"},
				"body":    call.Body,
			},
		}

		if i == 0 {
			stub["requiredScenarioState"] = "Started"
		} else {
			stub["requiredScenarioState"] = fmt.Sprintf("step-%d", i+1)
		}
		if i < len(journal)-1 {
			stub["newScenarioState"] = fmt.Sprintf("step-%d", i+2)
		}

		data, err := json.MarshalIndent(stub, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal fixture %d: %w", i, err)
		}

		filename := fmt.Sprintf("%03d.json", i+1)
		if err := os.WriteFile(filepath.Join(dir, filename), data, 0o644); err != nil {
			return fmt.Errorf("write fixture %s: %w", filename, err)
		}
		fmt.Printf("[openai-mock] Saved fixture: %s/%s\n", checkpointID, filename)
	}
	fmt.Printf("[openai-mock] Saved %d fixture(s) for %s\n", len(journal), checkpointID)
	return nil
}

// fixtureDir returns the path to the fixture directory for a checkpoint.
// Mapping examples:
//
//	"java/quarkus/examples/chat-quarkus/01-basic-agent"    → <fixturesDir>/quarkus/01-basic-agent
//	"python/examples/langchain/doc-checkpoints/03-with-history" → <fixturesDir>/python-langchain/03-with-history
//	"python/examples/langgraph/doc-checkpoints/30-memories"     → <fixturesDir>/python-langgraph/30-memories
//	"typescript/examples/vecelai/doc-checkpoints/03-with-history" → <fixturesDir>/typescript-vecelai/03-with-history
func (m *MockServer) fixtureDir(checkpointID string) string {
	name := lastSegment(checkpointID)
	var framework string
	switch {
	case strings.HasPrefix(checkpointID, "java/quarkus/"):
		framework = "quarkus"
	case strings.HasPrefix(checkpointID, "java/spring/"):
		framework = "spring"
	case strings.HasPrefix(checkpointID, "python/") && strings.Contains(checkpointID, "/langchain/"):
		framework = "python-langchain"
	case strings.HasPrefix(checkpointID, "python/") && strings.Contains(checkpointID, "/langgraph/"):
		framework = "python-langgraph"
	case strings.HasPrefix(checkpointID, "typescript/") && strings.Contains(checkpointID, "/vecelai/"):
		framework = "typescript-vecelai"
	default:
		parts := strings.SplitN(checkpointID, "/", 2)
		framework = parts[0]
	}
	return filepath.Join(m.fixturesDir, framework, name)
}

// extractUID parses "Bearer sitebdd-<uid>" → "<uid>".
func extractUID(auth string) string {
	const prefix = "Bearer sitebdd-"
	if strings.HasPrefix(auth, prefix) {
		return strings.TrimPrefix(auth, prefix)
	}
	return ""
}

func (m *MockServer) getState(uid string) *mockScenarioState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registry[uid]
}

func (m *MockServer) handleModels(w http.ResponseWriter, r *http.Request) {
	mappingsFile := filepath.Join(m.projectRoot, "internal", "sitebdd", "testdata", "openai-mock", "mappings", "models.json")
	data, err := os.ReadFile(mappingsFile)
	if err != nil {
		// Fallback minimal response
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"mock-gpt-markdown","object":"model"}]}`))
		return
	}

	var mapping struct {
		Response struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &mapping); err != nil {
		http.Error(w, "bad mapping", 500)
		return
	}

	for k, v := range mapping.Response.Headers {
		w.Header().Set(k, v)
	}
	if mapping.Response.Status != 0 {
		w.WriteHeader(mapping.Response.Status)
	}
	_, _ = w.Write([]byte(mapping.Response.Body))
}

func (m *MockServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	uid := extractUID(authHeader)
	state := m.getState(uid)

	// Read request body (needed for recording and for detecting streaming)
	reqBody, _ := io.ReadAll(r.Body)

	if state != nil && state.recording {
		m.proxyToOpenAI(w, r, reqBody, state)
		return
	}

	if state != nil {
		// Playback mode: serve next fixture
		state.mu.Lock()
		idx := state.fixtureIndex
		state.fixtureIndex++
		checkpointID := state.checkpointID
		state.mu.Unlock()

		if fixture, ok := m.loadFixture(checkpointID, idx); ok {
			// Use fixture headers if present; default to application/json.
			ct := fixture.Headers["Content-Type"]
			if ct == "" {
				ct = "application/json"
			}
			w.Header().Set("Content-Type", ct)
			for k, v := range fixture.Headers {
				if strings.EqualFold(k, "Content-Type") {
					continue // already set above
				}
				w.Header().Set(k, v)
			}
			w.WriteHeader(fixture.Status)
			if err := writeFixtureBody(w, fixture); err != nil {
				log.Printf("[openai-mock] fixture write failed: checkpoint=%q fixtureIndex=%d error=%v", checkpointID, idx+1, err)
			}
			return
		}
		m.fatalMockFailure(
			"missing fixture during playback",
			fmt.Sprintf("uid=%q checkpoint=%q fixtureIndex=%d auth=%q", uid, checkpointID, idx+1, authHeader),
			reqBody,
		)
		return
	} else {
		m.fatalMockFailure(
			"request had no registered scenario state",
			fmt.Sprintf("uid=%q auth=%q", uid, authHeader),
			reqBody,
		)
		return
	}
}

// loadFixture reads NNN.json for the given 0-based index from the fixture directory.
func (m *MockServer) loadFixture(checkpointID string, idx int) (fixtureResponse, bool) {
	dir := m.fixtureDir(checkpointID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fixtureResponse{}, false
	}

	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	if idx >= len(files) {
		return fixtureResponse{}, false
	}

	data, err := os.ReadFile(filepath.Join(dir, files[idx]))
	if err != nil {
		return fixtureResponse{}, false
	}

	var stub struct {
		Response fixtureResponse `json:"response"`
	}
	if err := json.Unmarshal(data, &stub); err != nil {
		return fixtureResponse{}, false
	}

	if stub.Response.Status == 0 {
		stub.Response.Status = 200
	}
	return stub.Response, true
}

func writeFixtureBody(w http.ResponseWriter, fixture fixtureResponse) error {
	dribble := fixture.ChunkedDribbleDelay
	if dribble == nil || dribble.NumberOfChunks <= 1 || dribble.TotalDuration <= 0 {
		_, err := io.WriteString(w, fixture.Body)
		return err
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		_, err := io.WriteString(w, fixture.Body)
		return err
	}

	body := []byte(fixture.Body)
	if len(body) == 0 {
		return nil
	}

	if strings.Contains(strings.ToLower(fixture.Headers["Content-Type"]), "text/event-stream") {
		return writeFixtureSSEBody(w, fixture.Body, dribble.TotalDuration, flusher)
	}

	chunkCount := dribble.NumberOfChunks
	if chunkCount > len(body) {
		chunkCount = len(body)
	}
	chunkSize := (len(body) + chunkCount - 1) / chunkCount
	interval := time.Duration(dribble.TotalDuration) * time.Millisecond
	if chunkCount > 1 {
		interval /= time.Duration(chunkCount - 1)
	}

	for offset := 0; offset < len(body); offset += chunkSize {
		end := offset + chunkSize
		if end > len(body) {
			end = len(body)
		}
		if _, err := w.Write(body[offset:end]); err != nil {
			return err
		}
		flusher.Flush()
		if end < len(body) && interval > 0 {
			time.Sleep(interval)
		}
	}
	return nil
}

func writeFixtureSSEBody(w http.ResponseWriter, body string, totalDurationMs int, flusher http.Flusher) error {
	events := splitSSEEvents(body)
	if len(events) == 0 {
		_, err := io.WriteString(w, body)
		return err
	}

	interval := time.Duration(totalDurationMs) * time.Millisecond
	if len(events) > 1 {
		interval /= time.Duration(len(events) - 1)
	}

	for i, event := range events {
		if _, err := io.WriteString(w, event); err != nil {
			return err
		}
		flusher.Flush()
		if i < len(events)-1 && interval > 0 {
			time.Sleep(interval)
		}
	}
	return nil
}

func splitSSEEvents(body string) []string {
	var events []string
	for _, event := range strings.Split(body, "\n\n") {
		if event == "" {
			continue
		}
		events = append(events, event+"\n\n")
	}
	return events
}

func (m *MockServer) fatalMockFailure(reason, context string, reqBody []byte) {
	log.Printf("[openai-mock] FATAL: %s; %s; request-body=%s", reason, context, string(reqBody))
	killAllActiveCheckpoints("mock fatal failure")
	os.Exit(2)
}

// proxyToOpenAI forwards the request to the real OpenAI API and captures the response.
func (m *MockServer) proxyToOpenAI(w http.ResponseWriter, r *http.Request, reqBody []byte, state *mockScenarioState) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	apiBase := os.Getenv("OPENAI_API_BASE")
	if apiBase == "" {
		apiBase = "https://api.openai.com"
	}

	targetURL := apiBase + r.URL.Path
	proxyReq, err := http.NewRequest(r.Method, targetURL, strings.NewReader(string(reqBody)))
	if err != nil {
		http.Error(w, "proxy error: "+err.Error(), 500)
		return
	}

	// Copy original headers except Authorization, then set real API key
	for k, vs := range r.Header {
		if strings.ToLower(k) != "authorization" {
			for _, v := range vs {
				proxyReq.Header.Add(k, v)
			}
		}
	}
	proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
	// Record plain JSON payloads so saved fixtures are replayable without
	// Content-Encoding metadata.
	proxyReq.Header.Set("Accept-Encoding", "identity")

	client := &http.Client{}
	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "read upstream body: "+err.Error(), 502)
		return
	}

	// Capture for journal
	call := capturedCall{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
	}
	state.mu.Lock()
	state.journal = append(state.journal, call)
	state.mu.Unlock()

	fmt.Printf("[openai-mock] Recorded call %d for %s\n", len(state.journal), state.checkpointID)

	// Forward response to caller
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}
