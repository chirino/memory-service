package sqlentry

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestBoundedEntriesFromBaseForwardCursor(t *testing.T) {
	db, entries := setupPagerTestEntries(t)
	base := pagerTestBase(db)
	lookup := pagerTestLookup(base)

	page, afterCursor, beforeCursor, err := BoundedEntriesFromBase(context.Background(), base, nil, nil, nil, false, 2, lookup, pagerTestCursorError, UUIDStringValue, "scan")
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{entries[0].ID, entries[1].ID}, pagerEntryIDs(page))
	require.Equal(t, entries[1].ID.String(), requireString(t, afterCursor))
	require.Nil(t, beforeCursor)

	page, afterCursor, beforeCursor, err = BoundedEntriesFromBase(context.Background(), base, nil, afterCursor, nil, false, 2, lookup, pagerTestCursorError, UUIDStringValue, "scan")
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{entries[2].ID}, pagerEntryIDs(page))
	require.Nil(t, afterCursor)
	require.Equal(t, entries[2].ID.String(), requireString(t, beforeCursor))
}

func TestBoundedEntriesFromBaseTailCursor(t *testing.T) {
	db, entries := setupPagerTestEntries(t)
	base := pagerTestBase(db)
	lookup := pagerTestLookup(base)

	page, afterCursor, beforeCursor, err := BoundedEntriesFromBase(context.Background(), base, nil, nil, nil, true, 2, lookup, pagerTestCursorError, UUIDStringValue, "scan")
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{entries[1].ID, entries[2].ID}, pagerEntryIDs(page))
	require.Nil(t, afterCursor)
	require.Equal(t, entries[1].ID.String(), requireString(t, beforeCursor))
}

func TestBoundedEntriesFromBaseMissingCursor(t *testing.T) {
	db, _ := setupPagerTestEntries(t)
	base := pagerTestBase(db)
	lookup := pagerTestLookup(base)
	missing := uuid.NewString()

	_, _, _, err := BoundedEntriesFromBase(context.Background(), base, nil, &missing, nil, false, 2, lookup, pagerTestCursorError, UUIDStringValue, "scan")
	require.ErrorContains(t, err, "afterCursor")
	require.ErrorContains(t, err, missing)
}

func TestBoundedEntriesFromBaseRejectsZeroLimit(t *testing.T) {
	db, _ := setupPagerTestEntries(t)
	base := pagerTestBase(db)

	_, _, _, err := BoundedEntriesFromBase(context.Background(), base, nil, nil, nil, false, 0, pagerTestLookup(base), pagerTestCursorError, UUIDStringValue, "scan")
	require.ErrorContains(t, err, "limit must be greater than zero")
}

func setupPagerTestEntries(t *testing.T) (*gorm.DB, []model.Entry) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE entries (
			id TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			conversation_group_id TEXT NOT NULL,
			user_id TEXT,
			client_id TEXT,
			agent_id TEXT,
			channel TEXT NOT NULL,
			epoch INTEGER,
			seq INTEGER,
			content_type TEXT NOT NULL,
			content BLOB NOT NULL,
			indexed_content TEXT,
			indexed_at DATETIME,
			created_at DATETIME NOT NULL,
			PRIMARY KEY (id, conversation_group_id)
		)
	`).Error)

	groupID := uuid.New()
	baseTime := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	entries := []model.Entry{
		pagerTestEntry(groupID, baseTime),
		pagerTestEntry(groupID, baseTime.Add(time.Second)),
		pagerTestEntry(groupID, baseTime.Add(2*time.Second)),
	}
	require.NoError(t, db.Create(&entries).Error)
	return db, entries
}

func pagerTestEntry(groupID uuid.UUID, createdAt time.Time) model.Entry {
	return model.Entry{
		ID:                  uuid.New(),
		ConversationID:      "conversation-1",
		ConversationGroupID: groupID,
		Channel:             model.ChannelHistory,
		ContentType:         "application/json",
		Content:             []byte(`{}`),
		CreatedAt:           createdAt,
	}
}

func pagerTestBase(db *gorm.DB) *gorm.DB {
	return db.Model(&model.Entry{}).
		Table("entries AS e").
		Select("e.*").
		Where("e.conversation_id = ?", "conversation-1")
}

func pagerTestLookup(base *gorm.DB) LookupFunc {
	return func(entryID string) (model.Entry, bool, error) {
		var entry model.Entry
		result := base.Session(&gorm.Session{}).
			Where("e.id = ?", entryID).
			Limit(1).
			Find(&entry)
		return entry, result.RowsAffected > 0, result.Error
	}
}

func pagerTestCursorError(_ context.Context, name, value string) error {
	return fmt.Errorf("%s not found: %s", name, value)
}

func pagerEntryIDs(entries []model.Entry) []uuid.UUID {
	ids := make([]uuid.UUID, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func requireString(t *testing.T, value *string) string {
	t.Helper()
	require.NotNil(t, value)
	return *value
}
