package bdd

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
	"github.com/google/uuid"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		c := &conversationSteps{s: s}
		ctx.Step(`^I have a conversation with title "([^"]*)"$`, c.iHaveAConversationWithTitle)
		ctx.Step(`^the conversation exists$`, c.theConversationExists)
		ctx.Step(`^the conversation id is "([^"]*)"$`, c.theConversationIdIs)
		ctx.Step(`^there is a conversation owned by "([^"]*)"$`, c.thereIsAConversationOwnedBy)
		ctx.Step(`^I create a conversation with request:$`, c.iCreateAConversationWithRequest)
		ctx.Step(`^I list conversations$`, c.iListConversations)
		ctx.Step(`^I list conversations with limit (\d+)$`, c.iListConversationsWithLimit)
		ctx.Step(`^I list conversations with limit (\d+) and afterCursor "([^"]*)"$`, c.iListConversationsWithLimitAndAfterCursor)
		ctx.Step(`^I list conversations with query "([^"]*)"$`, c.iListConversationsWithQuery)
		ctx.Step(`^I list conversations with mode "([^"]*)"$`, c.iListConversationsWithMode)
		ctx.Step(`^I get the conversation$`, c.iGetTheConversation)
		ctx.Step(`^I get conversation "([^"]*)"$`, c.iGetConversation)
		ctx.Step(`^I get that conversation$`, c.iGetTheConversation)
		ctx.Step(`^I delete the conversation$`, c.iDeleteTheConversation)
		ctx.Step(`^I delete conversation "([^"]*)"$`, c.iDeleteConversation)
		ctx.Step(`^I delete that conversation$`, c.iDeleteTheConversation)
		ctx.Step(`^I update the conversation with request:$`, c.iUpdateTheConversationWithRequest)
		ctx.Step(`^I fork the conversation at entry "([^"]*)"$`, c.iForkTheConversationAtEntry)
		ctx.Step(`^I fork the conversation at entry "([^"]*)" with request:$`, c.iForkTheConversationAtEntryWithRequest)
		ctx.Step(`^I fork conversation "([^"]*)" at entry "([^"]*)" with request:$`, c.iForkConversationAtEntryWithRequest)
		ctx.Step(`^I fork that conversation at entry "([^"]*)" with request:$`, c.iForkTheConversationAtEntryWithRequest)
		ctx.Step(`^I list forks for the conversation$`, c.iListForksForTheConversation)
		ctx.Step(`^I list forks for conversation "([^"]*)"$`, c.iListForksForConversation)
		ctx.Step(`^I list forks for that conversation$`, c.iListForksForTheConversation)
		ctx.Step(`^I resolve the conversation group ID for conversation "([^"]*)" into "([^"]*)"$`, c.iResolveConversationGroupID)
	})
}

type conversationSteps struct {
	s *cucumber.TestScenario
}

func (c *conversationSteps) iHaveAConversationWithTitle(title string) error {
	body := fmt.Sprintf(`{"title": %q}`, title)
	err := c.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations", &godog.DocString{Content: body}, false, false)
	if err != nil {
		return err
	}
	session := c.s.Session()
	if session.Resp != nil && session.Resp.StatusCode == 201 {
		respJSON, err := session.RespJSON()
		if err != nil {
			return err
		}
		if m, ok := respJSON.(map[string]interface{}); ok {
			if id, ok := m["id"].(string); ok {
				c.s.Variables["conversationId"] = id
				// Resolve conversationGroupId from DB for tests that need it.
				c.resolveGroupID(id)
			}
		}
	}
	return nil
}

func (c *conversationSteps) resolveGroupID(convID string) {
	if c.s.Suite.DB == nil {
		return
	}
	groupID, err := c.s.Suite.DB.ResolveGroupID(context.Background(), convID)
	if err == nil {
		c.s.Variables["conversationGroupId"] = groupID
	}
}

func (c *conversationSteps) theConversationExists() error {
	if _, ok := c.s.Variables["conversationId"]; !ok {
		return c.iHaveAConversationWithTitle("Test Conversation")
	}
	return nil
}

func (c *conversationSteps) theConversationIdIs(id string) error {
	c.s.Variables["conversationId"] = id
	return nil
}

func (c *conversationSteps) thereIsAConversationOwnedBy(ownerId string) error {
	// Temporarily switch user to create conversation as owner
	savedUser := c.s.CurrentUser
	a := &authSteps{s: c.s}
	_ = a.iAmAuthenticatedAsUser(ownerId)

	body := fmt.Sprintf(`{"title": "Owned by %s"}`, ownerId)
	err := c.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations", &godog.DocString{Content: body}, false, false)
	if err != nil {
		c.s.CurrentUser = savedUser
		return err
	}
	session := c.s.Session()
	if session.Resp != nil && session.Resp.StatusCode == 201 {
		respJSON, err := session.RespJSON()
		if err != nil {
			c.s.CurrentUser = savedUser
			return err
		}
		if m, ok := respJSON.(map[string]interface{}); ok {
			if id, ok := m["id"].(string); ok {
				c.s.Variables["conversationId"] = id
				c.s.Variables["conversationOwner"] = ownerId
			}
		}
	}
	// Restore original user
	c.s.CurrentUser = savedUser
	return nil
}

func (c *conversationSteps) iCreateAConversationWithRequest(body *godog.DocString) error {
	err := c.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations", body, false, true)
	if err != nil {
		return err
	}
	session := c.s.Session()
	if session.Resp != nil && session.Resp.StatusCode == 201 {
		respJSON, err := session.RespJSON()
		if err != nil {
			return nil // Non-fatal: conversation was created but couldn't parse response
		}
		if m, ok := respJSON.(map[string]interface{}); ok {
			if id, ok := m["id"].(string); ok {
				c.s.Variables["conversationId"] = id
				c.s.Variables["conversationOwner"] = c.s.CurrentUser
			}
		}
	}
	return nil
}

func (c *conversationSteps) iListConversations() error {
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations", nil, false, true)
}

func (c *conversationSteps) iListConversationsWithLimit(limit int) error {
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", fmt.Sprintf("/v1/conversations?limit=%d", limit), nil, false, true)
}

func (c *conversationSteps) iListConversationsWithLimitAndAfterCursor(limit int, afterCursor string) error {
	expanded, err := c.s.Expand(afterCursor)
	if err != nil {
		return err
	}
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", fmt.Sprintf("/v1/conversations?limit=%d&afterCursor=%s", limit, expanded), nil, false, true)
}

func (c *conversationSteps) iListConversationsWithQuery(query string) error {
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", fmt.Sprintf("/v1/conversations?query=%s", query), nil, false, true)
}

func (c *conversationSteps) iListConversationsWithMode(mode string) error {
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", fmt.Sprintf("/v1/conversations?mode=%s", mode), nil, false, true)
}

func (c *conversationSteps) iGetTheConversation() error {
	convID, err := c.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations/"+convID, nil, false, true)
}

func (c *conversationSteps) iGetConversation(convID string) error {
	expanded, err := c.s.Expand(convID)
	if err != nil {
		return err
	}
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations/"+expanded, nil, false, true)
}

func (c *conversationSteps) iDeleteTheConversation() error {
	convID, err := c.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("DELETE", "/v1/conversations/"+convID, nil, false, true)
}

func (c *conversationSteps) iDeleteConversation(convID string) error {
	expanded, err := c.s.Expand(convID)
	if err != nil {
		return err
	}
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("DELETE", "/v1/conversations/"+expanded, nil, false, true)
}

func (c *conversationSteps) iUpdateTheConversationWithRequest(body *godog.DocString) error {
	convID, err := c.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("PATCH", "/v1/conversations/"+convID, body, false, true)
}

func (c *conversationSteps) iForkTheConversationAtEntry(entryID string) error {
	return c.iForkTheConversationAtEntryWithRequest(entryID, &godog.DocString{Content: "{}"})
}

func (c *conversationSteps) iForkTheConversationAtEntryWithRequest(entryID string, _ *godog.DocString) error {
	convID, err := c.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	expandedEntryID, err := c.s.Expand(entryID)
	if err != nil {
		return err
	}

	// Java parity: fork via POST /v1/conversations/{newId}/entries with fork metadata
	// in the entry body. The server auto-creates the fork conversation.
	newConvID := uuid.New().String()

	entryBody := fmt.Sprintf(`{
		"forkedAtConversationId": %q,
		"forkedAtEntryId": %q,
		"channel": "HISTORY",
		"contentType": "history",
		"content": [{"role": "USER", "text": "Fork message"}]
	}`, convID, expandedEntryID)

	err = c.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+newConvID+"/entries", &godog.DocString{Content: entryBody}, false, false)
	if err != nil {
		return fmt.Errorf("fork-on-append failed: %w", err)
	}

	session := c.s.Session()
	if session.Resp == nil {
		return nil
	}

	if session.Resp.StatusCode != 201 {
		// Non-201: expose the response for the feature file to assert status/error codes.
		return nil
	}

	c.s.Variables["forkedConversationId"] = newConvID

	// GET the fork conversation so lastResponse matches Java's behavior
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations/"+newConvID, nil, false, true)
}

func (c *conversationSteps) iForkConversationAtEntryWithRequest(convID, entryID string, body *godog.DocString) error {
	expanded, err := c.s.Expand(convID)
	if err != nil {
		return err
	}
	savedConvID := c.s.Variables["conversationId"]
	c.s.Variables["conversationId"] = expanded
	err = c.iForkTheConversationAtEntryWithRequest(entryID, body)
	c.s.Variables["conversationId"] = savedConvID
	return err
}

func (c *conversationSteps) iListForksForTheConversation() error {
	convID, err := c.s.ResolveString("conversationId")
	if err != nil {
		return err
	}
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations/"+convID+"/forks", nil, false, true)
}

func (c *conversationSteps) iListForksForConversation(convID string) error {
	expanded, err := c.s.Expand(convID)
	if err != nil {
		return err
	}
	return c.s.SendHTTPRequestWithJSONBodyAndStyle("GET", "/v1/conversations/"+expanded+"/forks", nil, false, true)
}

func (c *conversationSteps) iResolveConversationGroupID(convID, varName string) error {
	expanded, err := c.s.Expand(convID)
	if err != nil {
		return err
	}

	if c.s.Suite.DB == nil {
		return fmt.Errorf("no TestDB configured for resolving conversation group ID")
	}

	groupID, err := c.s.Suite.DB.ResolveGroupID(context.Background(), expanded)
	if err != nil {
		return err
	}
	c.s.Variables[varName] = groupID
	return nil
}
