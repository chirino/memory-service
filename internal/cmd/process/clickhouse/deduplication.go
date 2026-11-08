package clickhouse

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
)

var deduplicationWindowSetting = regexp.MustCompile(`\bnon_replicated_deduplication_window\s*=\s*([0-9]+)\b`)

// Apply table settings separately from schema DDL so existing databases retain
// their schema checksum and data when insert deduplication is enabled.
func (s *ClickHouseSink) configureInsertDeduplication(ctx context.Context, mode string) error {
	var defaultWindow uint64
	if err := s.conn.QueryRow(ctx, "SELECT toUInt64(value) FROM system.merge_tree_settings WHERE name='non_replicated_deduplication_window'").Scan(&defaultWindow); err != nil {
		return fmt.Errorf("read ClickHouse deduplication default: %w", err)
	}
	tables := []string{"lifecycle_events", "deletion_fences", "conversation_memberships", "projection_failures", "purge_queue"}
	projections, err := s.registeredProjections(ctx)
	if err != nil {
		return err
	}
	for _, projection := range projections {
		tables = append(tables, projection.TableName)
	}
	for _, table := range tables {
		var ddl string
		if err := s.conn.QueryRow(ctx, "SELECT create_table_query FROM system.tables WHERE database=? AND name=?", s.database, table).Scan(&ddl); err != nil {
			return fmt.Errorf("read ClickHouse table %s deduplication settings: %w", table, err)
		}
		window := defaultWindow
		if match := deduplicationWindowSetting.FindStringSubmatch(ddl); match != nil {
			window, err = strconv.ParseUint(match[1], 10, 64)
			if err != nil {
				return fmt.Errorf("read ClickHouse table %s deduplication window: %w", table, err)
			}
		}
		if window > 0 {
			continue
		}
		if mode == "validate" {
			return fmt.Errorf("ClickHouse table %s.%s has disabled insert deduplication; set non_replicated_deduplication_window to a positive value or use schema manage mode", s.database, table)
		}
		if err := s.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE %s.%s MODIFY SETTING non_replicated_deduplication_window=1000", quoteIdentifier(s.database), quoteIdentifier(table))); err != nil {
			return fmt.Errorf("enable ClickHouse table %s insert deduplication: %w", table, err)
		}
	}
	return nil
}
