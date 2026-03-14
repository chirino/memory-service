package sqlite

import (
	"strings"
	"testing"
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
