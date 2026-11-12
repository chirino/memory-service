package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// Validate existing objects before manage mode executes any DDL. The fingerprint
// identifies this baseline; physical checks also catch accidental/operator drift.
func (s *ClickHouseSink) validateInfrastructure(ctx context.Context, allowMissing bool) error {
	for _, ddl := range schemaStatements(s.database)[1:] {
		rest := strings.TrimPrefix(ddl, "CREATE TABLE IF NOT EXISTS "+quoteIdentifier(s.database)+".")
		view := rest == ddl
		if view {
			rest = strings.TrimPrefix(ddl, "CREATE OR REPLACE VIEW "+quoteIdentifier(s.database)+".")
		}
		name, _, _ := strings.Cut(rest, " ")
		var count uint64
		var engine, full, key, partition string
		if err := s.conn.QueryRow(ctx, "SELECT count(),any(engine),any(engine_full),any(sorting_key),any(partition_key) FROM system.tables WHERE database=? AND name=?", s.database, name).Scan(&count, &engine, &full, &key, &partition); err != nil {
			return err
		}
		if count == 0 && allowMissing {
			continue
		}
		if count != 1 {
			return fmt.Errorf("required analytics relation %s is missing", name)
		}
		if view {
			if engine != "View" {
				return fmt.Errorf("analytics relation %s must be a view", name)
			}
			if err := s.validateView(ctx, name, ddl); err != nil {
				return err
			}
			continue
		}
		definitions, tail, _ := strings.Cut(rest[strings.Index(rest, "(")+1:], ") ENGINE=")
		expectedEngine, tail, _ := strings.Cut(tail, " ")
		expectedPartition := ""
		if strings.HasPrefix(tail, "PARTITION BY ") {
			expectedPartition, tail, _ = strings.Cut(strings.TrimPrefix(tail, "PARTITION BY "), "ORDER BY ")
		} else {
			tail = strings.TrimPrefix(tail, "ORDER BY ")
		}
		if len(strings.Fields(full)) == 0 || compactSQL(strings.Fields(full)[0]) != compactSQL(expectedEngine) {
			return fmt.Errorf("analytics table %s has incompatible engine", name)
		}
		if compactSQL(key) != compactSQL(strings.Trim(strings.TrimSpace(tail), "()")) || compactSQL(partition) != compactSQL(expectedPartition) {
			return fmt.Errorf("analytics table %s has incompatible sorting or partition key", name)
		}
		columns := map[string]string{}
		for _, definition := range splitSQLColumns(definitions) {
			field, kind, _ := strings.Cut(strings.TrimSpace(definition), " ")
			columns[field] = strings.TrimSpace(kind)
		}
		rows, err := s.conn.Query(ctx, "SELECT name,type FROM system.columns WHERE database=? AND table=?", s.database, name)
		if err != nil {
			return err
		}
		actual := map[string]string{}
		for rows.Next() {
			var column, kind string
			if err := rows.Scan(&column, &kind); err != nil {
				_ = rows.Close()
				return err
			}
			actual[column] = kind
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		if len(actual) != len(columns) {
			return fmt.Errorf("analytics table %s has incompatible columns", name)
		}
		for column, kind := range columns {
			canonical, err := s.canonicalClickHouseType(ctx, kind)
			if err != nil {
				return err
			}
			if actual[column] != canonical {
				return fmt.Errorf("analytics table %s column %s has incompatible type", name, column)
			}
		}
	}
	return nil
}
func compactSQL(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '`' {
			return -1
		}
		return r
	}, value)
}
func splitSQLColumns(value string) []string {
	var columns []string
	depth, start := 0, 0
	for i, r := range value {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				columns = append(columns, value[start:i])
				start = i + 1
			}
		}
	}
	return append(columns, value[start:])
}

func (s *ClickHouseSink) validateView(ctx context.Context, name, ddl string) error {
	var actual string
	if err := s.conn.QueryRow(ctx, "SELECT as_select FROM system.tables WHERE database=? AND name=?", s.database, name).Scan(&actual); err != nil {
		return err
	}
	_, expected, ok := strings.Cut(ddl, " AS")
	if !ok {
		return fmt.Errorf("invalid view declaration %s", name)
	}
	expectedAST, err := s.queryAST(ctx, strings.TrimSpace(expected))
	if err != nil {
		return err
	}
	actualAST, err := s.queryAST(ctx, actual)
	if err != nil {
		return err
	}
	if expectedAST != actualAST {
		return fmt.Errorf("analytics view %s has incompatible definition", name)
	}
	return nil
}
func (s *ClickHouseSink) queryAST(ctx context.Context, query string) (string, error) {
	rows, err := s.conn.Query(ctx, "EXPLAIN AST "+query)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), rows.Err()
}
