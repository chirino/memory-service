package bdd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

		ctx.Step(`^the conversation was archived (\d+) days ago$`, e.theConversationWasArchivedDaysAgo)
		ctx.Step(`^I have (\d+) conversations archived (\d+) days ago$`, e.iHaveConversationsArchivedDaysAgo)
		ctx.Step(`^the conversation has entries$`, e.theConversationHasEntries)
		ctx.Step(`^the conversation group of "([^"]*)" was last active (\d+) days ago$`, e.theConversationGroupWasLastActiveDaysAgo)
		ctx.Step(`^the conversation "([^"]*)" was archived (\d+) days ago$`, e.theNamedConversationWasArchivedDaysAgo)
		ctx.Step(`^the conversation is shared with user "([^"]*)"$`, e.theConversationIsSharedWithUser)
		ctx.Step(`^the conversation has a pending ownership transfer to user "([^"]*)"$`, e.theConversationHasAPendingOwnershipTransferToUser)
		ctx.Step(`^the response content type should be "([^"]*)"$`, e.theResponseContentTypeShouldBe)
		ctx.Step(`^the SSE stream should contain progress events$`, e.theSSEStreamShouldContainProgressEvents)
		ctx.Step(`^the final progress should be (\d+)$`, e.theFinalProgressShouldBe)
		ctx.Step(`^I call (POST|PUT) "([^"]*)" concurrently (\d+) times with body:$`, e.iCallMethodConcurrentlyTimesWithBody)
		ctx.Step(`^every concurrent response body field "([^"]*)" should be "([^"]*)"$`, e.everyConcurrentResponseBodyFieldShouldBe)
		ctx.Step(`^all responses should have status (\d+)$`, e.allResponsesShouldHaveStatus)
		ctx.Step(`^exactly one response should have status (\d+) and the rest should have status (\d+)$`, e.exactlyOneResponseShouldHaveStatusAndTheRestShouldHaveStatus)
	})
}

type evictionSteps struct {
	s                *cucumber.TestScenario
	sseEvents        []string
	finalProgress    int
	concurrentResp   []int    // status codes from concurrent requests
	concurrentBodies []string // response bodies from concurrent requests, index-aligned with concurrentResp
}

func (e *evictionSteps) theConversationWasArchivedDaysAgo(days int) error {
	convID := fmt.Sprintf("%v", e.s.Variables["conversationId"])
	return e.archiveConversationDaysAgo(convID, days)
}

func (e *evictionSteps) theConversationGroupWasLastActiveDaysAgo(convID string, days int) error {
	expanded, err := e.s.Expand(convID)
	if err != nil {
		return err
	}
	if e.s.TestDB() == nil {
		return fmt.Errorf("no TestDB configured")
	}
	return e.s.TestDB().SetConversationGroupActivity(context.Background(), expanded, days)
}

func (e *evictionSteps) theNamedConversationWasArchivedDaysAgo(convID string, days int) error {
	expanded, err := e.s.Expand(convID)
	if err != nil {
		return err
	}
	return e.archiveConversationDaysAgo(expanded, days)
}

func (e *evictionSteps) archiveConversationDaysAgo(convID string, days int) error {
	if e.s.TestDB() == nil {
		return fmt.Errorf("no TestDB configured")
	}
	return e.s.TestDB().ArchiveConversation(context.Background(), convID, days)
}

func (e *evictionSteps) iHaveConversationsArchivedDaysAgo(count, days int) error {
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
		if err := e.archiveConversationDaysAgo(id, days); err != nil {
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
		savedAuth := snapshotAuthState(e.s)
		a := &authSteps{s: e.s}
		_ = a.iAmAuthenticatedAsAgentWithAPIKey("test-agent-key")

		err := e.s.SendHTTPRequestWithJSONBodyAndStyle("POST", "/v1/conversations/"+convID+"/entries", &godog.DocString{Content: body}, false, false)
		restoreAuthState(e.s, savedAuth)
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
	return e.iCallMethodConcurrentlyTimesWithBody("POST", path, count, jsonTxt)
}

func (e *evictionSteps) iCallMethodConcurrentlyTimesWithBody(method, path string, count int, jsonTxt *godog.DocString) error {
	expanded, err := e.s.Expand(path)
	if err != nil {
		return err
	}
	expandedBody, err := e.s.Expand(jsonTxt.Content)
	if err != nil {
		return err
	}
	expandedBody = e.s.RewriteQuotedUsers(expandedBody)

	apiURL := e.s.APIBaseURL()
	session := e.s.Session()

	// Clone all session headers so every concurrent request shares scenario identity
	baseHeaders := make(http.Header)
	for k, v := range session.Header {
		baseHeaders[k] = append([]string(nil), v...)
	}
	if baseHeaders.Get("Authorization") == "" && session.TestUser != nil && session.TestUser.Subject != "" {
		baseHeaders.Set("Authorization", "Bearer "+session.TestUser.Subject)
	}
	if baseHeaders.Get("Content-Type") == "" {
		baseHeaders.Set("Content-Type", "application/json")
	}

	var wg sync.WaitGroup
	e.concurrentResp = make([]int, count)
	e.concurrentBodies = make([]string, count)

	// start is closed once every goroutine is ready, so the requests are released together
	// instead of trickling out as goroutines are scheduled.
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(count)

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ready.Done()
			<-start
			req, err := http.NewRequest(method, apiURL+expanded, strings.NewReader(expandedBody))
			if err != nil {
				e.concurrentResp[idx] = 0
				return
			}
			req.Header = make(http.Header)
			for k, v := range baseHeaders {
				req.Header[k] = append([]string(nil), v...)
			}

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				e.concurrentResp[idx] = 0
				return
			}
			defer resp.Body.Close()
			e.concurrentResp[idx] = resp.StatusCode
			if body, readErr := io.ReadAll(resp.Body); readErr == nil {
				e.concurrentBodies[idx] = string(body)
			}
		}(i)
	}
	ready.Wait()
	close(start)
	wg.Wait()
	return nil
}

// everyConcurrentResponseBodyFieldShouldBe asserts a top-level JSON string field on every
// captured concurrent response body.
func (e *evictionSteps) everyConcurrentResponseBodyFieldShouldBe(field, expected string) error {
	if len(e.concurrentBodies) == 0 {
		return fmt.Errorf("no concurrent responses were captured")
	}
	for i, body := range e.concurrentBodies {
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			return fmt.Errorf("concurrent response %d is not a JSON object: %w (body: %s)", i, err, body)
		}
		got, ok := decoded[field].(string)
		if !ok || got != expected {
			return fmt.Errorf("concurrent response %d field %q = %v, expected %q (body: %s)", i, field, decoded[field], expected, body)
		}
	}
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

func (e *evictionSteps) exactlyOneResponseShouldHaveStatusAndTheRestShouldHaveStatus(firstStatus int, restStatus int) error {
	firstCount := 0
	for i, status := range e.concurrentResp {
		if status == firstStatus {
			firstCount++
		} else if status != restStatus {
			return fmt.Errorf("concurrent request %d had status %d, expected either %d or %d", i, status, firstStatus, restStatus)
		}
	}
	if firstCount != 1 {
		return fmt.Errorf("expected exactly one response with status %d, got %d", firstStatus, firstCount)
	}
	return nil
}
