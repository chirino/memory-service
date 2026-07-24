package bdd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		e := &grpcEventSteps{s: s, streams: make(map[string]*grpcEventStream)}

		ctx.Step(`^"([^"]*)" is connected to the gRPC event stream$`, e.userIsConnectedToGRPCEventStream)
		ctx.Step(`^the current client is connected to the gRPC event stream as "([^"]*)"$`, e.currentClientIsConnectedToGRPCEventStreamAs)
		ctx.Step(`^"([^"]*)" is connected to the gRPC event stream filtered to kinds "([^"]*)"$`, e.userIsConnectedToGRPCEventStreamFilteredToKinds)
		ctx.Step(`^"([^"]*)" is connected to the gRPC event stream filtered to kinds "([^"]*)" and entry channels "([^"]*)"$`, e.userIsConnectedToGRPCEventStreamFilteredToKindsAndEntryChannels)
		ctx.Step(`^"([^"]*)" is connected to the gRPC event stream filtered to kinds "([^"]*)" entry channels "([^"]*)" content types "([^"]*)" and roles "([^"]*)"$`, e.userIsConnectedToGRPCEventStreamWithEntryFilters)
		ctx.Step(`^"([^"]*)" is connected to the gRPC event stream after cursor "([^"]*)" with detail "([^"]*)"$`, e.userIsConnectedToGRPCEventStreamAfterCursorWithDetail)
		ctx.Step(`^"([^"]*)" should receive a gRPC event with kind "([^"]*)" and event "([^"]*)" within (\d+) seconds$`, e.userShouldReceiveGRPCEvent)
		ctx.Step(`^"([^"]*)" should receive a gRPC event with kind "([^"]*)" and event "([^"]*)"$`, e.userShouldReceiveGRPCEventDefault)
		ctx.Step(`^"([^"]*)" should receive a gRPC event with kind "([^"]*)" and event "([^"]*)" where data "([^"]*)" is "([^"]*)"$`, e.userShouldReceiveGRPCEventWithDataField)
		ctx.Step(`^"([^"]*)" should not receive a gRPC event with kind "([^"]*)" and event "([^"]*)" within (\d+) seconds$`, e.userShouldNotReceiveGRPCEventWithKind)
		ctx.Step(`^"([^"]*)" should not receive any gRPC event within (\d+) seconds$`, e.userShouldNotReceiveGRPCEvent)
		ctx.Step(`^the gRPC event cursor should be saved as "([^"]*)"$`, e.saveGRPCEventCursor)
		ctx.Step(`^the gRPC event cursor should match the Postgres outbox format$`, e.grpcEventCursorShouldMatchPostgresOutboxFormat)
		ctx.Step(`^the gRPC event data should contain "([^"]*)"$`, e.grpcEventDataShouldContain)
		ctx.Step(`^the gRPC event data "([^"]*)" should be "([^"]*)"$`, e.grpcEventDataFieldShouldBe)

		ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
			e.closeAll()
			return ctx, nil
		})
	})
}

type grpcEventStream struct {
	events chan map[string]any
	cancel context.CancelFunc
	conn   *grpc.ClientConn
	done   chan struct{}
}

type grpcEventSteps struct {
	s         *cucumber.TestScenario
	streams   map[string]*grpcEventStream
	lastEvent map[string]any
	mu        sync.Mutex
}

func (e *grpcEventSteps) openGRPCEventStream(userID string, kinds []string, entryChannels []string, entryContentTypes []string, entryRoles []string, afterCursor, detail *string) error {
	e.s.RegisterCanonicalUsers(userID)
	subject := e.s.IsolatedUser(userID)
	authCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+subject))
	return e.openGRPCEventStreamWithContext(userID, authCtx, kinds, entryChannels, entryContentTypes, entryRoles, afterCursor, detail)
}

func (e *grpcEventSteps) openGRPCEventStreamWithContext(streamID string, authCtx context.Context, kinds []string, entryChannels []string, entryContentTypes []string, entryRoles []string, afterCursor, detail *string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if existing, ok := e.streams[streamID]; ok {
		existing.cancel()
		<-existing.done
	}

	addr, ok := e.s.Extra["grpcAddr"].(string)
	if !ok || addr == "" {
		return fmt.Errorf("gRPC address not configured in test suite")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(authCtx)

	stream, err := pb.NewEventStreamServiceClient(conn).SubscribeEvents(ctx, &pb.SubscribeEventsRequest{
		Kinds:             kinds,
		EntryChannels:     entryChannels,
		EntryContentTypes: entryContentTypes,
		EntryRoles:        entryRoles,
		AfterCursor:       afterCursor,
		Detail:            detail,
	})
	if err != nil {
		cancel()
		_ = conn.Close()
		return err
	}

	grpcStream := &grpcEventStream{
		events: make(chan map[string]any, 100),
		cancel: cancel,
		conn:   conn,
		done:   make(chan struct{}),
	}
	e.streams[streamID] = grpcStream

	go func() {
		defer close(grpcStream.done)
		defer close(grpcStream.events)
		defer conn.Close()
		for {
			msg, err := stream.Recv()
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return
			}
			if err != nil {
				return
			}
			event := map[string]any{
				"event": msg.GetEvent(),
				"kind":  msg.GetKind(),
			}
			if msg.GetCursor() != "" {
				event["cursor"] = msg.GetCursor()
			}
			if len(msg.GetData()) > 0 {
				var data map[string]any
				if err := json.Unmarshal(msg.GetData(), &data); err == nil {
					event["data"] = data
				}
			}
			select {
			case grpcStream.events <- event:
			default:
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	return nil
}

func (e *grpcEventSteps) currentClientIsConnectedToGRPCEventStreamAs(streamID string) error {
	return e.openGRPCEventStreamWithContext(streamID, grpcAuthContext(e.s), nil, nil, nil, nil, nil, nil)
}

func (e *grpcEventSteps) closeAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, stream := range e.streams {
		stream.cancel()
		<-stream.done
	}
	e.streams = make(map[string]*grpcEventStream)
}

func (e *grpcEventSteps) userIsConnectedToGRPCEventStream(userID string) error {
	return e.openGRPCEventStream(userID, nil, nil, nil, nil, nil, nil)
}

func (e *grpcEventSteps) userIsConnectedToGRPCEventStreamFilteredToKinds(userID, kinds string) error {
	return e.openGRPCEventStream(userID, parseCSV(kinds), nil, nil, nil, nil, nil)
}

func (e *grpcEventSteps) userIsConnectedToGRPCEventStreamFilteredToKindsAndEntryChannels(userID, kinds, entryChannels string) error {
	return e.openGRPCEventStream(userID, parseCSV(kinds), parseCSV(entryChannels), nil, nil, nil, nil)
}

func (e *grpcEventSteps) userIsConnectedToGRPCEventStreamWithEntryFilters(userID, kinds, entryChannels, entryContentTypes, entryRoles string) error {
	return e.openGRPCEventStream(userID, parseCSV(kinds), parseCSV(entryChannels), parseCSV(entryContentTypes), parseCSV(entryRoles), nil, nil)
}

func parseCSV(raw string) []string {
	var filter []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			filter = append(filter, item)
		}
	}
	return filter
}

func (e *grpcEventSteps) userIsConnectedToGRPCEventStreamAfterCursorWithDetail(userID, afterCursor, detail string) error {
	expandedCursor, err := e.s.Expand(afterCursor)
	if err != nil {
		return err
	}
	expandedDetail, err := e.s.Expand(detail)
	if err != nil {
		return err
	}
	return e.openGRPCEventStream(userID, nil, nil, nil, nil, &expandedCursor, &expandedDetail)
}

func (e *grpcEventSteps) userShouldReceiveGRPCEventDefault(userID, kind, event string) error {
	return e.userShouldReceiveGRPCEvent(userID, kind, event, 5)
}

func (e *grpcEventSteps) userShouldReceiveGRPCEvent(userID, kind, eventType string, timeoutSec int) error {
	e.mu.Lock()
	stream, ok := e.streams[userID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("no gRPC event stream open for user %q", userID)
	}

	timeout := time.After(time.Duration(timeoutSec) * time.Second)
	for {
		select {
		case evt, ok := <-stream.events:
			if !ok {
				return fmt.Errorf("gRPC event stream for %q closed before receiving kind=%q event=%q", userID, kind, eventType)
			}
			if evt["kind"] == kind && evt["event"] == eventType {
				e.lastEvent = evt
				return nil
			}
		case <-timeout:
			return fmt.Errorf("timed out after %ds waiting for gRPC event kind=%q event=%q for user %q", timeoutSec, kind, eventType, userID)
		}
	}
}

func (e *grpcEventSteps) userShouldReceiveGRPCEventWithDataField(userID, kind, eventType, field, expected string) error {
	e.mu.Lock()
	stream, ok := e.streams[userID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("no gRPC event stream open for user %q", userID)
	}

	timeout := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-stream.events:
			if !ok {
				return fmt.Errorf("gRPC event stream for %q closed before receiving kind=%q event=%q with %s=%q", userID, kind, eventType, field, expected)
			}
			if evt["kind"] != kind || evt["event"] != eventType {
				continue
			}
			data, ok := evt["data"].(map[string]any)
			if !ok {
				continue
			}
			if fmt.Sprintf("%v", data[field]) != expected {
				continue
			}
			e.lastEvent = evt
			return nil
		case <-timeout:
			return fmt.Errorf("timed out after 5s waiting for gRPC event kind=%q event=%q with %s=%q for user %q", kind, eventType, field, expected, userID)
		}
	}
}

func (e *grpcEventSteps) userShouldNotReceiveGRPCEvent(userID string, timeoutSec int) error {
	e.mu.Lock()
	stream, ok := e.streams[userID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("no gRPC event stream open for user %q", userID)
	}

	timeout := time.After(time.Duration(timeoutSec) * time.Second)
	for {
		select {
		case evt, ok := <-stream.events:
			if !ok {
				return nil
			}
			if evt["kind"] == "stream" && evt["event"] == "phase" {
				continue
			}
			return fmt.Errorf("expected no gRPC event for %q within %ds, but received: %v", userID, timeoutSec, evt)
		case <-timeout:
			return nil
		}
	}
}

func (e *grpcEventSteps) userShouldNotReceiveGRPCEventWithKind(userID, kind, eventType string, timeoutSec int) error {
	e.mu.Lock()
	stream, ok := e.streams[userID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("no gRPC event stream open for user %q", userID)
	}

	timeout := time.After(time.Duration(timeoutSec) * time.Second)
	for {
		select {
		case evt, ok := <-stream.events:
			if !ok {
				return nil
			}
			if evt["kind"] == kind && evt["event"] == eventType {
				return fmt.Errorf("expected no gRPC event kind=%q event=%q for %q within %ds, but received: %v", kind, eventType, userID, timeoutSec, evt)
			}
		case <-timeout:
			return nil
		}
	}
}

func (e *grpcEventSteps) saveGRPCEventCursor(varName string) error {
	if e.lastEvent == nil {
		return fmt.Errorf("no gRPC event captured for assertion")
	}
	cursor, ok := e.lastEvent["cursor"].(string)
	if !ok || strings.TrimSpace(cursor) == "" {
		return fmt.Errorf("gRPC event has no cursor: %v", e.lastEvent)
	}
	e.s.Variables[varName] = cursor
	return nil
}

func (e *grpcEventSteps) grpcEventCursorShouldMatchPostgresOutboxFormat() error {
	if e.lastEvent == nil {
		return fmt.Errorf("no gRPC event captured for assertion")
	}
	cursor, ok := e.lastEvent["cursor"].(string)
	if !ok || strings.TrimSpace(cursor) == "" {
		return fmt.Errorf("gRPC event has no cursor: %v", e.lastEvent)
	}
	if !postgresOutboxCursorPattern.MatchString(cursor) {
		return fmt.Errorf("gRPC event cursor %q does not match Postgres outbox numeric cursor format", cursor)
	}
	return nil
}

func (e *grpcEventSteps) grpcEventDataShouldContain(field string) error {
	if e.lastEvent == nil {
		return fmt.Errorf("no gRPC event captured for assertion")
	}
	data, ok := e.lastEvent["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("gRPC event has no 'data' object: %v", e.lastEvent)
	}
	if _, ok := data[field]; !ok {
		return fmt.Errorf("gRPC event data does not contain field %q: %v", field, data)
	}
	return nil
}

func (e *grpcEventSteps) grpcEventDataFieldShouldBe(field, expected string) error {
	if e.lastEvent == nil {
		return fmt.Errorf("no gRPC event captured for assertion")
	}
	data, ok := e.lastEvent["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("gRPC event has no 'data' object: %v", e.lastEvent)
	}
	actual := fmt.Sprintf("%v", data[field])
	if actual != expected {
		return fmt.Errorf("gRPC event data field %q: expected %q, got %q", field, expected, actual)
	}
	return nil
}
