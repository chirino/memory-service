//go:build ignore

// manifest-to-csv reads loadtest/results/seed-manifest.json and writes CSV
// files consumed by Hyperfoil benchmark flows.  It also live-fetches deep
// cursors from the running service for benchmarks that need to measure deep-
// page latency.
//
// Output files:
//
//   - loadtest/results/conversation-ids.csv
//       All seeded conversations.  THREE columns:
//         col 0  conversationId
//         col 1  ownerID
//         col 2  deepCursor  (cursor pointing to page 5 of the list; empty if <5 pages)
//
//   - loadtest/results/long-tail-conversation-ids.csv
//       Conversations with >100 entries.  THREE columns:
//         col 0  conversationId
//         col 1  ownerID
//         col 2  deepCursor  (cursor pointing to page 10 of entries; empty if <10 pages)
//
//   - loadtest/results/deep-conversations.csv
//       Subset of conversation-ids.csv where deepCursor is non-empty.
//       Used by list-conversations.hf.yaml for the deep-page request.
//
//   - loadtest/results/deep-entries.csv
//       Subset of long-tail-conversation-ids.csv where deepCursor is non-empty.
//       Used by list-entries.hf.yaml for the deep-page request.
//
//   - loadtest/results/deep-search.csv
//       Per-owner deep search cursor (page 3 of search results).
//       THREE columns: ownerID, ownerID (duplicate for column alignment), deepCursor.
//       Used by search-conversations.hf.yaml for the deep-page request.
//
//   - loadtest/results/sse-conversation-ids.csv
//   - loadtest/results/fork-root-ids.csv
//
// Usage:
//
//	go run ./loadtest/benchmarks/manifest-to-csv.go [--manifest path] [--results-dir dir]
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type conversationRecord struct {
	ID              string `json:"id"`
	OwnerID         string `json:"ownerID"`
	EntryCount      int    `json:"entryCount"`
	ParticipantType string `json:"participantType"`
}

type forkRecord struct {
	RootID          string `json:"rootId"`
	ForkID          string `json:"forkId"`
	ForkedAtEntryID string `json:"forkedAtEntryId"`
}

type seedManifest struct {
	BaseURL            string               `json:"baseURL"`
	TotalConversations int                  `json:"totalConversations"`
	Conversations      []conversationRecord `json:"conversations"`
	Forks              []forkRecord         `json:"forks"`
}

func main() {
	manifestPath := flag.String("manifest", "loadtest/results/seed-manifest.json", "Path to seed-manifest.json")
	resultsDir := flag.String("results-dir", "loadtest/results", "Directory to write CSV files")
	flag.Parse()

	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: read manifest: %v\n", err)
		os.Exit(1)
	}

	var manifest seedManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: parse manifest: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*resultsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: mkdir: %v\n", err)
		os.Exit(1)
	}

	baseURL := manifest.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:8082"
	}
	client := &http.Client{Timeout: 15 * time.Second}

	// -----------------------------------------------------------------------
	// conversation-ids.csv — all conversations with deep cursor (col 2).
	// Deep cursor = cursor returned after page 5 of GET /v1/conversations
	// for the ownerID.  One cursor fetch per unique owner (not per row) to
	// avoid N*2000 HTTP calls.
	// -----------------------------------------------------------------------
	fmt.Fprintln(os.Stderr, "Fetching deep cursors for conversations (1 call per unique owner)...")
	ownerCursors := fetchOwnerDeepCursors(client, baseURL, manifest.Conversations, 5)

	allPath := *resultsDir + "/conversation-ids.csv"
	deepConvPath := *resultsDir + "/deep-conversations.csv"
	var allRows, deepConvRows [][]string
	for _, c := range manifest.Conversations {
		cursor := ownerCursors[c.OwnerID]
		allRows = append(allRows, []string{c.ID, c.OwnerID, cursor})
		if cursor != "" {
			deepConvRows = append(deepConvRows, []string{c.ID, c.OwnerID, cursor})
		}
	}
	if err := writeCSVRows(allPath, allRows); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: write %s: %v\n", allPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d rows to %s\n", len(allRows), allPath)
	if err := writeCSVRows(deepConvPath, deepConvRows); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: write %s: %v\n", deepConvPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d rows to %s\n", len(deepConvRows), deepConvPath)

	// -----------------------------------------------------------------------
	// long-tail-conversation-ids.csv — conversations with >100 entries.
	// Deep cursor = cursor returned after page 10 of GET /v1/.../entries.
	// Fetched per conversation (each has its own cursor sequence).
	// -----------------------------------------------------------------------
	var longTail []conversationRecord
	for _, c := range manifest.Conversations {
		if c.EntryCount > 100 {
			longTail = append(longTail, c)
		}
	}

	fmt.Fprintf(os.Stderr, "Fetching deep cursors for %d long-tail conversations (1 call each)...\n", len(longTail))
	var longTailRows, deepEntryRows [][]string
	for i, c := range longTail {
		if (i+1)%50 == 0 {
			fmt.Fprintf(os.Stderr, "  %d/%d\n", i+1, len(longTail))
		}
		cursor := fetchEntriesDeepCursor(client, baseURL, c.ID, c.OwnerID, 10)
		longTailRows = append(longTailRows, []string{c.ID, c.OwnerID, cursor})
		if cursor != "" {
			deepEntryRows = append(deepEntryRows, []string{c.ID, c.OwnerID, cursor})
		}
	}
	longPath := *resultsDir + "/long-tail-conversation-ids.csv"
	deepEntryPath := *resultsDir + "/deep-entries.csv"
	if err := writeCSVRows(longPath, longTailRows); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: write %s: %v\n", longPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d rows to %s\n", len(longTailRows), longPath)
	if err := writeCSVRows(deepEntryPath, deepEntryRows); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: write %s: %v\n", deepEntryPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d rows to %s\n", len(deepEntryRows), deepEntryPath)

	// -----------------------------------------------------------------------
	// sse-conversation-ids.csv — loadtest-user-1 conversations only.
	// -----------------------------------------------------------------------
	var sseConvs []conversationRecord
	for _, c := range manifest.Conversations {
		if c.OwnerID == "loadtest-user-1" {
			sseConvs = append(sseConvs, c)
		}
	}
	ssePath := *resultsDir + "/sse-conversation-ids.csv"
	var sseRows [][]string
	for _, c := range sseConvs {
		sseRows = append(sseRows, []string{c.ID, c.OwnerID})
	}
	if err := writeCSVRows(ssePath, sseRows); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: write %s: %v\n", ssePath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d rows to %s\n", len(sseRows), ssePath)

	// -----------------------------------------------------------------------
	// fork-root-ids.csv — fork root conversations.
	// -----------------------------------------------------------------------
	forkOwnerIndex := make(map[string]string)
	for _, conv := range manifest.Conversations {
		forkOwnerIndex[conv.ID] = conv.OwnerID
	}
	forkPath := *resultsDir + "/fork-root-ids.csv"
	var forkRows [][]string
	for _, f := range manifest.Forks {
		ownerID := forkOwnerIndex[f.RootID]
		if ownerID == "" {
			ownerID = "loadtest-user-1"
		}
		forkRows = append(forkRows, []string{f.RootID, ownerID})
	}
	if err := writeCSVRows(forkPath, forkRows); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: write %s: %v\n", forkPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d rows to %s\n", len(forkRows), forkPath)

	// -----------------------------------------------------------------------
	// deep-search.csv — one row per owner with a deep search cursor.
	// Deep cursor = cursor returned after page 3 of POST /v1/conversations/search.
	// Only owners that have enough indexed conversations to reach page 3 are included.
	// THREE columns: conversationId (reused as placeholder), ownerID, deepCursor.
	// -----------------------------------------------------------------------
	// Collect unique owners.
	seenOwners := make(map[string]bool)
	var owners []string
	for _, c := range manifest.Conversations {
		if !seenOwners[c.OwnerID] {
			seenOwners[c.OwnerID] = true
			owners = append(owners, c.OwnerID)
		}
	}
	fmt.Fprintf(os.Stderr, "Fetching deep search cursors for %d unique owners...\n", len(owners))
	deepSearchPath := *resultsDir + "/deep-search.csv"
	var deepSearchRows [][]string
	for _, ownerID := range owners {
		cursor := fetchSearchDeepCursor(client, baseURL, ownerID, 3)
		if cursor != "" {
			// Use ownerID as col 0 placeholder so randomCsvRow can bind col 1 = ownerID, col 2 = cursor.
			deepSearchRows = append(deepSearchRows, []string{ownerID, ownerID, cursor})
		}
	}
	if err := writeCSVRows(deepSearchPath, deepSearchRows); err != nil {
		fmt.Fprintf(os.Stderr, "manifest-to-csv: write %s: %v\n", deepSearchPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d rows to %s\n", len(deepSearchRows), deepSearchPath)
}

// fetchOwnerDeepCursors fetches a cursor for page targetPage of
// GET /v1/conversations?mode=all&limit=20 for each unique ownerID.
// Returns a map of ownerID -> cursor (empty string if fewer than targetPage pages).
func fetchOwnerDeepCursors(client *http.Client, baseURL string, convs []conversationRecord, targetPage int) map[string]string {
	// Collect unique owners.
	seenOwners := make(map[string]bool)
	var owners []string
	for _, c := range convs {
		if !seenOwners[c.OwnerID] {
			seenOwners[c.OwnerID] = true
			owners = append(owners, c.OwnerID)
		}
	}

	result := make(map[string]string, len(owners))
	for _, ownerID := range owners {
		cursor := walkToPage(client, baseURL, ownerID, targetPage)
		result[ownerID] = cursor
	}
	return result
}

// walkToPage walks targetPage pages of GET /v1/conversations?mode=all&limit=20
// for the given owner and returns the afterCursor from the last response.
// Returns "" if the owner has fewer than targetPage pages.
func walkToPage(client *http.Client, baseURL, ownerID string, targetPage int) string {
	cursor := ""
	for page := 1; page <= targetPage; page++ {
		url := baseURL + "/v1/conversations?mode=all&limit=20"
		if cursor != "" {
			url += "&afterCursor=" + cursor
		}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return ""
		}
		req.Header.Set("X-API-Key", "agent-api-key-1")
		req.Header.Set("X-User-ID", ownerID)

		resp, err := client.Do(req)
		if err != nil {
			return ""
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ""
		}

		var result struct {
			AfterCursor *string `json:"afterCursor"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return ""
		}
		if result.AfterCursor == nil || *result.AfterCursor == "" {
			return "" // not enough pages
		}
		cursor = *result.AfterCursor
	}
	return cursor
}

// fetchEntriesDeepCursor walks targetPage pages of
// GET /v1/conversations/{id}/entries?limit=10 and returns the afterCursor
// from the last response. Returns "" if the conversation has fewer pages.
func fetchEntriesDeepCursor(client *http.Client, baseURL, convID, ownerID string, targetPage int) string {
	cursor := ""
	for page := 1; page <= targetPage; page++ {
		url := fmt.Sprintf("%s/v1/conversations/%s/entries?limit=10", baseURL, convID)
		if cursor != "" {
			url += "&afterCursor=" + cursor
		}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return ""
		}
		req.Header.Set("X-API-Key", "agent-api-key-1")
		req.Header.Set("X-User-ID", ownerID)

		resp, err := client.Do(req)
		if err != nil {
			return ""
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ""
		}

		var result struct {
			AfterCursor *string `json:"afterCursor"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return ""
		}
		if result.AfterCursor == nil || *result.AfterCursor == "" {
			return ""
		}
		cursor = *result.AfterCursor
	}
	return cursor
}

// fetchSearchDeepCursor walks targetPage pages of
// POST /v1/conversations/search with query "load test" limit 5 and returns
// the afterCursor from the last response. Returns "" if fewer pages exist.
func fetchSearchDeepCursor(client *http.Client, baseURL, ownerID string, targetPage int) string {
	cursor := ""
	for page := 1; page <= targetPage; page++ {
		var bodyBytes []byte
		if cursor == "" {
			bodyBytes = []byte(`{"query":"load test","limit":5}`)
		} else {
			bodyBytes = []byte(fmt.Sprintf(`{"query":"load test","limit":5,"afterCursor":%q}`, cursor))
		}
		req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/conversations/search", nil)
		if err != nil {
			return ""
		}
		req.Body = io.NopCloser(newBytesReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "agent-api-key-1")
		req.Header.Set("X-User-ID", ownerID)

		resp, err := client.Do(req)
		if err != nil {
			return ""
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ""
		}

		var result struct {
			AfterCursor *string `json:"afterCursor"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return ""
		}
		if result.AfterCursor == nil || *result.AfterCursor == "" {
			return ""
		}
		cursor = *result.AfterCursor
	}
	return cursor
}

// newBytesReader is a thin wrapper so we can set a body on repeated requests.
func newBytesReader(b []byte) *bytesReader {
	return &bytesReader{data: b}
}

type bytesReader struct {
	data   []byte
	offset int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func writeCSVRows(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
