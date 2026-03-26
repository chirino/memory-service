package bdd

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	_ "github.com/mattn/go-sqlite3"
)

// SQLiteTestDB implements cucumber.TestDB for SQLite.
type SQLiteTestDB struct {
	DBURL string

	mu sync.Mutex
}

var _ cucumber.TestDB = (*SQLiteTestDB)(nil)

func (s *SQLiteTestDB) conn(ctx context.Context) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", s.DBURL)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (s *SQLiteTestDB) ClearAll(ctx context.Context) error {
	db, err := s.conn(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: failed to connect: %w", err)
	}
	defer db.Close()

	tables := []string{
		"entry_embeddings",
		"memory_vectors",
		"memory_usage_stats",
		"memories",
		"tasks",
		"attachments",
		"conversation_ownership_transfers",
		"entries",
		"conversation_memberships",
		"conversations",
		"conversation_groups",
	}
	for _, table := range tables {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			if strings.Contains(err.Error(), "no such table") {
				continue
			}
			return fmt.Errorf("cleanup: failed to delete from %s: %w", table, err)
		}
	}
	return nil
}

func (s *SQLiteTestDB) ResolveGroupID(ctx context.Context, conversationID string) (string, error) {
	db, err := s.conn(ctx)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var groupID string
	if err := db.QueryRowContext(ctx,
		"SELECT conversation_group_id FROM conversations WHERE id = ?", conversationID).Scan(&groupID); err != nil {
		return "", fmt.Errorf("could not resolve conversation group ID for %s: %w", conversationID, err)
	}
	return groupID, nil
}

func (s *SQLiteTestDB) ExecSQL(ctx context.Context, query string) ([]map[string]interface{}, error) {
	db, err := s.conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	query = translateSQLiteBDDQuery(query)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("SQL query failed: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to read columns: %w", err)
	}

	var result []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(cols))
		scan := make([]interface{}, len(cols))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		row := make(map[string]interface{}, len(cols))
		for i, col := range cols {
			row[col] = normalizeSQLiteValue(values[i])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *SQLiteTestDB) ExecMongoQuery(_ context.Context, _ string) ([]map[string]interface{}, error) {
	return nil, nil
}

func (s *SQLiteTestDB) ArchiveConversation(ctx context.Context, conversationID string, days int) error {
	db, err := s.conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	archivedAt := time.Now().AddDate(0, 0, -days)
	if _, err := db.ExecContext(ctx,
		`UPDATE conversation_groups SET archived_at = ? WHERE id = (SELECT conversation_group_id FROM conversations WHERE id = ?)`,
		archivedAt, conversationID); err != nil {
		return fmt.Errorf("failed to archive conversation group: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE conversations SET archived_at = ? WHERE id = ?`,
		archivedAt, conversationID); err != nil {
		return fmt.Errorf("failed to archive conversation: %w", err)
	}
	return nil
}

func (s *SQLiteTestDB) ArchiveConversationOnly(ctx context.Context, conversationID string, days int) error {
	db, err := s.conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	archivedAt := time.Now().AddDate(0, 0, -days)
	if _, err := db.ExecContext(ctx,
		`UPDATE conversations SET archived_at = ? WHERE id = ?`,
		archivedAt, conversationID); err != nil {
		return fmt.Errorf("failed to archive conversation: %w", err)
	}
	return nil
}

func (s *SQLiteTestDB) DeleteAllTasks(ctx context.Context) error {
	db, err := s.conn(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "DELETE FROM tasks")
	return err
}

func (s *SQLiteTestDB) CreateTask(ctx context.Context, id, taskType, body string) error {
	db, err := s.conn(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now().UTC()
	_, err = db.ExecContext(ctx,
		"INSERT INTO tasks (id, task_type, task_body, created_at, retry_at, retry_count) VALUES (?, ?, ?, ?, ?, 0)",
		id, taskType, body, now, now)
	return err
}

func (s *SQLiteTestDB) CreateFailedTask(ctx context.Context, id, taskType, body string) error {
	db, err := s.conn(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now().UTC()
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, task_type, task_body, created_at, retry_at, retry_count, last_error)
		 VALUES (?, ?, ?, ?, ?, 1, 'previous failure')`,
		id, taskType, body, now, now.Add(-time.Hour))
	return err
}

func (s *SQLiteTestDB) ClaimReadyTasks(ctx context.Context, limit int) ([]cucumber.TaskRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.conn(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx,
		"SELECT id, task_type, task_body FROM tasks WHERE retry_at <= ? ORDER BY created_at LIMIT ?",
		now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []cucumber.TaskRow
	var ids []string
	for rows.Next() {
		var t cucumber.TaskRow
		if err := rows.Scan(&t.ID, &t.TaskType, &t.TaskBody); err != nil {
			return nil, err
		}
		result = append(result, t)
		ids = append(ids, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	retryAt := now.Add(5 * time.Minute)
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, "UPDATE tasks SET retry_at = ? WHERE id = ?", retryAt, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SQLiteTestDB) DeleteTask(ctx context.Context, id string) error {
	db, err := s.conn(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	return err
}

func (s *SQLiteTestDB) FailTask(ctx context.Context, id, errMsg string) error {
	db, err := s.conn(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx,
		"UPDATE tasks SET retry_count = retry_count + 1, retry_at = ?, last_error = ? WHERE id = ?",
		time.Now().UTC().Add(30*time.Second), errMsg, id)
	return err
}

func (s *SQLiteTestDB) GetTask(ctx context.Context, id string) (*cucumber.TaskRow, error) {
	db, err := s.conn(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var t cucumber.TaskRow
	if err := db.QueryRowContext(ctx,
		"SELECT id, task_type, retry_at, retry_count, last_error FROM tasks WHERE id = ?", id).
		Scan(&t.ID, &t.TaskType, &t.RetryAt, &t.RetryCount, &t.LastError); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *SQLiteTestDB) CountTasks(ctx context.Context) (int, error) {
	db, err := s.conn(ctx)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks").Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func translateSQLiteBDDQuery(query string) string {
	query = strings.ReplaceAll(query, "task_body->>'conversationGroupId'", "json_extract(task_body, '$.conversationGroupId')")
	return query
}

func normalizeSQLiteValue(v interface{}) interface{} {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	default:
		return t
	}
}
