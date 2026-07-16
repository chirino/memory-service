package bdd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chirino/memory-service/internal/cmd/process/turntraces"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		t := &turnTraceSteps{s: s, sink: &captureTurnTraceSink{spans: make(chan turntraces.SpanData, 100)}}

		ctx.Step(`^the turn trace processor is running for user "([^"]*)"$`, t.processorIsRunningForUser)
		ctx.Step(`^the turn trace processor is running for user "([^"]*)" with scope "([^"]*)"$`, t.processorIsRunningForUserWithScope)
		ctx.Step(`^the turn trace processor is stopped$`, t.processorIsStopped)
		ctx.Step(`^the turn trace processor should emit a turn span for conversation "([^"]*)" with end reason "([^"]*)" within (\d+) seconds$`, t.processorShouldEmitTurnSpan)
		ctx.Step(`^the last turn trace span should have context entry count (\d+)$`, t.lastSpanShouldHaveContextEntryCount)
		ctx.Step(`^the last turn trace span should have a Langfuse LLM generation span with input containing "([^"]*)" and output "([^"]*)"$`, t.lastSpanShouldHaveLLMGenerationSpan)
		ctx.Step(`^the last turn trace span should use session "([^"]*)"$`, t.lastSpanShouldUseSession)
		ctx.Step(`^the last turn trace span metadata "([^"]*)" should be "([^"]*)"$`, t.lastSpanMetadataShouldBe)

		ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
			if shutdownErr := t.shutdown(context.Background()); shutdownErr != nil && err == nil {
				return ctx, shutdownErr
			}
			return ctx, nil
		})
	})
}

type turnTraceSteps struct {
	s       *cucumber.TestScenario
	running *turntraces.RunningProcessor
	sink    *captureTurnTraceSink
	last    *turntraces.SpanData
}

type captureTurnTraceSink struct {
	mu    sync.Mutex
	all   []turntraces.SpanData
	spans chan turntraces.SpanData
}

func (s *captureTurnTraceSink) EmitTurnSpan(_ context.Context, span turntraces.SpanData) error {
	s.mu.Lock()
	s.all = append(s.all, span)
	s.mu.Unlock()
	select {
	case s.spans <- span:
	default:
	}
	return nil
}

func (t *turnTraceSteps) processorIsRunningForUser(userID string) error {
	return t.processorIsRunningForUserWithScope(userID, "user")
}

func (t *turnTraceSteps) processorIsRunningForUserWithScope(userID, scope string) error {
	if t.running != nil {
		if err := t.shutdown(context.Background()); err != nil {
			return err
		}
	}

	addr, ok := t.s.Extra["grpcAddr"].(string)
	if !ok || addr == "" {
		return fmt.Errorf("gRPC address not configured in test suite")
	}

	t.s.RegisterCanonicalUsers(userID)
	subject := t.s.IsolatedUser(userID)
	// A user-scoped event subscription can only read context entries written by
	// the same client. The scenarios append context as test-agent-key, so run the
	// processor as that client as well. A distinct processor client would need
	// admin scope to observe another client's context.
	clientID := t.s.IsolatedClientID("test-agent-key")
	running, err := turntraces.StartProcessor(context.Background(), turntraces.StartOptions{
		Endpoint:           addr,
		ClientID:           clientID,
		BearerToken:        subject,
		Scope:              scope,
		AfterCursor:        "start",
		CheckpointInterval: 100 * time.Millisecond,
		Sink:               t.sink,
		TurnTraces: turntraces.Config{
			SessionIDMode: "conversation-group",
			MaxOpenTurns:  16,
			ServiceName:   "memory-service-turn-traces-bdd",
			Environment:   "bdd",
		},
	})
	if err != nil {
		return err
	}
	t.running = running

	// Give the gRPC subscription a short window to attach before the scenario
	// starts producing events.
	time.Sleep(150 * time.Millisecond)
	return nil
}

func (t *turnTraceSteps) processorIsStopped() error {
	return t.shutdown(context.Background())
}

func (t *turnTraceSteps) shutdown(ctx context.Context) error {
	if t.running == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := t.running.Shutdown(shutdownCtx)
	t.running = nil
	return err
}

func (t *turnTraceSteps) processorShouldEmitTurnSpan(conversationID, endReason string, timeoutSec int) error {
	expandedConversationID, err := t.s.Expand(conversationID)
	if err != nil {
		return err
	}
	deadline := time.After(time.Duration(timeoutSec) * time.Second)
	for {
		select {
		case span := <-t.sink.spans:
			if span.ConversationID == expandedConversationID && span.EndReason == endReason {
				t.last = &span
				return nil
			}
		case <-deadline:
			t.sink.mu.Lock()
			defer t.sink.mu.Unlock()
			return fmt.Errorf("timed out waiting for turn span conversation=%q endReason=%q; captured spans=%v", expandedConversationID, endReason, summarizeTurnTraceSpans(t.sink.all))
		}
	}
}

func (t *turnTraceSteps) lastSpanShouldHaveContextEntryCount(expected int) error {
	if t.last == nil {
		return fmt.Errorf("no turn trace span captured")
	}
	if t.last.ContextCount != expected {
		return fmt.Errorf("expected context entry count %d, got %d", expected, t.last.ContextCount)
	}
	return nil
}

func (t *turnTraceSteps) lastSpanShouldHaveLLMGenerationSpan(inputContains, output string) error {
	if t.last == nil {
		return fmt.Errorf("no turn trace span captured")
	}
	expectedInput, err := t.s.Expand(inputContains)
	if err != nil {
		return err
	}
	expectedOutput, err := t.s.Expand(output)
	if err != nil {
		return err
	}
	if len(t.last.ContextEntries) == 0 {
		return fmt.Errorf("expected Langfuse LLM generation span, but the turn had no context entries")
	}
	actualInput := turnTraceContextInput(t.last.ContextEntries)
	if !strings.Contains(actualInput, expectedInput) {
		return fmt.Errorf("expected Langfuse LLM generation input to contain %q, got %q", expectedInput, actualInput)
	}
	if t.last.Output != expectedOutput {
		return fmt.Errorf("expected Langfuse LLM generation output %q, got %q", expectedOutput, t.last.Output)
	}
	return nil
}

func (t *turnTraceSteps) lastSpanShouldUseSession(expected string) error {
	if t.last == nil {
		return fmt.Errorf("no turn trace span captured")
	}
	expanded, err := t.s.Expand(expected)
	if err != nil {
		return err
	}
	if t.last.SessionID != expanded {
		return fmt.Errorf("expected session %q, got %q", expanded, t.last.SessionID)
	}
	return nil
}

func (t *turnTraceSteps) lastSpanMetadataShouldBe(key, expected string) error {
	if t.last == nil {
		return fmt.Errorf("no turn trace span captured")
	}
	expanded, err := t.s.Expand(expected)
	if err != nil {
		return err
	}
	actual := t.last.Metadata[key]
	if actual != expanded {
		return fmt.Errorf("expected turn trace metadata %q to be %q, got %q", key, expanded, actual)
	}
	return nil
}

func turnTraceContextInput(entries []turntraces.ContextEntryData) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) == "" {
			continue
		}
		if entry.ID == "" {
			parts = append(parts, entry.Text)
			continue
		}
		parts = append(parts, entry.ID+": "+entry.Text)
	}
	return strings.Join(parts, "\n")
}

func summarizeTurnTraceSpans(spans []turntraces.SpanData) []map[string]string {
	summary := make([]map[string]string, 0, len(spans))
	for _, span := range spans {
		summary = append(summary, map[string]string{
			"conversationId": span.ConversationID,
			"endReason":      span.EndReason,
			"sessionId":      span.SessionID,
		})
	}
	return summary
}
