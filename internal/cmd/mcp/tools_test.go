package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/buildcaps"
	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/generated/apiclient"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setupEmbeddedTestServer(t *testing.T) *mcpServer {
	t.Helper()
	if !buildcaps.SQLite {
		t.Skip("required build capabilities missing: sqlite")
	}

	dbURL := filepath.Join(t.TempDir(), "memory.db")

	cfg := defaultEmbeddedConfig()
	cfg.DBURL = dbURL
	cfg.EncryptionKey = testEncryptionKey
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true

	ensureEmbeddedAuth(&cfg)

	ctx := config.WithContext(context.Background(), &cfg)
	runCtx, cancel := context.WithCancel(ctx)
	srv, err := serve.BuildServer(runCtx, &cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		_ = srv.Shutdown(context.Background())
	})

	// Wire the embedded MCP identity: uses CredentialEmbeddedMCP, not raw bearer.
	if r := serve.GetTokenResolver(srv); r != nil {
		r.ConfigureEmbeddedMCP(defaultEmbeddedUserID, embeddedClientID)
	}

	client, err := newInProcessClient(srv.Router, defaultEmbeddedUserID)
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

func runSessionToolContract(t *testing.T, setup func(*testing.T) *mcpServer, includeSearch bool) {
	t.Helper()

	t.Run("save and get session", func(t *testing.T) {
		s := setup(t)

		saveResult := callTool(t, s, s.handleSaveSessionNotes, map[string]any{
			"title": "Test session",
			"notes": "These are test notes about a bugfix.",
			"tags":  "test,bugfix",
		})
		assert.Contains(t, saveResult, "Session notes saved")
		assert.Contains(t, saveResult, "Conversation ID:")

		convID := extractConversationID(t, saveResult)
		getResult := callTool(t, s, s.handleGetSession, map[string]any{
			"conversation_id": convID,
		})
		assert.Contains(t, getResult, "Test session")
		assert.Contains(t, getResult, "These are test notes about a bugfix.")
		assert.Contains(t, getResult, "Tags: test,bugfix")
	})

	t.Run("append note", func(t *testing.T) {
		s := setup(t)

		saveResult := callTool(t, s, s.handleSaveSessionNotes, map[string]any{
			"title": "Append test",
			"notes": "Initial notes.",
		})
		convID := extractConversationID(t, saveResult)

		appendResult := callTool(t, s, s.handleAppendNote, map[string]any{
			"conversation_id": convID,
			"notes":           "Follow-up note with more details.",
		})
		assert.Contains(t, appendResult, "Note appended")

		getResult := callTool(t, s, s.handleGetSession, map[string]any{
			"conversation_id": convID,
		})
		assert.Contains(t, getResult, "Initial notes.")
		assert.Contains(t, getResult, "Follow-up note with more details.")
	})

	t.Run("list sessions", func(t *testing.T) {
		s := setup(t)

		callTool(t, s, s.handleSaveSessionNotes, map[string]any{
			"title": "First session",
			"notes": "First notes.",
		})
		callTool(t, s, s.handleSaveSessionNotes, map[string]any{
			"title": "Second session",
			"notes": "Second notes.",
		})

		listResult := callTool(t, s, s.handleListSessions, map[string]any{
			"limit": float64(10),
		})
		assert.Contains(t, listResult, "Recent sessions")
		assert.Contains(t, listResult, "First session")
		assert.Contains(t, listResult, "Second session")
	})

	t.Run("list sessions empty", func(t *testing.T) {
		s := setup(t)

		listResult := callTool(t, s, s.handleListSessions, map[string]any{})
		assert.Contains(t, listResult, "No sessions found")
	})

	if !includeSearch {
		return
	}

	t.Run("search sessions", func(t *testing.T) {
		if !buildcaps.SQLiteFTS5 {
			t.Skip("required build capabilities missing: sqlite_fts5")
		}

		s := setup(t)

		callTool(t, s, s.handleSaveSessionNotes, map[string]any{
			"title": "Cache serialization fix",
			"notes": "Fixed the JSON marshal symmetry issue in cache layer.",
			"tags":  "bugfix,cache",
		})

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
	})

	t.Run("search sessions no results", func(t *testing.T) {
		s := setup(t)

		searchResult := callTool(t, s, s.handleSearchSessions, map[string]any{
			"query": "nonexistent topic xyz",
		})
		assert.Contains(t, searchResult, "No matching sessions found")
	})
}

func TestSessionToolContractEmbedded(t *testing.T) {
	runSessionToolContract(t, setupEmbeddedTestServer, false)
}
