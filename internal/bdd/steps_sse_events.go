package bdd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		e := &sseEventSteps{s: s, streams: make(map[string]*sseStream)}

		// Connection management
		ctx.Step(`^"([^"]*)" is connected to the SSE event stream$`, e.userIsConnectedToSSEStream)
		ctx.Step(`^"([^"]*)" is connected to the SSE event stream filtered to kinds "([^"]*)"$`, e.userIsConnectedToSSEStreamFilteredToKinds)
		ctx.Step(`^"([^"]*)" is connected to the admin SSE event stream with justification "([^"]*)"$`, e.userIsConnectedToAdminSSEStream)

		// Event assertions
		ctx.Step(`^"([^"]*)" should receive an SSE event with kind "([^"]*)" and event "([^"]*)" within (\d+) seconds$`, e.userShouldReceiveSSEEvent)
		ctx.Step(`^"([^"]*)" should receive an SSE event with kind "([^"]*)" and event "([^"]*)"$`, e.userShouldReceiveSSEEventDefault)
		ctx.Step(`^"([^"]*)" should not receive any SSE event within (\d+) seconds$`, e.userShouldNotReceiveSSEEvent)
		ctx.Step(`^the SSE event data should contain "([^"]*)"$`, e.sseEventDataShouldContain)
		ctx.Step(`^the SSE event data "([^"]*)" should be "([^"]*)"$`, e.sseEventDataFieldShouldBe)

		// Cleanup on scenario end
		ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
			e.closeAll()
			return ctx, nil
		})
	})
}

// sseStream represents a background SSE connection that collects events.
type sseStream struct {
	events chan map[string]any
	cancel context.CancelFunc
	resp   *http.Response
	done   chan struct{}
}

type sseEventSteps struct {
	s         *cucumber.TestScenario
	streams   map[string]*sseStream // keyed by user name
	lastEvent map[string]any        // last matched event for further assertions
	mu        sync.Mutex
}

func (e *sseEventSteps) openSSEStream(userID, path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Close existing stream for this user if any.
	if existing, ok := e.streams[userID]; ok {
		existing.cancel()
		<-existing.done
	}

	// Resolve the isolated user subject for auth.
	e.s.RegisterCanonicalUsers(userID)
	subject := e.s.IsolatedUser(userID)

	ctx, cancel := context.WithCancel(context.Background())
	url := e.s.Suite.APIURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return err
	}
	req.Header.Set("Authorization", "Bearer "+subject)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		return fmt.Errorf("SSE connect failed: status %d, body: %s", resp.StatusCode, body)
	}

	stream := &sseStream{
		events: make(chan map[string]any, 100),
		cancel: cancel,
		resp:   resp,
		done:   make(chan struct{}),
	}
	e.streams[userID] = stream

	// Background goroutine reads SSE data lines and parses JSON events.
	go func() {
		defer close(stream.done)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			var event map[string]any
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			select {
			case stream.events <- event:
			default:
				// Buffer full — drop (test should consume events fast enough).
			}
		}
	}()

	// Give the connection a moment to establish and subscribe to the bus.
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (e *sseEventSteps) closeAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, stream := range e.streams {
		stream.cancel()
		<-stream.done
	}
	e.streams = make(map[string]*sseStream)
}

func (e *sseEventSteps) userIsConnectedToSSEStream(userID string) error {
	return e.openSSEStream(userID, "/v1/events")
}

func (e *sseEventSteps) userIsConnectedToSSEStreamFilteredToKinds(userID, kinds string) error {
	return e.openSSEStream(userID, "/v1/events?kinds="+kinds)
}

func (e *sseEventSteps) userIsConnectedToAdminSSEStream(userID, justification string) error {
	return e.openSSEStream(userID, "/v1/admin/events?justification="+url.QueryEscape(justification))
}

func (e *sseEventSteps) userShouldReceiveSSEEventDefault(userID, kind, event string) error {
	return e.userShouldReceiveSSEEvent(userID, kind, event, 5)
}

func (e *sseEventSteps) userShouldReceiveSSEEvent(userID, kind, eventType string, timeoutSec int) error {
	e.mu.Lock()
	stream, ok := e.streams[userID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("no SSE stream open for user %q", userID)
	}

	timeout := time.After(time.Duration(timeoutSec) * time.Second)
	for {
		select {
		case evt, ok := <-stream.events:
			if !ok {
				return fmt.Errorf("SSE stream for %q closed before receiving kind=%q event=%q", userID, kind, eventType)
			}
			if evt["kind"] == kind && evt["event"] == eventType {
				e.lastEvent = evt
				return nil
			}
			// Not the event we're looking for — keep consuming.
		case <-timeout:
			return fmt.Errorf("timed out after %ds waiting for SSE event kind=%q event=%q for user %q", timeoutSec, kind, eventType, userID)
		}
	}
}

func (e *sseEventSteps) userShouldNotReceiveSSEEvent(userID string, timeoutSec int) error {
	e.mu.Lock()
	stream, ok := e.streams[userID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("no SSE stream open for user %q", userID)
	}

	timeout := time.After(time.Duration(timeoutSec) * time.Second)
	select {
	case evt, ok := <-stream.events:
		if !ok {
			return nil // Stream closed — no events.
		}
		return fmt.Errorf("expected no SSE event for %q within %ds, but received: %v", userID, timeoutSec, evt)
	case <-timeout:
		return nil // Good — no event received within the timeout.
	}
}

func (e *sseEventSteps) sseEventDataShouldContain(field string) error {
	if e.lastEvent == nil {
		return fmt.Errorf("no SSE event captured for assertion")
	}
	data, ok := e.lastEvent["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("SSE event has no 'data' object: %v", e.lastEvent)
	}
	if _, ok := data[field]; !ok {
		return fmt.Errorf("SSE event data does not contain field %q: %v", field, data)
	}
	return nil
}

func (e *sseEventSteps) sseEventDataFieldShouldBe(field, expected string) error {
	if e.lastEvent == nil {
		return fmt.Errorf("no SSE event captured for assertion")
	}
	data, ok := e.lastEvent["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("SSE event has no 'data' object: %v", e.lastEvent)
	}
	actual := fmt.Sprintf("%v", data[field])
	if actual != expected {
		return fmt.Errorf("SSE event data field %q: expected %q, got %q", field, expected, actual)
	}
	return nil
}
