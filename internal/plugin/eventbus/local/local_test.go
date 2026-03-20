package local

import (
	"context"
	"testing"
	"time"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/google/uuid"
)

func TestPublishSubscribe(t *testing.T) {
	bus := New(64)
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	event := registryeventbus.Event{
		Event: "created",
		Kind:  "conversation",
		Data:  map[string]any{"id": uuid.New()},
	}
	if err := bus.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}

	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		if got.Event != "created" || got.Kind != "conversation" {
			t.Errorf("unexpected event: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	bus := New(64)
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	event := registryeventbus.Event{
		Event: "updated",
		Kind:  "entry",
		Data:  map[string]any{"id": uuid.New()},
	}
	if err := bus.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}

	for i, ch := range []<-chan registryeventbus.Event{ch1, ch2} {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("subscriber %d: channel closed unexpectedly", i)
			}
			if got.Event != "updated" || got.Kind != "entry" {
				t.Errorf("subscriber %d: unexpected event: %+v", i, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timeout waiting for event", i)
		}
	}
}

func TestAllKindsDelivered(t *testing.T) {
	bus := New(64)
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	kinds := []string{"conversation", "entry", "response", "membership", "stream"}
	for _, kind := range kinds {
		event := registryeventbus.Event{
			Event: "created",
			Kind:  kind,
			Data:  nil,
		}
		if err := bus.Publish(ctx, event); err != nil {
			t.Fatalf("publish kind %q: %v", kind, err)
		}
	}

	for _, expectedKind := range kinds {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed before receiving kind %q", expectedKind)
			}
			if got.Kind != expectedKind {
				t.Errorf("expected kind %q, got %q", expectedKind, got.Kind)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for kind %q", expectedKind)
		}
	}
}

func TestSlowConsumerEviction(t *testing.T) {
	bus := New(2)
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Publish 5 events without reading — buffer of 2 will overflow.
	for i := 0; i < 5; i++ {
		event := registryeventbus.Event{
			Event: "created",
			Kind:  "conversation",
			Data:  map[string]any{"i": i},
		}
		if err := bus.Publish(ctx, event); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Drain whatever is in the channel; it should be closed eventually.
	timeout := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // success: channel was closed
			}
		case <-timeout:
			t.Fatal("timeout waiting for slow consumer eviction (channel close)")
		}
	}
}

func TestContextCancellation(t *testing.T) {
	bus := New(64)
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	// The cleanup goroutine should close the channel.
	timeout := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // success: channel was closed
			}
		case <-timeout:
			t.Fatal("timeout waiting for channel close after context cancellation")
		}
	}
}

func TestClose(t *testing.T) {
	bus := New(64)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}

	// Channel should be closed after bus.Close().
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed, but got an event")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close after bus.Close()")
	}
}

func TestPublishAfterClose(t *testing.T) {
	bus := New(64)
	bus.Close()

	// Should not panic.
	err := bus.Publish(context.Background(), registryeventbus.Event{
		Event: "created",
		Kind:  "conversation",
		Data:  nil,
	})
	if err != nil {
		t.Fatalf("unexpected error publishing to closed bus: %v", err)
	}
}

func TestSubscribeAfterClose(t *testing.T) {
	bus := New(64)
	bus.Close()

	ch, err := bus.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Channel returned from a closed bus should already be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed immediately")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout: channel from closed bus should be immediately closed")
	}
}
