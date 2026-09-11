package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// createConversation posts a new conversation to the memory service and returns
// the assigned conversation ID.
func createConversation(client *http.Client, cfg GeneratorConfig, title, ownerUserID string) (string, error) {
	body, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return "", err
	}

	for attempt := 0; attempt < 20; attempt++ {
		req, err := http.NewRequest(http.MethodPost, cfg.BaseURL+"/v1/conversations", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", cfg.APIKey)
		req.Header.Set("X-User-ID", ownerUserID)

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
			if backoff > time.Second {
				backoff = time.Second
			}
			time.Sleep(backoff)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("createConversation: status %d: %s", resp.StatusCode, respBody)
		}

		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", fmt.Errorf("createConversation: unmarshal: %w", err)
		}
		return result.ID, nil
	}
	return "", fmt.Errorf("createConversation: rate limited after retries")
}

// appendEntry appends a single content entry to a conversation and returns the
// new entry ID. userID is sent as the X-User-ID header. role must be "USER" or
// "AI". For fork seeding, forkedAtConversationId and forkedAtEntryId may be
// non-empty to make this append implicitly create a fork.
func appendEntry(
	client *http.Client,
	cfg GeneratorConfig,
	convID, authUserID, entryUserID, role, text string,
	forkedAtConversationId, forkedAtEntryId string,
) (string, error) {
	payload := map[string]any{
		"contentType": "history",
		"content":     []map[string]string{{"role": role, "text": text}},
		"userId":      entryUserID,
	}
	if forkedAtConversationId != "" {
		payload["forkedAtConversationId"] = forkedAtConversationId
	}
	if forkedAtEntryId != "" {
		payload["forkedAtEntryId"] = forkedAtEntryId
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/v1/conversations/%s/entries", cfg.BaseURL, convID)

	for attempt := 0; attempt < 20; attempt++ {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", cfg.APIKey)
		req.Header.Set("X-User-ID", authUserID)

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
			if backoff > time.Second {
				backoff = time.Second
			}
			time.Sleep(backoff)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("appendEntry conv=%s: status %d: %s", convID, resp.StatusCode, respBody)
		}

		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", fmt.Errorf("appendEntry: unmarshal: %w", err)
		}
		return result.ID, nil
	}
	return "", fmt.Errorf("appendEntry: rate limited after retries")
}

// indexEntries calls POST /v1/conversations/index to make all seeded entries
// searchable via fulltext and semantic search. The agent API key has indexer
// role (MEMORY_SERVICE_ROLES_INDEXER_CLIENTS=agent) so no separate admin key
// is required.
//
// indexedContent for each entry is the entry's text content — the same words
// used in the entry body, which guarantees "load test" search terms will match.
func indexEntries(client *http.Client, cfg GeneratorConfig, entries []indexEntryRequest, batchSize int) error {
	if len(entries) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	total := len(entries)
	for i := 0; i < total; i += batchSize {
		end := i + batchSize
		if end > total {
			end = total
		}
		batch := entries[i:end]

		body, err := json.Marshal(batch)
		if err != nil {
			return err
		}

		for attempt := 0; attempt < 20; attempt++ {
			req, err := http.NewRequest(http.MethodPost, cfg.BaseURL+"/v1/conversations/index", bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", cfg.APIKey)
			req.Header.Set("X-User-ID", "loadtest-user-1") // indexer-role user

			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode == http.StatusTooManyRequests {
				backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
				if backoff > time.Second {
					backoff = time.Second
				}
				time.Sleep(backoff)
				continue
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return fmt.Errorf("indexEntries: status %d: %s", resp.StatusCode, respBody)
			}
			break
		}

		// Pace index batches to give Postgres WAL time to flush between writes.
		// Without this, large seeds (2000+ conversations with 5000-char entries)
		// cause a WAL write spike that crashes the local Postgres instance.
		// 50ms between batches = ~20 batches/sec max throughput for indexing.
		if i+batchSize < total {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return nil
}

// indexEntryRequest is a single entry to submit to POST /v1/conversations/index.
type indexEntryRequest struct {
	ConversationID string `json:"conversationId"`
	EntryID        string `json:"entryId"`
	IndexedContent string `json:"indexedContent"`
}

// shareConversation grants accessLevel to granteeUserID on convID.
// The request is authenticated as ownerUserID (who must have manager/owner access).
func shareConversation(client *http.Client, cfg GeneratorConfig, ownerUserID, convID, granteeUserID, accessLevel string) error {
	body, err := json.Marshal(map[string]string{
		"userId":      granteeUserID,
		"accessLevel": accessLevel,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v1/conversations/%s/memberships", cfg.BaseURL, convID)

	for attempt := 0; attempt < 20; attempt++ {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", cfg.APIKey)
		req.Header.Set("X-User-ID", ownerUserID)

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
			if backoff > time.Second {
				backoff = time.Second
			}
			time.Sleep(backoff)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("shareConversation: status %d: %s", resp.StatusCode, respBody)
		}
		return nil
	}
	return fmt.Errorf("shareConversation: rate limited after retries")
}

// createFork creates a new conversation and seeds it as a fork of rootConvID
// branching at atEntryID. Returns the new fork conversation ID.
//
// IMPORTANT: do NOT call createConversation first.  The fork ancestry closure
// is only registered by AppendEntries when the conversation does not yet exist
// (RowsAffected == 0 path in the store).  Creating the conversation shell first
// makes RowsAffected == 1, which silently skips the forkedAtConversationId/
// forkedAtEntryId fields, leaving the fork unregistered in the ancestry table.
// Instead, generate the UUID here and let appendEntry trigger the implicit
// conversation auto-create with fork fields in a single store call.
func createFork(client *http.Client, cfg GeneratorConfig, rootConvID, atEntryID string) (string, error) {
	forkID := uuid.New().String()

	// The first entry append with forkedAt fields implicitly creates the
	// conversation AND registers the fork in the ancestry closure table.
	_, err := appendEntry(client, cfg, forkID, "loadtest-user-1", "loadtest-user-1", "USER",
		"fork branch entry", rootConvID, atEntryID)
	if err != nil {
		return "", fmt.Errorf("createFork: append first entry: %w", err)
	}

	return forkID, nil
}
