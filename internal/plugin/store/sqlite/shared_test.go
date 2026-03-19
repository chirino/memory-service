//go:build !nosqlite

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSQLiteRuntimeDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
		want []string
	}{
		{
			name: "plain filename",
			dsn:  "/tmp/memory.db",
			want: []string{
				"/tmp/memory.db?",
				"_busy_timeout=5000",
				"_foreign_keys=1",
				"_journal_mode=WAL",
				"_synchronous=NORMAL",
			},
		},
		{
			name: "preserves existing query",
			dsn:  "file:/tmp/memory.db?cache=shared",
			want: []string{
				"file:/tmp/memory.db?",
				"cache=shared",
				"_busy_timeout=5000",
				"_foreign_keys=1",
				"_journal_mode=WAL",
				"_synchronous=NORMAL",
			},
		},
		{
			name: "does not overwrite explicit settings",
			dsn:  "file:/tmp/memory.db?_busy_timeout=10&_foreign_keys=0&_journal_mode=MEMORY&_synchronous=FULL",
			want: []string{
				"_busy_timeout=10",
				"_foreign_keys=0",
				"_journal_mode=MEMORY",
				"_synchronous=FULL",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sqliteRuntimeDSN(tt.dsn)
			for _, fragment := range tt.want {
				if !strings.Contains(got, fragment) {
					t.Fatalf("sqliteRuntimeDSN(%q) = %q, want fragment %q", tt.dsn, got, fragment)
				}
			}
		})
	}
}

func TestEnsureSQLiteDBParentDir(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "nested", "deeper", "memory.db")
	cfg := &config.Config{DBURL: dbPath}

	require.NoError(t, ensureSQLiteDBParentDir(cfg))

	info, err := os.Stat(filepath.Dir(dbPath))
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestDetectFTS5SupportTreatsMissingModuleAsDisabled(t *testing.T) {
	t.Parallel()

	exec := &recordingExec{createErr: errors.New("no such module: fts5")}

	enabled, err := detectFTS5Support(context.Background(), exec)
	require.NoError(t, err)
	require.False(t, enabled)
	require.Len(t, exec.queries, 1)
}

func TestDetectFTS5SupportPropagatesUnexpectedProbeErrors(t *testing.T) {
	t.Parallel()

	exec := &recordingExec{createErr: errors.New("disk I/O error")}

	enabled, err := detectFTS5Support(context.Background(), exec)
	require.Error(t, err)
	require.False(t, enabled)
}

func TestDetectVecSupportTreatsMissingFunctionAsDisabled(t *testing.T) {
	t.Parallel()

	exec := &recordingExec{insertErr: errors.New("no such function: vec_distance_cosine")}

	enabled, err := detectVecSupport(context.Background(), exec)
	require.NoError(t, err)
	require.False(t, enabled)
	require.NotEmpty(t, exec.queries)
}

func TestDetectVecSupportPropagatesUnexpectedProbeErrors(t *testing.T) {
	t.Parallel()

	exec := &recordingExec{insertErr: errors.New("disk I/O error")}

	enabled, err := detectVecSupport(context.Background(), exec)
	require.Error(t, err)
	require.False(t, enabled)
}

type recordingExec struct {
	queries   []string
	createErr error
	insertErr error
}

func (r *recordingExec) ExecContext(_ context.Context, query string, _ ...interface{}) (sql.Result, error) {
	r.queries = append(r.queries, query)
	if strings.Contains(query, "CREATE VIRTUAL TABLE") && r.createErr != nil {
		return nil, r.createErr
	}
	if strings.Contains(query, "vec_distance_cosine") && r.insertErr != nil {
		return nil, r.insertErr
	}
	return nil, nil
}
