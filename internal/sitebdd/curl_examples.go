//go:build site_tests

package sitebdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type curlCaptureMode string

const (
	curlCaptureOff     curlCaptureMode = "off"
	curlCaptureMissing curlCaptureMode = "missing"
	curlCaptureAll     curlCaptureMode = "all"
)

type CurlExampleCapture struct {
	CaptureID   string `json:"captureId"`
	Checkpoint  string `json:"checkpoint"`
	Scenario    string `json:"scenario"`
	RequestURL  string `json:"requestUrl"`
	Method      string `json:"method"`
	StatusCode  int    `json:"statusCode"`
	ContentType string `json:"contentType,omitempty"`
	Body        string `json:"body"`
	CapturedAt  string `json:"capturedAt"`
}

var curlCaptureFileMu sync.Mutex

func captureModeFromEnv() curlCaptureMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SITE_TEST_CAPTURE_CURL_OUTPUT"))) {
	case "", "off", "false":
		return curlCaptureOff
	case "missing", "true":
		return curlCaptureMissing
	case "all", "force":
		return curlCaptureAll
	default:
		return curlCaptureOff
	}
}

func (s *SiteScenario) recordCurlExample() {
	if captureModeFromEnv() == curlCaptureOff {
		s.pendingCurlCaptureID = ""
		return
	}
	if s.lastCurlReq == nil {
		s.pendingCurlCaptureID = ""
		return
	}

	s.curlCaptureSeq++
	captureID := strings.TrimSpace(s.pendingCurlCaptureID)
	if captureID == "" {
		captureID = fmt.Sprintf("%s#%d", s.CheckpointID, s.curlCaptureSeq)
	}
	s.pendingCurlCaptureID = ""

	contentType := strings.TrimSpace(s.lastRespCT)
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}

	s.curlCaptures = append(s.curlCaptures, CurlExampleCapture{
		CaptureID:   captureID,
		Checkpoint:  s.CheckpointID,
		Scenario:    s.ScenarioName,
		RequestURL:  s.lastCurlReq.URL,
		Method:      strings.ToUpper(strings.TrimSpace(s.lastCurlReq.Method)),
		StatusCode:  s.LastStatusCode,
		ContentType: contentType,
		Body:        s.LastRespBody,
		CapturedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *SiteScenario) saveCurlExamples() error {
	mode := captureModeFromEnv()
	if mode == curlCaptureOff || len(s.curlCaptures) == 0 {
		return nil
	}
	if strings.TrimSpace(s.ProjectRoot) == "" {
		return fmt.Errorf("missing project root")
	}

	dir := filepath.Join(
		s.ProjectRoot,
		"internal",
		"sitebdd",
		"testdata",
		"curl-examples",
		deriveFrameworkForCheckpoint(s.CheckpointID),
	)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create curl examples dir: %w", err)
	}
	path := filepath.Join(dir, lastSegment(s.CheckpointID)+".json")

	curlCaptureFileMu.Lock()
	defer curlCaptureFileMu.Unlock()

	var existing []CurlExampleCapture
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parse existing curl examples %s: %w", path, err)
		}
	}

	byID := map[string]CurlExampleCapture{}
	for _, item := range existing {
		if strings.TrimSpace(item.CaptureID) == "" {
			continue
		}
		byID[item.CaptureID] = item
	}
	for _, item := range s.curlCaptures {
		if mode == curlCaptureMissing {
			if _, exists := byID[item.CaptureID]; exists {
				continue
			}
		}
		byID[item.CaptureID] = item
	}

	keys := make([]string, 0, len(byID))
	for id := range byID {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	merged := make([]CurlExampleCapture, 0, len(keys))
	for _, id := range keys {
		merged = append(merged, byID[id])
	}

	payload, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("encode curl examples: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write curl examples %s: %w", path, err)
	}
	fmt.Printf("[curl-examples] Saved %d capture(s) to %s\n", len(merged), path)
	return nil
}

func deriveFrameworkForCheckpoint(checkpointID string) string {
	switch {
	case strings.HasPrefix(checkpointID, "python/") && strings.Contains(checkpointID, "/langchain/"):
		return "python-langchain"
	case strings.HasPrefix(checkpointID, "python/") && strings.Contains(checkpointID, "/langgraph/"):
		return "python-langgraph"
	case strings.HasPrefix(checkpointID, "typescript/") && strings.Contains(checkpointID, "/vecelai/"):
		return "typescript-vecelai"
	default:
		parts := strings.SplitN(strings.TrimSpace(checkpointID), "/", 2)
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
		return "unknown"
	}
}
