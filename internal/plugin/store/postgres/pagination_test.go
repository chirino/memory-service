package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pgdriver "gorm.io/driver/postgres"
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

func TestVisibleHistoryQueryUsesConversationAncestryJoin(t *testing.T) {
	dsn := testpg.StartPostgres(t)
	db, err := gorm.Open(pgdriver.New(pgdriver.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true,
	}), &gorm.Config{DryRun: true})
	require.NoError(t, err)

	store := &PostgresStore{db: db}
	conv := model.Conversation{
		ID:                  "conversation-a",
		ConversationGroupID: uuid.MustParse("00000000-0000-0000-0000-000000000371"),
	}
	var entries []model.Entry
	tx := store.visibleHistoryEntriesQuery(context.Background(), conv).
		Order("e.created_at ASC, e.seq ASC NULLS FIRST, e.id ASC").
		Limit(11).
		Find(&entries)
	require.NoError(t, tx.Error)

	sql := strings.ToLower(tx.Statement.SQL.String())
	assert.Contains(t, sql, "conversation_ancestry")
	assert.Contains(t, sql, "descendant_conversation_id")
	assert.Contains(t, sql, "ancestor_conversation_id = e.conversation_id")
	assert.Contains(t, sql, "limit")
}
