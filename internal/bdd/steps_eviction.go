package bdd

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		e := &evictionSteps{s: s}

		ctx.Step(`^the conversation was soft-deleted (\d+) days ago$`, e.theConversationWasSoftDeletedDaysAgo)
		ctx.Step(`^I have (\d+) conversations soft-deleted (\d+) days ago$`, e.iHaveConversationsSoftDeletedDaysAgo)
		ctx.Step(`^the conversation has entries$`, e.theConversationHasEntries)
		ctx.Step(`^the conversation is shared with user "([^"]*)"$`, e.theConversationIsSharedWithUser)
		ctx.Step(`^the conversation has a pending ownership transfer to user "([^"]*)"$`, e.theConversationHasAPendingOwnershipTransferToUser)
		ctx.Step(`^the response content type should be "([^"]*)"$`, e.theResponseContentTypeShouldBe)
		ctx.Step(`^the SSE stream should contain progress events$`, e.theSSEStreamShouldContainProgressEvents)
		ctx.Step(`^the final progress should be (\d+)$`, e.theFinalProgressShouldBe)
		ctx.Step(`^I call POST "([^"]*)" concurrently (\d+) times with body:$`, e.iCallPOSTConcurrentlyTimesWithBody)
		ctx.Step(`^all responses should have status (\d+)$`, e.allResponsesShouldHaveStatus)
	})
}

type evictionSteps struct {
	s              *cucumber.TestScenario
	sseEvents      []string
	finalProgress  int
	concurrentResp []int // status codes from concurrent requests
}

func (e *evictionSteps) theConversationWasSoftDeletedDaysAgo(days int) error {
	convID := fmt.Sprintf("%v", e.s.Variables["conversationId"])
	return e.softDeleteConversationDaysAgo(convID, days)
}

func (e *evictionSteps) softDeleteConversationDaysAgo(convID string, days int) error {
	if e.s.Suite.DB == nil {
		return fmt.Errorf("no TestDB configured")
	}
	return e.s.Suite.DB.SoftDeleteConversation(context.Background(), convID, days)
}

func (e *evictionSteps) iHaveConversationsSoftDeletedDaysAgo(count, days int) error {
	for i := 0; i < count; i++ {
		// Create a conversation
		body := fmt.Sprintf(`{"title": "Bulk conversation %d"}`, i)
		err := e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations", &godog.DocString{Content: body}, false, false)
		if err != nil {
			return err
		}
		session := e.s.Session()
		if session.Resp == nil || session.Resp.StatusCode != 201 {
			return fmt.Errorf("failed to create conversation %d: status %d", i, session.Resp.StatusCode)
		}
		respJSON, err := session.RespJSON()
		if err != nil {
			return err
		}
		m, ok := respJSON.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected response format")
		}
		id, ok := m["id"].(string)
		if !ok {
			return fmt.Errorf("no id in response")
		}
		if err := e.softDeleteConversationDaysAgo(id, days); err != nil {
			return err
		}
	}
	return nil
}

func (e *evictionSteps) theConversationHasEntries() error {
	convID := fmt.Sprintf("%v", e.s.Variables["conversationId"])
	// Add a couple of entries
	for i := 0; i < 3; i++ {
		body := fmt.Sprintf(`{
			"channel": "HISTORY",
			"contentType": "history",
			"content": [{"role": "USER", "text": "Entry %d"}]
		}`, i)
		savedUser := e.s.CurrentUser
		savedClientID := e.s.Session().Header.Get("X-Client-ID")
		a := &authSteps{s: e.s}
		_ = a.iAmAuthenticatedAsAgentWithAPIKey("test-agent-key")

		err := e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/entries", &godog.DocString{Content: body}, false, false)
		e.s.CurrentUser = savedUser
		if savedClientID != "" {
			e.s.Session().Header.Set("X-Client-ID", savedClientID)
		} else {
			e.s.Session().Header.Del("X-Client-ID")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *evictionSteps) theConversationIsSharedWithUser(user string) error {
	convID := fmt.Sprintf("%v", e.s.Variables["conversationId"])
	body := fmt.Sprintf(`{"userId": %q, "accessLevel": "reader"}`, user)
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/memberships", &godog.DocString{Content: body}, false, false)
}

func (e *evictionSteps) theConversationHasAPendingOwnershipTransferToUser(user string) error {
	convID := fmt.Sprintf("%v", e.s.Variables["conversationId"])
	body := fmt.Sprintf(`{"newOwnerUserId": %q}`, user)
	return e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/transfers", &godog.DocString{Content: body}, false, false)
}

func (e *evictionSteps) theResponseContentTypeShouldBe(expected string) error {
	session := e.s.Session()
	if session.Resp == nil {
		return fmt.Errorf("no response")
	}
	ct := session.Resp.Header.Get("Content-Type")
	if !strings.Contains(ct, expected) {
		return fmt.Errorf("expected content type %q, got %q", expected, ct)
	}
	return nil
}

func (e *evictionSteps) theSSEStreamShouldContainProgressEvents() error {
	session := e.s.Session()
	body := string(session.RespBytes)
	e.sseEvents = nil
	e.finalProgress = 0

	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			e.sseEvents = append(e.sseEvents, data)
			// Parse progress percentage
			if strings.Contains(data, "progress") {
				// Try to extract the number from JSON like {"progress":100,"evicted":1}
				for _, part := range strings.Split(data, ",") {
					part = strings.TrimSpace(part)
					if strings.Contains(part, `"progress"`) {
						parts := strings.SplitN(part, ":", 2)
						if len(parts) == 2 {
							numStr := strings.Trim(strings.TrimSpace(parts[1]), "} ")
							if n, err := strconv.Atoi(numStr); err == nil {
								e.finalProgress = n
							}
						}
					}
				}
			}
		}
	}

	if len(e.sseEvents) == 0 {
		return fmt.Errorf("SSE stream contained no data events. Body: %s", body)
	}
	return nil
}

func (e *evictionSteps) theFinalProgressShouldBe(expected int) error {
	if e.finalProgress != expected {
		return fmt.Errorf("expected final progress %d, got %d", expected, e.finalProgress)
	}
	return nil
}

func (e *evictionSteps) iCallPOSTConcurrentlyTimesWithBody(path string, count int, jsonTxt *godog.DocString) error {
	expanded, err := e.s.Expand(path)
	if err != nil {
		return err
	}
	expandedBody, err := e.s.Expand(jsonTxt.Content)
	if err != nil {
		return err
	}

	apiURL := e.s.Suite.APIURL
	var wg sync.WaitGroup
	e.concurrentResp = make([]int, count)

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req, err := http.NewRequest("POST", apiURL+expanded, strings.NewReader(expandedBody))
			if err != nil {
				e.concurrentResp[idx] = 0
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+e.s.CurrentUser)

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				e.concurrentResp[idx] = 0
				return
			}
			defer resp.Body.Close()
			e.concurrentResp[idx] = resp.StatusCode
		}(i)
	}
	wg.Wait()
	return nil
}

func (e *evictionSteps) allResponsesShouldHaveStatus(expected int) error {
	for i, status := range e.concurrentResp {
		if status != expected {
			return fmt.Errorf("concurrent request %d had status %d, expected %d", i, status, expected)
		}
	}
	return nil
}
