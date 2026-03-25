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

func (s *PostgresStore) dbFor(ctx context.Context) *gorm.DB {
	if scoped, ok := scopeFromContext(ctx); ok && scoped != nil && scoped.db != nil {
		return scoped.db.WithContext(ctx)
	}
	return s.db.WithContext(ctx)
}

func (s *PostgresStore) writeDBFor(ctx context.Context, op string) (*gorm.DB, error) {
	if scoped, ok := scopeFromContext(ctx); ok && scoped != nil && scoped.db != nil {
		if scoped.intent != txscope.IntentWrite {
			return nil, fmt.Errorf("postgres: %s requires write scope", op)
		}
		return scoped.db.WithContext(ctx), nil
	}
	return s.db.WithContext(ctx), nil
}

func (s *PostgresStore) txFor(ctx context.Context) *gorm.DB {
	return s.dbFor(ctx)
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
