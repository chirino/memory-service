package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/txscope"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type sharedHandle struct {
	db          *gorm.DB
	sqlDB       *sql.DB
	writeMu     sync.Mutex
	fts5Enabled bool
	vecEnabled  bool
}

type Capabilities struct {
	FTS5Enabled bool
	VecEnabled  bool
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

func SharedCapabilities(ctx context.Context) (Capabilities, error) {
	handle, err := getSharedHandle(ctx)
	if err != nil {
		return Capabilities{}, err
	}
	return Capabilities{
		FTS5Enabled: handle.fts5Enabled,
		VecEnabled:  handle.vecEnabled,
	}, nil
}

func openSharedHandle(cfg *config.Config) (*sharedHandle, error) {
	if err := ensureSQLiteDBParentDir(cfg); err != nil {
		return nil, err
	}

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

	fts5Enabled, err := detectFTS5Support(context.Background(), sqlDB)
	if err != nil {
		return nil, fmt.Errorf("sqlite: detect fts5 support: %w", err)
	}
	if !fts5Enabled {
		log.Warn("SQLite FTS5 extension unavailable; full-text search disabled")
	}

	vecEnabled, err := detectVecSupport(context.Background(), sqlDB)
	if err != nil {
		return nil, fmt.Errorf("sqlite: detect vector support: %w", err)
	}
	if !vecEnabled && strings.EqualFold(strings.TrimSpace(cfg.VectorType), "sqlite") {
		log.Warn("SQLite vector extension unavailable; semantic/vector search disabled")
	}

	return &sharedHandle{
		db:          db,
		sqlDB:       sqlDB,
		fts5Enabled: fts5Enabled,
		vecEnabled:  vecEnabled,
	}, nil
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

func ensureSQLiteDBParentDir(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}

	dbPath, err := cfg.SQLiteFilePath()
	if err != nil {
		if strings.Contains(err.Error(), "not file-backed") {
			return nil
		}
		return fmt.Errorf("sqlite: resolve database path: %w", err)
	}

	parentDir := filepath.Dir(dbPath)
	if parentDir == "" || parentDir == "." {
		return nil
	}
	if err := os.MkdirAll(parentDir, 0o700); err != nil {
		return fmt.Errorf("sqlite: create database directory %q: %w", parentDir, err)
	}
	return nil
}

type sqliteExecContexter interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func detectFTS5Support(ctx context.Context, db sqliteExecContexter) (bool, error) {
	const probeTable = "__memory_service_fts5_probe"
	if _, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE temp."+probeTable+" USING fts5(content)"); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such module: fts5") {
			return false, nil
		}
		return false, err
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS temp."+probeTable); err != nil {
		return false, err
	}
	return true, nil
}

func detectVecSupport(ctx context.Context, db sqliteExecContexter) (bool, error) {
	const probeTable = "__memory_service_vec_probe"
	probeVector, err := sqlitevec.SerializeFloat32([]float32{0})
	if err != nil {
		return false, err
	}
	if _, err := db.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS "+probeTable+" (score REAL)"); err != nil {
		return false, err
	}
	defer func() {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS temp."+probeTable)
	}()
	if _, err := db.ExecContext(ctx, "DELETE FROM temp."+probeTable); err != nil {
		return false, err
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO temp."+probeTable+"(score) VALUES (vec_distance_cosine(?, ?))", probeVector, probeVector); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such function: vec_distance_cosine") {
			return false, nil
		}
		return false, err
	}
	return true, nil
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
