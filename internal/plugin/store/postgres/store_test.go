package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) (registrystore.MemoryStore, context.Context) {
	t.Helper()

	dbURL := testpg.StartPostgres(t)

	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	ctx := config.WithContext(context.Background(), &cfg)

	// Ensure postgres store plugin is registered
	_ = postgres.ForceImport

	// Run migrations
	err := registrymigrate.RunAll(ctx)
	require.NoError(t, err)

	// Initialize store
	loader, err := registrystore.Select("postgres")
	require.NoError(t, err)

	store, err := loader(ctx)
	require.NoError(t, err)

	return store, ctx
}

func TestCreateAndGetConversation(t *testing.T) {
	store, ctx := setupTestStore(t)

	// Create a conversation
	conv, err := store.CreateConversation(ctx, "user1", "Test Conversation", nil, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, conv)
	assert.Equal(t, "Test Conversation", conv.Title)
	assert.Equal(t, "user1", conv.OwnerUserID)
	assert.Equal(t, model.AccessLevelOwner, conv.AccessLevel)

	// Get the conversation
	got, err := store.GetConversation(ctx, "user1", conv.ID)
	require.NoError(t, err)
	assert.Equal(t, conv.ID, got.ID)
	assert.Equal(t, "Test Conversation", got.Title)
}

func TestListConversations(t *testing.T) {
	store, ctx := setupTestStore(t)

	// Create two conversations
	_, err := store.CreateConversation(ctx, "user2", "Conv A", nil, nil, nil)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond) // ensure ordering
	_, err = store.CreateConversation(ctx, "user2", "Conv B", nil, nil, nil)
	require.NoError(t, err)

	// List conversations
	summaries, cursor, err := store.ListConversations(ctx, "user2", nil, nil, 10, model.ListModeAll)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(summaries), 2)
	_ = cursor
}

func TestDeleteConversation(t *testing.T) {
	store, ctx := setupTestStore(t)

	conv, err := store.CreateConversation(ctx, "user3", "To Delete", nil, nil, nil)
	require.NoError(t, err)

	err = store.DeleteConversation(ctx, "user3", conv.ID)
	require.NoError(t, err)

	// Should return not found after delete
	_, err = store.GetConversation(ctx, "user3", conv.ID)
	assert.Error(t, err)
}

func TestAppendAndGetEntries(t *testing.T) {
	store, ctx := setupTestStore(t)

	conv, err := store.CreateConversation(ctx, "user4", "Entry Test", nil, nil, nil)
	require.NoError(t, err)

	// Append entries
	entries, err := store.AppendEntries(ctx, "user4", conv.ID, []registrystore.CreateEntryRequest{
		{Content: json.RawMessage(`[{"type":"text","text":"Hello"}]`), ContentType: "application/json", Channel: "history"},
		{Content: json.RawMessage(`[{"type":"text","text":"World"}]`), ContentType: "application/json", Channel: "history"},
	}, nil, nil)
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	// Get entries
	result, err := store.GetEntries(ctx, "user4", conv.ID, nil, 10, nil, nil, nil, false)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(result.Data), 2)
}

func TestMemberships(t *testing.T) {
	store, ctx := setupTestStore(t)

	conv, err := store.CreateConversation(ctx, "owner1", "Shared Conv", nil, nil, nil)
	require.NoError(t, err)

	// Share with another user
	m, err := store.ShareConversation(ctx, "owner1", conv.ID, "reader1", model.AccessLevelReader)
	require.NoError(t, err)
	assert.Equal(t, "reader1", m.UserID)
	assert.Equal(t, model.AccessLevelReader, m.AccessLevel)

	// List memberships
	memberships, _, err := store.ListMemberships(ctx, "owner1", conv.ID, nil, 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(memberships), 2) // owner + reader

	// Reader can see the conversation
	_, err = store.GetConversation(ctx, "reader1", conv.ID)
	require.NoError(t, err)

	// Delete membership
	err = store.DeleteMembership(ctx, "owner1", conv.ID, "reader1")
	require.NoError(t, err)
}

func TestConversationAccessControl(t *testing.T) {
	store, ctx := setupTestStore(t)

	conv, err := store.CreateConversation(ctx, "owner2", "Private Conv", nil, nil, nil)
	require.NoError(t, err)

	// Unauthorized user cannot see the conversation
	_, err = store.GetConversation(ctx, "stranger", conv.ID)
	assert.Error(t, err)
}

func TestOwnershipTransfers(t *testing.T) {
	store, ctx := setupTestStore(t)

	conv, err := store.CreateConversation(ctx, "from_user", "Transfer Conv", nil, nil, nil)
	require.NoError(t, err)
	_, err = store.ShareConversation(ctx, "from_user", conv.ID, "to_user", model.AccessLevelReader)
	require.NoError(t, err)

	// Create transfer
	transfer, err := store.CreateOwnershipTransfer(ctx, "from_user", conv.ID, "to_user")
	require.NoError(t, err)
	assert.Equal(t, "from_user", transfer.FromUserID)
	assert.Equal(t, "to_user", transfer.ToUserID)

	// Get transfer
	got, err := store.GetTransfer(ctx, "from_user", transfer.ID)
	require.NoError(t, err)
	assert.Equal(t, transfer.ID, got.ID)

	// Accept transfer
	err = store.AcceptTransfer(ctx, "to_user", transfer.ID)
	require.NoError(t, err)
}

func TestAdminRestoreConversationConflictAndSuccess(t *testing.T) {
	store, ctx := setupTestStore(t)

	conv, err := store.CreateConversation(ctx, "admin-user", "Admin Restore", nil, nil, nil)
	require.NoError(t, err)

	err = store.AdminRestoreConversation(ctx, conv.ID)
	require.Error(t, err)
	var conflict *registrystore.ConflictError
	require.True(t, errors.As(err, &conflict), "expected conflict error, got %T", err)

	err = store.AdminDeleteConversation(ctx, conv.ID)
	require.NoError(t, err)

	err = store.AdminRestoreConversation(ctx, conv.ID)
	require.NoError(t, err)

	restored, err := store.AdminGetConversation(ctx, conv.ID)
	require.NoError(t, err)
	assert.Nil(t, restored.DeletedAt)
}

func TestAdminGetEntriesForkModes(t *testing.T) {
	store, ctx := setupTestStore(t)

	root, err := store.CreateConversation(ctx, "owner", "Root", nil, nil, nil)
	require.NoError(t, err)

	rootEntry1, err := store.AppendEntries(ctx, "owner", root.ID, []registrystore.CreateEntryRequest{
		{Content: json.RawMessage(`"root-1"`), ContentType: "text/plain", Channel: "history"},
	}, nil, nil)
	require.NoError(t, err)
	require.Len(t, rootEntry1, 1)
	time.Sleep(5 * time.Millisecond)
	rootEntry2, err := store.AppendEntries(ctx, "owner", root.ID, []registrystore.CreateEntryRequest{
		{Content: json.RawMessage(`"root-2"`), ContentType: "text/plain", Channel: "history"},
	}, nil, nil)
	require.NoError(t, err)
	require.Len(t, rootEntry2, 1)

	fork, err := store.CreateConversation(ctx, "owner", "Fork", nil, &root.ID, &rootEntry1[0].ID)
	require.NoError(t, err)
	forkEntries, err := store.AppendEntries(ctx, "owner", fork.ID, []registrystore.CreateEntryRequest{
		{Content: json.RawMessage(`"fork-1"`), ContentType: "text/plain", Channel: "history"},
	}, nil, nil)
	require.NoError(t, err)
	require.Len(t, forkEntries, 1)

	ancestryOnly, err := store.AdminGetEntries(ctx, fork.ID, registrystore.AdminMessageQuery{
		Limit:    20,
		AllForks: false,
	})
	require.NoError(t, err)
	require.Len(t, ancestryOnly.Data, 2)
	assert.Equal(t, rootEntry1[0].ID, ancestryOnly.Data[0].ID)
	assert.Equal(t, forkEntries[0].ID, ancestryOnly.Data[1].ID)

	allForks, err := store.AdminGetEntries(ctx, fork.ID, registrystore.AdminMessageQuery{
		Limit:    20,
		AllForks: true,
	})
	require.NoError(t, err)
	require.Len(t, allForks.Data, 3)
	assert.Equal(t, rootEntry1[0].ID, allForks.Data[0].ID)
	assert.Equal(t, rootEntry2[0].ID, allForks.Data[1].ID)
	assert.Equal(t, forkEntries[0].ID, allForks.Data[2].ID)
}
