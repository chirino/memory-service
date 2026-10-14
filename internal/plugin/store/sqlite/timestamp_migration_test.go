//go:build !nosqlite

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

// Inventory includes the vector plugin's table because it shares the core DB.
var timestampInventory = map[string][]string{
	"schema_metadata":                  {"updated_at"},
	"conversation_groups":              {"created_at", "archived_at"},
	"conversations":                    {"created_at", "updated_at", "vectorized_at", "archived_at"},
	"conversation_memberships":         {"created_at"},
	"entries":                          {"indexed_at", "created_at"},
	"conversation_ownership_transfers": {"created_at"},
	"tasks":                            {"created_at", "retry_at"},
	"admin_checkpoints":                {"lease_expires_at", "updated_at"},
	"outbox_events":                    {"created_at"},
	"attachments":                      {"expires_at", "created_at", "archived_at"},
	"memory_kind_versions":             {"created_at"},
	"memory_kind_migrations":           {"created_at", "started_at", "completed_at"},
	"memories":                         {"created_at", "expires_at", "archived_at", "indexed_at"},
	"memory_usage_stats":               {"last_fetched_at"},
	"entry_embeddings":                 {"created_at"},
}

func TestSQLiteTimestampUpgradePreservesAllColumns(t *testing.T) {
	for _, nulls := range []bool{false, true} {
		t.Run(fmt.Sprintf("nullable=%t", nulls), func(t *testing.T) {
			cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "upgrade.db"), DatastoreMigrateAtStart: true}
			ctx := config.WithContext(context.Background(), cfg)
			legacy, err := sql.Open("sqlite3", cfg.DBURL)
			require.NoError(t, err)
			legacy.SetMaxOpenConns(1)
			defer legacy.Close()
			_, err = legacy.Exec(legacyTextSchemaSQL)
			require.NoError(t, err)
			_, err = legacy.Exec(`
				CREATE TABLE entry_embeddings(entry_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, conversation_group_id TEXT NOT NULL, embedding BLOB NOT NULL, model TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
				INSERT INTO conversation_groups(id) VALUES ('g');
				INSERT INTO conversations(id,owner_user_id,client_id,conversation_group_id) VALUES ('c','alice','client','g');
				INSERT INTO conversation_ancestry(conversation_group_id,descendant_conversation_id,ancestor_conversation_id,depth) VALUES ('g','c','c',0);
				INSERT INTO conversation_memberships(conversation_group_id,user_id,access_level) VALUES ('g','alice','owner');
				INSERT INTO entries(rowid,id,conversation_id,conversation_group_id,channel,content_type,content,indexed_content,created_at_unix_ms) VALUES (42,'e','c','g','history','history',X'123456','searchable',0);
				INSERT INTO conversation_ownership_transfers(id,conversation_group_id,from_user_id,to_user_id) VALUES ('transfer','g','alice','bob');
				INSERT INTO tasks(id,task_type,task_body) VALUES ('task','test','{}');
				INSERT INTO admin_checkpoints(client_id,content_type,value) VALUES ('client','test',X'abcdef');
				INSERT INTO outbox_events(seq,event,kind,data) VALUES (1,'first','test','{}'),(100,'last','test','{}');
				DELETE FROM outbox_events WHERE seq=100;
				INSERT INTO attachments(id,content_type,user_id) VALUES ('attachment','text/plain','alice');
				INSERT INTO memory_kind_versions(name) VALUES ('default/v1'),('other/v1');
				INSERT INTO memory_kind_migrations(id,source,target,state) VALUES ('migration','default/v1','other/v1','queued');
				INSERT INTO memories(id,namespace,key,value_encrypted) VALUES ('memory','ns','key',X'123456');
				INSERT INTO memory_usage_stats(namespace,key) VALUES ('ns','key');
				INSERT INTO entry_embeddings(entry_id,conversation_id,conversation_group_id,embedding,model) VALUES ('e','c','g',X'123456','test');
				CREATE INDEX idx_entries_created_at_unix_ms ON entries(created_at_unix_ms);
				CREATE INDEX idx_entries_group_created_at_unix_ms ON entries(conversation_group_id,created_at_unix_ms);
			`)
			require.NoError(t, err)
			// Build the old FTS index before migration, preserving its rowid references.
			_, ftsErr := legacy.Exec(ftsSchemaSQL)
			if ftsErr == nil {
				_, err = legacy.Exec(`INSERT INTO entries_fts(entries_fts) VALUES ('rebuild')`)
				require.NoError(t, err)
			} else {
				require.Contains(t, ftsErr.Error(), "no such module: fts5")
			}
			instant := time.Date(2026, 1, 2, 3, 4, 5, 987654321, time.FixedZone("west", -25200))
			expected := map[string]map[string]*time.Time{}
			for table, cols := range timestampInventory {
				expected[table] = map[string]*time.Time{}
				for _, col := range cols {
					var notNull int
					require.NoError(t, legacy.QueryRow(`SELECT "notnull" FROM pragma_table_info(?) WHERE name=?`, table, col).Scan(&notNull))
					var value any = instant
					if nulls && notNull == 0 {
						value = nil
						expected[table][col] = nil
					} else {
						expected[table][col] = &instant
					}
					_, err := legacy.Exec(fmt.Sprintf(`UPDATE %s SET %s=?`, table, col), value)
					require.NoError(t, err)
				}
			}
			require.NoError(t, legacy.Close())
			for iteration := 0; iteration < 2; iteration++ {
				require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
				_, db, err := SharedDB(ctx)
				require.NoError(t, err)
				for table, cols := range expected {
					var count int
					require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&count))
					if table == "memory_kind_versions" {
						require.Equal(t, 2, count)
					} else {
						require.Equal(t, 1, count)
					}
					for col, want := range cols {
						var typ string
						require.NoError(t, db.QueryRow(`SELECT type FROM pragma_table_info(?) WHERE name=?`, table, col).Scan(&typ))
						require.Equal(t, "INTEGER_SECONDS", typ, table+"."+col)
						var got *time.Time
						var raw, nanos sql.NullInt64
						require.NoError(t, db.QueryRow(fmt.Sprintf(`SELECT %s,%s_ns,CAST(%s AS INTEGER) AS raw_seconds,CAST(%s_ns AS INTEGER) AS raw_nanos FROM %s LIMIT 1`, col, col, col, col, table)).Scan(&got, &raw, &nanos))
						// The schema version upsert intentionally advances its updated_at value.
						if table == "schema_metadata" {
							require.NotNil(t, got)
							continue
						}
						if want == nil {
							require.Nil(t, got)
							require.False(t, raw.Valid)
						} else {
							require.NotNil(t, got)
							require.True(t, want.Equal(*got), table+"."+col)
							require.Equal(t, want.Unix(), raw.Int64)
							require.Equal(t, int64(want.Nanosecond()), nanos.Int64)
						}
						// Reject text dates at the DB boundary on both upgraded and fresh layouts.
						_, err = db.Exec(fmt.Sprintf(`UPDATE %s SET %s='2026-01-01'`, table, col))
						require.Error(t, err, table+"."+col)
					}
				}
				var obsolete int
				require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name='created_at_unix_ms'`).Scan(&obsolete))
				require.Zero(t, obsolete)
				var rowid int
				var payload []byte
				require.NoError(t, db.QueryRow(`SELECT rowid,content FROM entries WHERE id='e'`).Scan(&rowid, &payload))
				require.Equal(t, 42, rowid)
				require.Equal(t, []byte{0x12, 0x34, 0x56}, payload)
				if ftsErr == nil {
					var matches int
					require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM entries_fts WHERE entries_fts MATCH 'searchable'`).Scan(&matches))
					require.Equal(t, 1, matches)
				}
			}
			_, db, err := SharedDB(ctx)
			require.NoError(t, err)
			result, err := db.Exec(`INSERT INTO outbox_events(event,kind,data) VALUES ('next','test','{}')`)
			require.NoError(t, err)
			seq, err := result.LastInsertId()
			require.NoError(t, err)
			require.Equal(t, int64(101), seq)
			if ftsErr == nil {
				_, err = db.Exec(`UPDATE entries SET indexed_content='changed' WHERE id='e'`)
				require.NoError(t, err)
				var matches int
				require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM entries_fts WHERE entries_fts MATCH 'changed'`).Scan(&matches))
				require.Equal(t, 1, matches)
			}
		})
	}
}

func TestSQLiteTimestampMigrationRollsBackInvalidDates(t *testing.T) {
	for _, invalid := range []string{"not-a-date"} {
		t.Run(invalid, func(t *testing.T) {
			cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "invalid.db"), DatastoreMigrateAtStart: true}
			ctx := config.WithContext(context.Background(), cfg)
			legacy, err := sql.Open("sqlite3", cfg.DBURL)
			require.NoError(t, err)
			_, err = legacy.Exec(legacyTextSchemaSQL)
			require.NoError(t, err)
			_, err = legacy.Exec(`INSERT INTO attachments(id,content_type,user_id,created_at) VALUES ('a','text/plain','alice',?)`, invalid)
			require.NoError(t, err)
			require.NoError(t, legacy.Close())
			require.Error(t, (&sqliteMigrator{}).Migrate(ctx))
			_, db, err := SharedDB(ctx)
			require.NoError(t, err)
			var original string
			require.NoError(t, db.QueryRow(`SELECT CAST(created_at AS TEXT) FROM attachments`).Scan(&original))
			require.Equal(t, invalid, original)
			var remaining int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='__numeric_timestamp_upgrade'`).Scan(&remaining))
			require.Zero(t, remaining)
			var foreignKeys bool
			require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys))
			require.True(t, foreignKeys)
		})
	}
}

func TestSQLiteFreshSchemaUsesOnlyNumericTimestamps(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "fresh.db"), DatastoreMigrateAtStart: true}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	_, db, err := SharedDB(ctx)
	require.NoError(t, err)
	rows, err := db.Query(`SELECT m.name,p.name,p.type FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type='table' AND p.name LIKE '%_at'`)
	require.NoError(t, err)
	defer rows.Close()
	count := 0
	for rows.Next() {
		var table, col, typ string
		require.NoError(t, rows.Scan(&table, &col, &typ))
		require.Equal(t, "INTEGER_SECONDS", typ, table+"."+col)
		require.Contains(t, timestampInventory[table], col)
		count++
	}
	require.NoError(t, rows.Err())
	expected := 0
	for table, cols := range timestampInventory {
		if table != "entry_embeddings" {
			expected += len(cols)
		}
	}
	require.Equal(t, expected, count)
	require.NotContains(t, strings.ToUpper(schemaSQL), "DATETIME")
}

func TestSQLiteTimestampUpgradePreservesEmptyOutboxSequence(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "empty.db"), DatastoreMigrateAtStart: true}
	ctx := config.WithContext(context.Background(), cfg)
	legacy, err := sql.Open("sqlite3", cfg.DBURL)
	require.NoError(t, err)
	_, err = legacy.Exec(legacyTextSchemaSQL)
	require.NoError(t, err)
	_, err = legacy.Exec(`INSERT INTO outbox_events(seq,event,kind,data) VALUES (100,'last','test','{}'); DELETE FROM outbox_events;`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	_, db, err := SharedDB(ctx)
	require.NoError(t, err)
	result, err := db.Exec(`INSERT INTO outbox_events(event,kind,data) VALUES ('next','test','{}')`)
	require.NoError(t, err)
	seq, err := result.LastInsertId()
	require.NoError(t, err)
	require.Equal(t, int64(101), seq)
}

func TestSQLiteTimestampUpgradePreservesValidLongTTL(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "long-ttl.db"), DatastoreMigrateAtStart: true}
	ctx := config.WithContext(context.Background(), cfg)
	legacy, err := sql.Open("sqlite3", cfg.DBURL)
	require.NoError(t, err)
	_, err = legacy.Exec(legacyTextSchemaSQL)
	require.NoError(t, err)
	created := time.Date(2026, 10, 13, 12, 0, 0, 123456789, time.UTC)
	_, err = legacy.Exec(`INSERT INTO memory_kind_versions(name) VALUES ('default/v1')`)
	require.NoError(t, err)
	const ttlSeconds int64 = 8000000000
	expires := created.Add(time.Duration(ttlSeconds) * time.Second)
	require.Greater(t, expires.Year(), 2262)
	payload := []byte{0x12, 0x34, 0x56}
	_, err = legacy.Exec(`INSERT INTO memories(id,namespace,key,value_encrypted,created_at,expires_at) VALUES ('memory','ns','key',?,?,?)`, payload, created, expires)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())
	for iteration := 0; iteration < 2; iteration++ {
		require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
		_, db, err := SharedDB(ctx)
		require.NoError(t, err)
		var gotCreated, gotExpires time.Time
		var gotPayload []byte
		require.NoError(t, db.QueryRow(`SELECT created_at,created_at_ns,expires_at,expires_at_ns,value_encrypted FROM memories WHERE id='memory'`).Scan(&gotCreated, &gotExpires, &gotPayload))
		require.True(t, created.Equal(gotCreated))
		require.True(t, expires.Equal(gotExpires))
		require.Equal(t, payload, gotPayload)
	}
}

func TestSQLiteLineageAndTimestampUpgradePreservesConversation(t *testing.T) {
	for _, numeric := range []bool{false, true} {
		t.Run(fmt.Sprintf("numeric=%t", numeric), func(t *testing.T) {
			cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "lineage.db"), DatastoreMigrateAtStart: true}
			ctx := config.WithContext(context.Background(), cfg)
			legacy, err := sql.Open("sqlite3", cfg.DBURL)
			require.NoError(t, err)
			legacy.SetMaxOpenConns(1)
			defer legacy.Close()
			schema := legacyTextSchemaSQL
			if numeric {
				schema = schemaSQL
			}
			lineageSchema := strings.Replace(schema, "started_by_conversation_id TEXT,", "started_by_conversation_id TEXT REFERENCES conversations(id) ON DELETE CASCADE,", 1)
			require.NotEqual(t, schema, lineageSchema)
			_, err = legacy.Exec(lineageSchema)
			require.NoError(t, err)
			_, err = legacy.Exec(`
				ALTER TABLE conversations ADD COLUMN extra_payload BLOB;
				INSERT INTO conversation_groups(id) VALUES ('parent-group'),('child-group');
				INSERT INTO conversations(id,owner_user_id,client_id,conversation_group_id) VALUES ('parent','alice','client','parent-group');
				INSERT INTO conversations(rowid,id,owner_user_id,client_id,conversation_group_id,started_by_conversation_id,extra_payload) VALUES (42,'child','alice','client','child-group','parent',X'123456');
				CREATE TABLE conversation_audit(title BLOB);
				CREATE TRIGGER conversation_title_audit AFTER UPDATE OF title ON conversations BEGIN INSERT INTO conversation_audit(title) VALUES(new.title); END;
			`)
			require.NoError(t, err)
			indexColumns := "created_at DESC"
			if numeric {
				indexColumns += ",created_at_ns DESC"
			}
			_, err = legacy.Exec("CREATE INDEX custom_conversation_time ON conversations(" + indexColumns + ")")
			require.NoError(t, err)
			instant := time.Date(2280, 4, 17, 2, 13, 20, 123456789, time.FixedZone("east", 19800))
			if numeric {
				_, err = legacy.Exec(`UPDATE conversations SET created_at=?,created_at_ns=?,updated_at=?,updated_at_ns=?,vectorized_at=?,vectorized_at_ns=? WHERE id='child'`, instant.Unix(), instant.Nanosecond(), instant.Unix(), instant.Nanosecond(), instant.Unix(), instant.Nanosecond())
			} else {
				_, err = legacy.Exec(`UPDATE conversations SET created_at=?,updated_at=?,vectorized_at=? WHERE id='child'`, instant, instant, instant)
			}
			require.NoError(t, err)
			require.NoError(t, legacy.Close())
			for iteration := 0; iteration < 2; iteration++ {
				require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
				_, db, err := SharedDB(ctx)
				require.NoError(t, err)
				var rowid int
				var payload []byte
				var created, updated, vectorized time.Time
				var archived *time.Time
				require.NoError(t, db.QueryRow(`SELECT rowid,extra_payload,created_at,created_at_ns,updated_at,updated_at_ns,vectorized_at,vectorized_at_ns,archived_at,archived_at_ns FROM conversations WHERE id='child'`).Scan(&rowid, &payload, &created, &updated, &vectorized, &archived))
				require.Equal(t, 42, rowid)
				require.Equal(t, []byte{0x12, 0x34, 0x56}, payload)
				for _, got := range []time.Time{created, updated, vectorized} {
					require.True(t, instant.Equal(got))
				}
				require.Nil(t, archived)
				var fkCount int
				require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list('conversations') WHERE "from"='started_by_conversation_id'`).Scan(&fkCount))
				require.Zero(t, fkCount)
				var indexColumns int
				require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_index_info('custom_conversation_time')`).Scan(&indexColumns))
				require.Equal(t, 2, indexColumns)
			}
			_, db, err := SharedDB(ctx)
			require.NoError(t, err)
			_, err = db.Exec(`UPDATE conversations SET title=X'abcdef' WHERE id='child'; DELETE FROM conversation_groups WHERE id='parent-group'`)
			require.NoError(t, err)
			var title []byte
			require.NoError(t, db.QueryRow(`SELECT title FROM conversation_audit`).Scan(&title))
			require.Equal(t, []byte{0xab, 0xcd, 0xef}, title)
			var parent string
			require.NoError(t, db.QueryRow(`SELECT started_by_conversation_id FROM conversations WHERE id='child'`).Scan(&parent))
			require.Equal(t, "parent", parent)
		})
	}
}

func TestSQLiteLineageAndTimestampUpgradeRollBackTogether(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "invalid-lineage.db"), DatastoreMigrateAtStart: true}
	ctx := config.WithContext(context.Background(), cfg)
	legacy, err := sql.Open("sqlite3", cfg.DBURL)
	require.NoError(t, err)
	defer legacy.Close()
	schema := strings.Replace(legacyTextSchemaSQL, "started_by_conversation_id TEXT,", "started_by_conversation_id TEXT REFERENCES conversations(id) ON DELETE CASCADE,", 1)
	_, err = legacy.Exec(schema)
	require.NoError(t, err)
	_, err = legacy.Exec(`
		INSERT INTO conversation_groups(id) VALUES ('g');
		INSERT INTO conversations(rowid,id,owner_user_id,client_id,conversation_group_id) VALUES (42,'c','alice','client','g');
		INSERT INTO memory_kind_versions(name) VALUES ('default/v1');
		INSERT INTO memories(id,namespace,key,value_encrypted,created_at) VALUES ('m','ns','key',X'123456','invalid-date');
	`)
	require.NoError(t, err)
	var before string
	require.NoError(t, legacy.QueryRow(`SELECT sql FROM sqlite_master WHERE name='conversations'`).Scan(&before))
	require.NoError(t, legacy.Close())
	require.ErrorContains(t, (&sqliteMigrator{}).Migrate(ctx), "invalid legacy SQLite timestamp")
	_, db, err := SharedDB(ctx)
	require.NoError(t, err)
	var after string
	require.NoError(t, db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='conversations'`).Scan(&after))
	require.Equal(t, before, after)
	var rowid int
	require.NoError(t, db.QueryRow(`SELECT rowid FROM conversations WHERE id='c'`).Scan(&rowid))
	require.Equal(t, 42, rowid)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list('conversations') WHERE "from"='started_by_conversation_id'`).Scan(&count))
	require.Equal(t, 1, count)
}
