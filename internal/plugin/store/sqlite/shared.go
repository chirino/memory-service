package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/txscope"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type sharedHandle struct {
	db      *gorm.DB
	sqlDB   *sql.DB
	writeMu sync.Mutex
}

type scope struct {
	db     *gorm.DB
	intent txscope.Intent
}

type scopeKey struct{}

var sharedHandles struct {
	sync.Mutex
	byDSN map[string]*sharedHandle
}

func getSharedHandle(ctx context.Context) (*sharedHandle, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("sqlite: missing config in context")
	}
	dsn := strings.TrimSpace(cfg.DBURL)
	if dsn == "" {
		return nil, fmt.Errorf("sqlite: db url is required")
	}

	sharedHandles.Lock()
	defer sharedHandles.Unlock()
	if sharedHandles.byDSN == nil {
		sharedHandles.byDSN = map[string]*sharedHandle{}
	}
	if handle := sharedHandles.byDSN[dsn]; handle != nil {
		return handle, nil
	}

	handle, err := openSharedHandle(cfg)
	if err != nil {
		return nil, err
	}
	sharedHandles.byDSN[dsn] = handle
	return handle, nil
}

// SharedDB exposes the process-wide SQLite GORM handle so sibling plugins can
// share a single opened database.
func SharedDB(ctx context.Context) (*gorm.DB, *sql.DB, error) {
	handle, err := getSharedHandle(ctx)
	if err != nil {
		return nil, nil, err
	}
	return handle.db, handle.sqlDB, nil
}

func openSharedHandle(cfg *config.Config) (*sharedHandle, error) {
	sqlitevec.Auto()

	db, err := gorm.Open(gormsqlite.Dialector{
		DriverName: "sqlite3",
		DSN:        sqliteRuntimeDSN(cfg.DBURL),
	}, &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		return nil, fmt.Errorf("sqlite: open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("sqlite: underlying db: %w", err)
	}

	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(4)

	return &sharedHandle{db: db, sqlDB: sqlDB}, nil
}

func sqliteRuntimeDSN(dsn string) string {
	base := dsn
	rawQuery := ""
	if idx := strings.IndexByte(dsn, '?'); idx >= 0 {
		base = dsn[:idx]
		rawQuery = dsn[idx+1:]
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		values = url.Values{}
	}
	setDefaultSQLiteParam(values, "_foreign_keys", "1")
	setDefaultSQLiteParam(values, "_journal_mode", "WAL")
	setDefaultSQLiteParam(values, "_busy_timeout", "5000")
	setDefaultSQLiteParam(values, "_synchronous", "NORMAL")

	encoded := values.Encode()
	if encoded == "" {
		return base
	}
	return base + "?" + encoded
}

func setDefaultSQLiteParam(values url.Values, key, value string) {
	if values.Get(key) != "" {
		return
	}
	values.Set(key, value)
}

func withScope(ctx context.Context, db *gorm.DB, intent txscope.Intent) context.Context {
	ctx = txscope.WithIntent(ctx, intent)
	return context.WithValue(ctx, scopeKey{}, &scope{db: db, intent: intent})
}

func scopeFromContext(ctx context.Context) (*scope, bool) {
	s, ok := ctx.Value(scopeKey{}).(*scope)
	return s, ok
}

func requireScope(ctx context.Context, op string) (*gorm.DB, error) {
	s, ok := scopeFromContext(ctx)
	if !ok || s == nil || s.db == nil {
		return nil, fmt.Errorf("sqlite: %s requires InReadTx or InWriteTx scope", op)
	}
	return s.db.WithContext(ctx), nil
}

func requireWriteScope(ctx context.Context, op string) (*gorm.DB, error) {
	s, ok := scopeFromContext(ctx)
	if !ok || s == nil || s.db == nil {
		return nil, fmt.Errorf("sqlite: %s requires InWriteTx scope", op)
	}
	if s.intent != txscope.IntentWrite {
		return nil, fmt.Errorf("sqlite: %s requires write scope", op)
	}
	return s.db.WithContext(ctx), nil
}

func (h *sharedHandle) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	if outer, ok := scopeFromContext(ctx); ok && outer != nil {
		return fn(ctx)
	}
	return h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(withScope(ctx, tx, txscope.IntentRead))
	}, &sql.TxOptions{ReadOnly: true})
}

func (h *sharedHandle) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	if outer, ok := scopeFromContext(ctx); ok && outer != nil {
		if outer.intent != txscope.IntentWrite {
			return fmt.Errorf("sqlite: cannot start write scope inside read scope")
		}
		return fn(ctx)
	}

	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	return h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(withScope(ctx, tx, txscope.IntentWrite))
	})
}
