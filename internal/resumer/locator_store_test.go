package resumer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLocalLocatorStoreLifecycle(t *testing.T) {
	store := newLocalLocatorStore()
	require.True(t, store.Available())

	locator := Locator{Host: "127.0.0.1", Port: 8080, FileName: "recording.tokens"}
	err := store.Upsert(context.Background(), "conversation-a", locator, time.Second)
	require.NoError(t, err)

	got, err := store.Get(context.Background(), "conversation-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, locator, *got)

	exists, err := store.Exists(context.Background(), "conversation-a")
	require.NoError(t, err)
	require.True(t, exists)

	err = store.Remove(context.Background(), "conversation-a")
	require.NoError(t, err)

	got, err = store.Get(context.Background(), "conversation-a")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestLocalLocatorStoreExpiry(t *testing.T) {
	store := newLocalLocatorStore()
	err := store.Upsert(context.Background(), "conversation-a", Locator{Host: "127.0.0.1", Port: 8080}, 50*time.Millisecond)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		exists, existsErr := store.Exists(context.Background(), "conversation-a")
		require.NoError(t, existsErr)
		return !exists
	}, time.Second, 25*time.Millisecond)
}
