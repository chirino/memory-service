package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is an HTTP client for the memory-service REST API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) do(method, path string, body any) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// API types matching the OpenAPI contract

type ConversationSummary struct {
	ID                 string  `json:"id"`
	Title              *string `json:"title"`
	OwnerUserID        string  `json:"ownerUserId"`
	CreatedAt          string  `json:"createdAt"`
	UpdatedAt          string  `json:"updatedAt"`
	LastMessagePreview *string `json:"lastMessagePreview"`
}

type Entry struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	ContentType    string `json:"contentType"`
	Content        any    `json:"content"`
	CreatedAt      string `json:"createdAt"`
}

type SearchResult struct {
	ConversationID    string  `json:"conversationId"`
	ConversationTitle string  `json:"conversationTitle"`
	EntryID           string  `json:"entryId"`
	Score             float64 `json:"score"`
	Highlights        *string `json:"highlights"`
	Entry             *Entry  `json:"entry"`
}

type ListResponse[T any] struct {
	Data        []T     `json:"data"`
	AfterCursor *string `json:"afterCursor"`
}

// CreateConversation creates a new conversation with optional title and initial entries.
func (c *Client) CreateConversation(title string) (*ConversationSummary, error) {
	body := map[string]any{}
	if title != "" {
		body["title"] = title
	}

	resp, err := c.do("POST", "/v1/conversations", body)
	if err != nil {
		return nil, err
	}

	var conv ConversationSummary
	if err := json.Unmarshal(resp, &conv); err != nil {
		return nil, fmt.Errorf("unmarshal conversation: %w", err)
	}
	return &conv, nil
}

// AppendEntry appends an entry to a conversation.
func (c *Client) AppendEntry(conversationID string, contentType string, content any) (*Entry, error) {
	body := map[string]any{
		"contentType": contentType,
		"content":     content,
	}

	resp, err := c.do("POST", "/v1/conversations/"+conversationID+"/entries", body)
	if err != nil {
		return nil, err
	}

	var entry Entry
	if err := json.Unmarshal(resp, &entry); err != nil {
		return nil, fmt.Errorf("unmarshal entry: %w", err)
	}
	return &entry, nil
}

// ListConversations lists conversations visible to the current user.
func (c *Client) ListConversations(limit int) (*ListResponse[ConversationSummary], error) {
	path := fmt.Sprintf("/v1/conversations?limit=%d", limit)

	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result ListResponse[ConversationSummary]
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("unmarshal conversations: %w", err)
	}
	return &result, nil
}

// ListEntries lists entries in a conversation.
func (c *Client) ListEntries(conversationID string, limit int) (*ListResponse[Entry], error) {
	path := fmt.Sprintf("/v1/conversations/%s/entries?limit=%d", conversationID, limit)

	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result ListResponse[Entry]
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("unmarshal entries: %w", err)
	}
	return &result, nil
}

// SearchConversations performs a search across conversations.
func (c *Client) SearchConversations(query string, limit int) (*ListResponse[SearchResult], error) {
	body := map[string]any{
		"query":        query,
		"limit":        limit,
		"includeEntry": true,
	}

	resp, err := c.do("POST", "/v1/conversations/search", body)
	if err != nil {
		return nil, err
	}

	var result ListResponse[SearchResult]
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("unmarshal search results: %w", err)
	}
	return &result, nil
}

// GetConversation gets a single conversation by ID.
func (c *Client) GetConversation(id string) (*ConversationSummary, error) {
	resp, err := c.do("GET", "/v1/conversations/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}

	var conv ConversationSummary
	if err := json.Unmarshal(resp, &conv); err != nil {
		return nil, fmt.Errorf("unmarshal conversation: %w", err)
	}
	return &conv, nil
}
