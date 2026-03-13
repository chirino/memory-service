package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func registerTools(s *mcpServer) {
	s.server.AddTool(saveSessionNotesTool(), s.handleSaveSessionNotes)
	s.server.AddTool(searchSessionsTool(), s.handleSearchSessions)
	s.server.AddTool(listSessionsTool(), s.handleListSessions)
	s.server.AddTool(getSessionTool(), s.handleGetSession)
	s.server.AddTool(appendNoteTool(), s.handleAppendNote)
}

// ── Tool definitions ───────────────────────────────────────

func saveSessionNotesTool() mcp.Tool {
	return mcp.NewTool("save_session_notes",
		mcp.WithDescription("Save notes from the current development session to the memory service. "+
			"Use this to persist decisions, discoveries, solutions, or any context that should be "+
			"available in future sessions. Creates a new conversation with the provided notes."),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Short title summarizing the session (e.g., 'Fixed cache serialization bug', 'Added fly.io deployment')"),
		),
		mcp.WithString("notes",
			mcp.Required(),
			mcp.Description("The session notes to save. Can include decisions made, problems solved, "+
				"key files changed, gotchas discovered, etc. Markdown is supported."),
		),
		mcp.WithString("tags",
			mcp.Description("Comma-separated tags for categorization (e.g., 'bugfix,cache,go')"),
		),
	)
}

func searchSessionsTool() mcp.Tool {
	return mcp.NewTool("search_sessions",
		mcp.WithDescription("Search past development sessions stored in the memory service. "+
			"Use this at the start of a session to find relevant context from previous work, "+
			"or when you need to recall how something was done before."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language search query (e.g., 'how was the cache bug fixed', 'fly.io deployment setup')"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default: 5)"),
		),
	)
}

func listSessionsTool() mcp.Tool {
	return mcp.NewTool("list_sessions",
		mcp.WithDescription("List recent development sessions stored in the memory service. "+
			"Use this to see what work has been done recently across the team."),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of sessions to return (default: 10)"),
		),
	)
}

func getSessionTool() mcp.Tool {
	return mcp.NewTool("get_session",
		mcp.WithDescription("Retrieve the full content of a specific past session by conversation ID. "+
			"Use this after list_sessions or search_sessions to get the complete notes."),
		mcp.WithString("conversation_id",
			mcp.Required(),
			mcp.Description("The conversation ID to retrieve"),
		),
	)
}

func appendNoteTool() mcp.Tool {
	return mcp.NewTool("append_note",
		mcp.WithDescription("Append additional notes to an existing session conversation. "+
			"Use this to add follow-up information to a session that was already saved."),
		mcp.WithString("conversation_id",
			mcp.Required(),
			mcp.Description("The conversation ID to append to"),
		),
		mcp.WithString("notes",
			mcp.Required(),
			mcp.Description("The additional notes to append"),
		),
	)
}

// ── Tool handlers ──────────────────────────────────────────

func (s *mcpServer) handleSaveSessionNotes(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title := request.GetArguments()["title"].(string)
	notes := request.GetArguments()["notes"].(string)
	tags, _ := request.GetArguments()["tags"].(string)

	// Add metadata to title
	sessionTitle := fmt.Sprintf("[claude-code] %s - %s", time.Now().Format("2006-01-02"), title)

	// Create conversation
	conv, err := s.client.CreateConversation(sessionTitle)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create conversation: %v", err)), nil
	}

	// Build entry content
	var content strings.Builder
	content.WriteString(notes)
	if tags != "" {
		content.WriteString("\n\n---\nTags: " + tags)
	}

	// Append the notes as an entry
	entryContent := []map[string]any{
		{
			"role": "USER",
			"text": content.String(),
		},
	}

	_, err = s.client.AppendEntry(conv.ID, "history", entryContent)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to save notes: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Session notes saved.\nConversation ID: %s\nTitle: %s", conv.ID, sessionTitle)), nil
}

func (s *mcpServer) handleSearchSessions(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := request.GetArguments()["query"].(string)
	limit := 5
	if l, ok := request.GetArguments()["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	results, err := s.client.SearchConversations(query, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}

	if len(results.Data) == 0 {
		return mcp.NewToolResultText("No matching sessions found."), nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("Found %d result(s):\n\n", len(results.Data)))
	for i, r := range results.Data {
		out.WriteString(fmt.Sprintf("### %d. %s\n", i+1, r.ConversationTitle))
		out.WriteString(fmt.Sprintf("- Conversation ID: `%s`\n", r.ConversationID))
		out.WriteString(fmt.Sprintf("- Score: %.2f\n", r.Score))
		if r.Highlights != nil && *r.Highlights != "" {
			out.WriteString(fmt.Sprintf("- Highlights: %s\n", *r.Highlights))
		}
		if r.Entry != nil {
			contentJSON, _ := json.Marshal(r.Entry.Content)
			out.WriteString(fmt.Sprintf("- Content: %s\n", truncate(string(contentJSON), 500)))
		}
		out.WriteString("\n")
	}

	return mcp.NewToolResultText(out.String()), nil
}

func (s *mcpServer) handleListSessions(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := 10
	if l, ok := request.GetArguments()["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	conversations, err := s.client.ListConversations(limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list sessions: %v", err)), nil
	}

	if len(conversations.Data) == 0 {
		return mcp.NewToolResultText("No sessions found."), nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("Recent sessions (%d):\n\n", len(conversations.Data)))
	for i, c := range conversations.Data {
		title := "(untitled)"
		if c.Title != nil && *c.Title != "" {
			title = *c.Title
		}
		preview := ""
		if c.LastMessagePreview != nil && *c.LastMessagePreview != "" {
			preview = " - " + truncate(*c.LastMessagePreview, 100)
		}
		out.WriteString(fmt.Sprintf("%d. **%s**%s\n", i+1, title, preview))
		out.WriteString(fmt.Sprintf("   ID: `%s` | Updated: %s\n\n", c.ID, c.UpdatedAt))
	}

	return mcp.NewToolResultText(out.String()), nil
}

func (s *mcpServer) handleGetSession(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	convID := request.GetArguments()["conversation_id"].(string)

	// Get conversation metadata
	conv, err := s.client.GetConversation(convID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get conversation: %v", err)), nil
	}

	// Get entries
	entries, err := s.client.ListEntries(convID, 100)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get entries: %v", err)), nil
	}

	var out strings.Builder
	title := "(untitled)"
	if conv.Title != nil && *conv.Title != "" {
		title = *conv.Title
	}
	out.WriteString(fmt.Sprintf("# %s\n\n", title))
	out.WriteString(fmt.Sprintf("Created: %s | Updated: %s\n\n", conv.CreatedAt, conv.UpdatedAt))

	if len(entries.Data) == 0 {
		out.WriteString("(no entries)")
	}
	for _, e := range entries.Data {
		contentJSON, _ := json.MarshalIndent(e.Content, "", "  ")
		out.WriteString(fmt.Sprintf("---\n**Entry** (%s)\n%s\n\n", e.CreatedAt, string(contentJSON)))
	}

	return mcp.NewToolResultText(out.String()), nil
}

func (s *mcpServer) handleAppendNote(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	convID := request.GetArguments()["conversation_id"].(string)
	notes := request.GetArguments()["notes"].(string)

	entryContent := []map[string]any{
		{
			"role": "USER",
			"text": notes,
		},
	}

	_, err := s.client.AppendEntry(convID, "history", entryContent)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to append note: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Note appended to conversation %s.", convID)), nil
}

// truncate shortens a string to max length, adding "..." if truncated.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
