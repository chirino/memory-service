package eventstream

import (
	"context"
	"testing"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/stretchr/testify/require"
)

// relayFakeStore satisfies MemoryStore by embedding the interface; only
// RelayPublishesOutboxEvents is called by PublishEvents on the user-targeted path.
type relayFakeStore struct {
	registrystore.MemoryStore
	relayPublishes bool
}

func (s relayFakeStore) RelayPublishesOutboxEvents() bool { return s.relayPublishes }

type recordingBus struct {
	published []registryeventbus.Event
}

func (b *recordingBus) Publish(_ context.Context, event registryeventbus.Event) error {
	b.published = append(b.published, event)
	return nil
}

func (b *recordingBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return nil, nil
}

func (b *recordingBus) Close() error { return nil }

// When the store's outbox relay owns publication, request-path publishing must be
// skipped so committed events are not delivered twice.
func TestPublishEventsSkipsWhenRelayPublishesOutboxEvents(t *testing.T) {
	event := registryeventbus.Event{Event: "created", Kind: "conversation", UserIDs: []string{"alice"}}

	relayBus := &recordingBus{}
	err := PublishEvents(context.Background(), relayFakeStore{relayPublishes: true}, relayBus, event)
	require.NoError(t, err)
	require.Empty(t, relayBus.published)

	directBus := &recordingBus{}
	err = PublishEvents(context.Background(), relayFakeStore{relayPublishes: false}, directBus, event)
	require.NoError(t, err)
	require.Len(t, directBus.published, 1)
	require.Equal(t, []string{"alice"}, directBus.published[0].UserIDs)
}
