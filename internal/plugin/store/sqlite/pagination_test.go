package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEntryOrderLessDistinguishesNullSeqFromZero(t *testing.T) {
	createdAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	zero := uint32(0)
	nullSeq := model.Entry{ID: uuid.MustParse("00000000-0000-0000-0000-0000000000ff"), CreatedAt: createdAt}
	seqZero := model.Entry{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), CreatedAt: createdAt, Seq: &zero}

	assert.True(t, entryOrderLess(nullSeq, seqZero))
	assert.False(t, entryOrderLess(seqZero, nullSeq))
}

func TestBoundedHistoryTailMaterializesOnlyLimitPlusOneEntries(t *testing.T) {
	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "pagination.db"),
		DatastoreMigrateAtStart: true,
		EncryptionDBDisabled:    true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	handle, err := getSharedHandle(ctx)
	require.NoError(t, err)
	store := &SQLiteStore{handle: handle, db: handle.db, cfg: cfg}

	groupID := uuid.New()
	conversationID := uuid.NewString()
	require.NoError(t, handle.db.Create(&model.ConversationGroup{ID: groupID}).Error)
	require.NoError(t, handle.db.Create(&model.Conversation{
		ID:                  conversationID,
		OwnerUserID:         "alice",
		ClientID:            "test-client",
		ConversationGroupID: groupID,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}).Error)
	for i := 0; i < 100; i++ {
		require.NoError(t, handle.db.Create(&model.Entry{
			ID:                  uuid.New(),
			ConversationID:      conversationID,
			ConversationGroupID: groupID,
			Channel:             model.ChannelHistory,
			ContentType:         "history",
			Content:             []byte("[]"),
			CreatedAt:           time.Date(2026, 7, 10, 12, 0, i, 0, time.UTC),
		}).Error)
	}

	materialized := int64(0)
	callbackName := fmt.Sprintf("test:count-bounded-pagination-%s", t.Name())
	require.NoError(t, handle.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement.Table == "entries" && len(db.Statement.Selects) == 0 {
			materialized += db.RowsAffected
		}
	}))
	t.Cleanup(func() { handle.db.Callback().Query().Remove(callbackName) })

	var page []model.Entry
	require.NoError(t, handle.InReadTx(ctx, func(txCtx context.Context) error {
		var scanErr error
		page, _, _, scanErr = store.boundedHistoryBackward(
			txCtx,
			[]forkAncestor{{ConversationID: conversationID}},
			nil,
			true,
			10,
		)
		return scanErr
	}))
	require.Len(t, page, 10)
	assert.Equal(t, int64(11), materialized)
}
