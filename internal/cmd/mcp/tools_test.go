//go:build sqlite_fts5

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/generated/apiclient"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setupTestServer(t *testing.T) *mcpServer {
	t.Helper()

	dbURL := filepath.Join(t.TempDir(), "memory.db")

	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = dbURL
	cfg.CacheType = "none"
	cfg.AttachType = "fs"
	cfg.VectorType = "none"
	cfg.SearchSemanticEnabled = false
	cfg.EncryptionKey = testEncryptionKey
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	cfg.AdminUsers = "alice,alice-*"
	cfg.AuditorUsers = "alice,alice-*"
	cfg.IndexerUsers = "alice,alice-*"
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false

	ctx := config.WithContext(context.Background(), &cfg)
	srv, err := serve.StartServer(ctx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	apiURL := fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	client, err := apiclient.NewClientWithResponses(
		apiURL,
		apiclient.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		apiclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("X-API-Key", "test-key")
			req.Header.Set("Authorization", "Bearer alice")
			return nil
		}),
	)
	require.NoError(t, err)

	return &mcpServer{client: client}
}

func callTool(t *testing.T, s *mcpServer, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	result, err := handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "tool returned error: %v", result.Content)
	require.NotEmpty(t, result.Content)
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	return text.Text
}

func TestSaveAndGetSession(t *testing.T) {
	s := setupTestServer(t)

	// Save session notes
	saveResult := callTool(t, s, s.handleSaveSessionNotes, map[string]any{
		"title": "Test session",
		"notes": "These are test notes about a bugfix.",
		"tags":  "test,bugfix",
	})
	assert.Contains(t, saveResult, "Session notes saved")
	assert.Contains(t, saveResult, "Conversation ID:")

	// Extract conversation ID
	convID := extractConversationID(t, saveResult)

	// Get session and verify content
	getResult := callTool(t, s, s.handleGetSession, map[string]any{
		"conversation_id": convID,
	})
	assert.Contains(t, getResult, "Test session")
	assert.Contains(t, getResult, "These are test notes about a bugfix.")
	assert.Contains(t, getResult, "Tags: test,bugfix")
}

func TestAppendNote(t *testing.T) {
	s := setupTestServer(t)

	// Save initial session
	saveResult := callTool(t, s, s.handleSaveSessionNotes, map[string]any{
		"title": "Append test",
		"notes": "Initial notes.",
	})
	convID := extractConversationID(t, saveResult)

	// Append additional note
	appendResult := callTool(t, s, s.handleAppendNote, map[string]any{
		"conversation_id": convID,
		"notes":           "Follow-up note with more details.",
	})
	assert.Contains(t, appendResult, "Note appended")

	// Verify both entries are present
	getResult := callTool(t, s, s.handleGetSession, map[string]any{
		"conversation_id": convID,
	})
	assert.Contains(t, getResult, "Initial notes.")
	assert.Contains(t, getResult, "Follow-up note with more details.")
}

func TestListSessions(t *testing.T) {
	s := setupTestServer(t)

	// Save a couple of sessions
	callTool(t, s, s.handleSaveSessionNotes, map[string]any{
		"title": "First session",
		"notes": "First notes.",
	})
	callTool(t, s, s.handleSaveSessionNotes, map[string]any{
		"title": "Second session",
		"notes": "Second notes.",
	})

	// List and verify
	listResult := callTool(t, s, s.handleListSessions, map[string]any{
		"limit": float64(10),
	})
	assert.Contains(t, listResult, "Recent sessions")
	assert.Contains(t, listResult, "First session")
	assert.Contains(t, listResult, "Second session")
}

func TestListSessionsEmpty(t *testing.T) {
	s := setupTestServer(t)

	listResult := callTool(t, s, s.handleListSessions, map[string]any{})
	assert.Contains(t, listResult, "No sessions found")
}

func TestSearchSessions(t *testing.T) {
	s := setupTestServer(t)

	// Save a session with indexedContent so FTS5 can find it without a background indexer.
	callTool(t, s, s.handleSaveSessionNotes, map[string]any{
		"title": "Cache serialization fix",
		"notes": "Fixed the JSON marshal symmetry issue in cache layer.",
		"tags":  "bugfix,cache",
	})

	// The save_session_notes tool does not set indexedContent, so the entry
	// won't appear in FTS5 search without a background indexer. Manually
	// append an indexed entry so we can verify the search tool works.
	convID := extractConversationID(t, callTool(t, s, s.handleSaveSessionNotes, map[string]any{
		"title": "Indexed session",
		"notes": "Searchable content about deployment.",
	}))
	indexedContent := "Searchable content about deployment"
	contentType := "history"
	_, err := s.client.AppendConversationEntryWithResponse(context.Background(),
		uuid.MustParse(convID),
		apiclient.AppendConversationEntryJSONRequestBody{
			ContentType:    contentType,
			IndexedContent: &indexedContent,
			Content: []interface{}{
				map[string]any{"role": "USER", "text": "Searchable content about deployment."},
			},
		})
	require.NoError(t, err)

	searchResult := callTool(t, s, s.handleSearchSessions, map[string]any{
		"query": "deployment",
		"limit": float64(5),
	})
	assert.Contains(t, searchResult, "Found")
	assert.Contains(t, searchResult, "Indexed session")
}

func TestSearchSessionsNoResults(t *testing.T) {
	s := setupTestServer(t)

	searchResult := callTool(t, s, s.handleSearchSessions, map[string]any{
		"query": "nonexistent topic xyz",
	})
	assert.Contains(t, searchResult, "No matching sessions found")
}

func extractConversationID(t *testing.T, result string) string {
	t.Helper()
	for _, line := range strings.Split(result, "\n") {
		if strings.HasPrefix(line, "Conversation ID:") {
			id := strings.TrimPrefix(line, "Conversation ID:")
			return strings.TrimSpace(id)
		}
	}
	t.Fatal("could not find Conversation ID in result")
	return ""
}
