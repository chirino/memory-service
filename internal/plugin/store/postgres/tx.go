//go:build !nopostgresql

package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/chirino/memory-service/internal/txscope"
	"gorm.io/gorm"
)

type scope struct {
	db     *gorm.DB
	intent txscope.Intent
}

type scopeKey struct{}

func withScope(ctx context.Context, db *gorm.DB, intent txscope.Intent) context.Context {
	ctx = txscope.WithIntent(ctx, intent)
	return context.WithValue(ctx, scopeKey{}, &scope{db: db, intent: intent})
}

func scopeFromContext(ctx context.Context) (*scope, bool) {
	s, ok := ctx.Value(scopeKey{}).(*scope)
	return s, ok
}

// dbFor returns the route-scoped transaction when one is active. Store code must
// use it (or writeDBFor) rather than s.db.WithContext(ctx); the base handle runs on
// a different transaction and misses uncommitted work such as sync auto-create rows.
func (s *PostgresStore) dbFor(ctx context.Context) *gorm.DB {
	if scoped, ok := scopeFromContext(ctx); ok && scoped != nil && scoped.db != nil {
		return scoped.db.WithContext(ctx)
	}
	return s.db.WithContext(ctx)
}

// writeDBFor is dbFor for writes; it rejects use inside a read-only scope.
func (s *PostgresStore) writeDBFor(ctx context.Context, op string) (*gorm.DB, error) {
	if scoped, ok := scopeFromContext(ctx); ok && scoped != nil && scoped.db != nil {
		if scoped.intent != txscope.IntentWrite {
			return nil, fmt.Errorf("postgres: %s requires write scope", op)
		}
		return scoped.db.WithContext(ctx), nil
	}
	return s.db.WithContext(ctx), nil
}

func (s *PostgresStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	if outer, ok := scopeFromContext(ctx); ok && outer != nil {
		return fn(ctx)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(withScope(ctx, tx, txscope.IntentRead))
	}, &sql.TxOptions{ReadOnly: true})
}

func (s *PostgresStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	if outer, ok := scopeFromContext(ctx); ok && outer != nil {
		if outer.intent != txscope.IntentWrite {
			return fmt.Errorf("postgres: cannot start write scope inside read scope")
		}
		return fn(ctx)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(withScope(ctx, tx, txscope.IntentWrite))
	})
}
