//go:build !nosqlite

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// BACKWARD COMPATIBILITY: remove in a future breaking release.
// Rebuild version-2 DATETIME tables without changing rowids, foreign keys, or
// nanoseconds. SQLite's date functions cannot preserve sub-millisecond precision.
func migrateSQLiteTimestamps(ctx context.Context, db *sql.DB) (returnErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var foreignKeys bool
	if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer func() {
		if foreignKeys {
			if _, err := conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`); err != nil && returnErr == nil {
				returnErr = err
			}
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type table struct{ name, schema string }
	rows, err := tx.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type = 'table' AND sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return err
	}
	var tables []table
	for rows.Next() {
		var t table
		if err := rows.Scan(&t.name, &t.schema); err != nil {
			rows.Close()
			return err
		}
		originalSchema := t.schema
		if t.name == "conversations" {
			t.schema = legacyStartedByForeignKey.ReplaceAllString(t.schema, "${1}")
		}
		if t.schema != originalSchema || legacyTimestampType.MatchString(t.schema) || strings.Contains(t.schema, "created_at_unix_ms") {
			tables = append(tables, t)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(tables) == 0 {
		return tx.Commit()
	}
	for _, t := range tables {
		if err := rebuildSQLiteTimestampTable(ctx, tx, t.name, t.schema); err != nil {
			return fmt.Errorf("migration: numeric timestamps in %s: %w", t.name, err)
		}
	}
	rows, err = tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	hasViolation := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if hasViolation {
		return fmt.Errorf("migration: numeric timestamp upgrade would violate foreign keys")
	}
	return tx.Commit()
}

// BACKWARD COMPATIBILITY: remove in a future breaking release.
var legacyTimestampType = regexp.MustCompile(`(?i)\b(DATETIME|TIMESTAMP|DATE)\b`)

// BACKWARD COMPATIBILITY: remove in a future breaking release.
// Historical schema-v2 lineage references must survive deletion of the parent.
// Remove the old inline cascade constraint during the same timestamp rebuild.
var legacyStartedByForeignKey = regexp.MustCompile(`(?i)(\bstarted_by_conversation_id\s+TEXT)\s+REFERENCES\s+conversations\s*\(\s*id\s*\)\s+ON\s+DELETE\s+CASCADE`)

// BACKWARD COMPATIBILITY: remove in a future breaking release.
func rebuildSQLiteTimestampTable(ctx context.Context, tx *sql.Tx, name, schema string) error {
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+quote(name)+`)`)
	if err != nil {
		return err
	}
	var columns, values []string
	convertedTimestamps := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var col, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &col, &typ, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if col == "created_at_unix_ms" {
			continue
		}
		columns = append(columns, quote(col))
		value := quote(col)
		if legacyTimestampType.MatchString(typ) {
			convertedTimestamps[col] = true
			values = append(values, "legacy_time_seconds("+value+")", "legacy_time_nanos("+value+")")
			columns = append(columns, quote(col+"_ns"))
			continue
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL ORDER BY type, name`, name)
	if err != nil {
		return err
	}
	var objects []string
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			rows.Close()
			return err
		}
		if !strings.Contains(statement, "created_at_unix_ms") {
			objects = append(objects, statement)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	// AUTOINCREMENT must not reuse event sequences already evicted from the outbox.
	var sequence sql.NullInt64
	if strings.Contains(strings.ToUpper(schema), "AUTOINCREMENT") {
		if err := tx.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = ?`, name).Scan(&sequence); err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	// Defaults retain the historical whole-second precision.
	schema = regexp.MustCompile(`(?i)\b(\w+_at)\s+(?:DATETIME|TIMESTAMP|DATE)(\s+NOT NULL)?(\s+DEFAULT CURRENT_TIMESTAMP)?`).ReplaceAllStringFunc(schema, func(column string) string {
		match := regexp.MustCompile(`(?i)^(\w+_at)\s+(?:DATETIME|TIMESTAMP|DATE)(\s+NOT NULL)?(\s+DEFAULT CURRENT_TIMESTAMP)?$`).FindStringSubmatch(column)
		col := match[1]
		rest := ""
		if match[2] != "" {
			rest = " NOT NULL"
		}
		if match[3] != "" {
			rest += " DEFAULT (unixepoch())"
		}
		nsRest := ""
		if match[2] != "" {
			nsRest = " NOT NULL"
		}
		if match[3] != "" {
			nsRest += " DEFAULT 0"
		}
		return col + " INTEGER_SECONDS CHECK (" + col + " IS NULL OR typeof(" + col + ") = 'integer')" + rest + ", " + col + "_ns INTEGER CHECK ((" + col + " IS NULL AND " + col + "_ns IS NULL) OR (" + col + " IS NOT NULL AND " + col + "_ns IS NOT NULL AND typeof(" + col + "_ns) = 'integer' AND " + col + "_ns BETWEEN 0 AND 999999999))" + nsRest
	})
	schema = regexp.MustCompile(`(?i),\s*created_at_unix_ms\s+INTEGER`).ReplaceAllString(schema, "")
	schema = "CREATE TABLE __numeric_timestamp_upgrade " + schema[strings.IndexByte(schema, '('):]
	// Keep the hidden rowid used by the external-content FTS index.
	columns = append([]string{"rowid"}, columns...)
	values = append([]string{"rowid"}, values...)
	for _, statement := range []string{
		schema,
		`INSERT INTO __numeric_timestamp_upgrade (` + strings.Join(columns, ",") + `) SELECT ` + strings.Join(values, ",") + ` FROM ` + quote(name),
		`DROP TABLE ` + quote(name),
		`ALTER TABLE __numeric_timestamp_upgrade RENAME TO ` + quote(name),
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if sequence.Valid {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name = ?`, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sqlite_sequence(name,seq) VALUES (?,?)`, name, sequence.Int64); err != nil {
			return err
		}
	}
	for _, statement := range objects {
		if strings.HasPrefix(strings.ToUpper(statement), "CREATE INDEX") || strings.HasPrefix(strings.ToUpper(statement), "CREATE UNIQUE INDEX") {
			open := strings.IndexByte(statement, '(')
			close := strings.IndexByte(statement, ')')
			if open >= 0 && close > open {
				pattern := regexp.MustCompile(`\b(\w+_at)(?:\s+(ASC|DESC))?\b`)
				columns := pattern.ReplaceAllStringFunc(statement[open+1:close], func(column string) string {
					match := pattern.FindStringSubmatch(column)
					if !convertedTimestamps[match[1]] {
						return column
					}
					return match[1] + " " + match[2] + ", " + match[1] + "_ns " + match[2]
				})
				statement = statement[:open+1] + columns + statement[close:]
			}
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
