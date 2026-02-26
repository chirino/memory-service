package bdd

import (
	"fmt"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		e := &entrySteps{s: s}
		// Given steps - conversation entry setup
		ctx.Step(`^the conversation has no entries$`, e.theConversationHasNoEntries)
		ctx.Step(`^the conversation has an entry "([^"]*)"$`, e.theConversationHasAnEntry)
		ctx.Step(`^the conversation has (\d+) entries$`, e.theConversationHasEntries)
		ctx.Step(`^the conversation has an entry "([^"]*)" in channel "([^"]*)"$`, e.theConversationHasAnEntryInChannel)
		ctx.Step(`^the conversation has an entry "([^"]*)" in channel "([^"]*)" with contentType "([^"]*)"$`, e.theConversationHasAnEntryInChannelWithContentType)
		ctx.Step(`^the conversation has a memory entry "([^"]*)" with epoch (\d+) and contentType "([^"]*)"$`, e.theConversationHasAMemoryEntryWithEpochAndContentType)

		// When steps - entry operations
		ctx.Step(`^I list entries for the conversation$`, e.iListEntriesForTheConversation)
		ctx.Step(`^I list entries with limit (\d+)$`, e.iListEntriesWithLimit)
		ctx.Step(`^I list entries for the conversation with channel "([^"]*)"$`, e.iListEntriesForTheConversationWithChannel)
		ctx.Step(`^I list memory entries for the conversation with epoch "([^"]*)"$`, e.iListMemoryEntriesWithEpoch)
		ctx.Step(`^I list entries for conversation "([^"]*)"$`, e.iListEntriesForConversation)
		ctx.Step(`^I list entries for that conversation$`, e.iListEntriesForTheConversation)
		ctx.Step(`^I append an entry with content "([^"]*)" and channel "([^"]*)" and contentType "([^"]*)"$`, e.iAppendAnEntry)
		ctx.Step(`^I append an entry to the conversation:$`, e.iAppendAnEntryToTheConversation)
		ctx.Step(`^I sync memory entries with request:$`, e.iSyncMemoryEntries)
		ctx.Step(`^I create a summary with request:$`, e.iCreateASummary)
		ctx.Step(`^I index a transcript with request:$`, e.iIndexATranscript)
		ctx.Step(`^I search entries with request:$`, e.iSearchEntries)
		ctx.Step(`^I search conversations with request:$`, e.iSearchConversations)
		ctx.Step(`^I search entries for query "([^"]*)"$`, e.iSearchEntriesForQuery)
		ctx.Step(`^I search conversations for query "([^"]*)"$`, e.iSearchConversationsForQuery)
	})
}

type entrySteps struct {
	s *cucumber.TestScenario
}

func (e *entrySteps) theConversationHasNoEntries() error {
	// No action needed - conversation already exists from background with no entries
	return nil
}

func (e *entrySteps) theConversationHasAnEntry(content string) error {
	return e.theConversationHasAnEntryInChannelWithContentType(content, "HISTORY", "history")
}

func (e *entrySteps) theConversationHasEntries(count int) error {
	for i := 1; i <= count; i++ {
		if err := e.theConversationHasAnEntry(fmt.Sprintf("Entry %d", i)); err != nil {
			return err
		}
	}
	return nil
}

func (e *entrySteps) theConversationHasAnEntryInChannel(content, channel string) error {
	return e.theConversationHasAnEntryInChannelWithContentType(content, channel, "test.v1")
}

func (e *entrySteps) theConversationHasAnEntryInChannelWithContentType(content, channel, contentType string) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}

	var body string
	if channel == "HISTORY" || channel == "history" {
		body = fmt.Sprintf(`{
			"channel": "HISTORY",
			"contentType": "history",
			"content": [{"role": "USER", "text": %q}]
		}`, content)
	} else {
		body = fmt.Sprintf(`{
			"channel": %q,
			"contentType": %q,
			"content": [{"type": "text", "text": %q}]
		}`, channel, contentType, content)
		if channel == "MEMORY" || channel == "memory" {
			body = fmt.Sprintf(`{
				"channel": %q,
				"contentType": %q,
				"epoch": 1,
				"content": [{"type": "text", "text": %q}]
			}`, channel, contentType, content)
		}
	}

	// Use agent auth to append entries (agents can append to any channel).
	// Save and restore the full auth state so we don't clobber explicit agent auth.
	savedUser := e.s.CurrentUser
	savedClientID := e.s.Session().Header.Get("X-Client-ID")
	a := &authSteps{s: e.s}
	_ = a.iAmAuthenticatedAsAgentWithAPIKey("test-agent-key")

	err = e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/entries", &godog.DocString{Content: body}, false, false)

	// Restore user and X-Client-ID header to previous state.
	e.s.CurrentUser = savedUser
	if savedClientID != "" {
		e.s.Session().Header.Set("X-Client-ID", savedClientID)
	} else {
		e.s.Session().Header.Del("X-Client-ID")
	}

	if err != nil {
		return err
	}

	// Small delay to ensure entries have distinct timestamps for deterministic ordering
	time.Sleep(2 * time.Millisecond)
	return nil
}

func (e *entrySteps) theConversationHasAMemoryEntryWithEpochAndContentType(content string, epoch int, contentType string) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}

	body := fmt.Sprintf(`{
		"channel": "MEMORY",
		"contentType": %q,
		"epoch": %d,
		"content": [{"type": "text", "text": %q}]
	}`, contentType, epoch, content)

	// If already authenticated as an agent (has X-Client-ID), use current auth.
	// Otherwise, temporarily switch to agent auth for memory entry creation.
	currentClientID := e.s.Session().Header.Get("X-Client-ID")
	needsAgentSwitch := currentClientID == ""

	var savedUser string
	if needsAgentSwitch {
		savedUser = e.s.CurrentUser
		a := &authSteps{s: e.s}
		_ = a.iAmAuthenticatedAsAgentWithAPIKey("test-agent-key")
	}

	err = e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/entries", &godog.DocString{Content: body}, false, false)

	if needsAgentSwitch {
		e.s.CurrentUser = savedUser
		e.s.Session().Header.Del("X-Client-ID")
	}

	if err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	return nil
}

func (e *entrySteps) iListEntriesForTheConversation() error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations/"+convID+"/entries", nil, false, true)
}

func (e *entrySteps) iListEntriesWithLimit(limit int) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("GET", fmt.Sprintf("/v1/conversations/%s/entries?limit=%d", convID, limit), nil, false, true)
}

func (e *entrySteps) iListEntriesForTheConversationWithChannel(channel string) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("GET", fmt.Sprintf("/v1/conversations/%s/entries?channel=%s", convID, channel), nil, false, true)
}

func (e *entrySteps) iListMemoryEntriesWithEpoch(epoch string) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("GET", fmt.Sprintf("/v1/conversations/%s/entries?channel=MEMORY&epoch=%s", convID, epoch), nil, false, true)
}

func (e *entrySteps) iListEntriesForConversation(convID string) error {
	expanded, err := e.s.Expand(convID)
	if err != nil {
		return err
	}
	e.s.Variables["conversationId"] = expanded
	return e.iListEntriesForTheConversation()
}

func (e *entrySteps) iAppendAnEntry(content, channel, contentType string) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}

	var body string
	if channel == "MEMORY" || channel == "memory" {
		body = fmt.Sprintf(`{
			"channel": %q,
			"contentType": %q,
			"epoch": 1,
			"content": [{"type": "text", "text": %q}]
		}`, channel, contentType, content)
	} else {
		body = fmt.Sprintf(`{
			"channel": %q,
			"contentType": %q,
			"content": [{"type": "text", "text": %q}]
		}`, channel, contentType, content)
	}

	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/entries", &godog.DocString{Content: body}, false, false)
}

func (e *entrySteps) iAppendAnEntryToTheConversation(body *godog.DocString) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/entries", body, false, true)
}

func (e *entrySteps) iSyncMemoryEntries(body *godog.DocString) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/entries/sync", body, false, true)
}

func (e *entrySteps) iCreateASummary(body *godog.DocString) error {
	convID, err := e.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/summaries", body, false, true)
}

func (e *entrySteps) iIndexATranscript(body *godog.DocString) error {
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/index", body, false, true)
}

func (e *entrySteps) iSearchEntries(body *godog.DocString) error {
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/user/search/entries", body, false, true)
}

func (e *entrySteps) iSearchConversations(body *godog.DocString) error {
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/search", body, false, true)
}

func (e *entrySteps) iSearchEntriesForQuery(query string) error {
	body := fmt.Sprintf(`{"query": %q}`, query)
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/user/search/entries", &godog.DocString{Content: body}, false, false)
}

func (e *entrySteps) iSearchConversationsForQuery(query string) error {
	body := fmt.Sprintf(`{"query": %q}`, query)
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/search", &godog.DocString{Content: body}, false, false)
}
