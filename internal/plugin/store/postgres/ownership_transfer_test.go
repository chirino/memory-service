//go:build !nopostgresql

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A store integration test can hold the exact row locks needed to reproduce this
// race; the HTTP BDD harness cannot coordinate concurrent requests at that point.
func TestConcurrentOwnershipAcceptPublishesOnce(t *testing.T) {
	s, baseCtx := setupPostgresOutboxStore(t)
	ctx, cancel := context.WithTimeout(baseCtx, 15*time.Second)
	defer cancel()
	conv, err := s.CreateConversation(ctx, "from", "app", "transfer", nil, nil, nil, nil)
	require.NoError(t, err)
	_, err = s.ShareConversation(ctx, "from", conv.ID, "to", model.AccessLevelReader)
	require.NoError(t, err)
	transfer, err := s.CreateOwnershipTransfer(ctx, "from", conv.ID, "to")
	require.NoError(t, err)
	held := s.db.WithContext(ctx).Begin()
	require.NoError(t, held.Error)
	defer held.Rollback()
	var owner model.ConversationMembership
	require.NoError(t, held.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_group_id=? AND user_id=?", conv.ConversationGroupID, "from").Take(&owner).Error)
	queried := make(chan struct{}, 2)
	require.NoError(t, s.db.Callback().Query().After("gorm:query").Register("test:transfer_loaded", func(tx *gorm.DB) {
		if tx.Statement.Table == (model.OwnershipTransfer{}).TableName() && tx.RowsAffected > 0 {
			queried <- struct{}{}
		}
	}))
	defer s.db.Callback().Query().Remove("test:transfer_loaded")
	results := make(chan error, 2)
	accept := func() {
		results <- s.InWriteTx(ctx, func(tx context.Context) error { return s.AcceptTransfer(tx, "to", transfer.ID) })
	}
	go accept()
	select {
	case <-queried:
	case <-ctx.Done():
		t.Fatal("first accept did not load the transfer")
	}
	go accept()
	secondLoaded := false
	select {
	case <-queried:
		secondLoaded = true
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, held.Rollback().Error)
	var successes, notFound int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else {
			var missing *registrystore.NotFoundError
			require.True(t, errors.As(err, &missing), "%v", err)
			notFound++
		}
	}
	require.False(t, secondLoaded, "the second accept must wait for the transfer's row lock")
	require.Equal(t, 1, successes)
	require.Equal(t, 1, notFound)
	var events int64
	require.NoError(t, s.db.Table("outbox_events").Where("kind='membership' AND event='updated'").Count(&events).Error)
	require.EqualValues(t, 2, events)
}
