package bdd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresTestDB implements cucumber.TestDB for Postgres.
type PostgresTestDB struct {
	DBURL string
}

var _ cucumber.TestDB = (*PostgresTestDB)(nil)

func (p *PostgresTestDB) conn(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, p.DBURL)
}

func (p *PostgresTestDB) ClearAll(ctx context.Context) error {
	conn, err := p.conn(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: failed to connect: %w", err)
	}
	defer conn.Close(ctx)

	tables := []string{
		"tasks",
		"entries",
		"attachment_file_chunks",
		"attachment_files",
		"attachments",
		"conversation_ownership_transfers",
		"conversation_memberships",
		"conversations",
		"conversation_groups",
	}
	for _, table := range tables {
		if _, err := conn.Exec(ctx, "DELETE FROM "+table); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
				continue
			}
			return fmt.Errorf("cleanup: failed to delete from %s: %w", table, err)
		}
	}
	return nil
}

func (p *PostgresTestDB) ResolveGroupID(ctx context.Context, conversationID string) (string, error) {
	conn, err := p.conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)

	var groupID string
	err = conn.QueryRow(ctx,
		"SELECT conversation_group_id::text FROM conversations WHERE id = $1::uuid", conversationID).Scan(&groupID)
	if err != nil {
		return "", fmt.Errorf("could not resolve conversation group ID for %s: %w", conversationID, err)
	}
	return groupID, nil
}

func (p *PostgresTestDB) ExecSQL(ctx context.Context, query string) ([]map[string]interface{}, error) {
	conn, err := p.conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("SQL query failed: %w", err)
	}
	defer rows.Close()

	var result []map[string]interface{}
	fieldDescs := rows.FieldDescriptions()
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		row := make(map[string]interface{})
		for i, fd := range fieldDescs {
			v := values[i]
			if t, ok := v.(time.Time); ok {
				row[string(fd.Name)] = t.Format(time.RFC3339Nano)
			} else {
				row[string(fd.Name)] = v
			}
		}
		result = append(result, row)
	}
	return result, nil
}

func (p *PostgresTestDB) SoftDeleteConversation(ctx context.Context, conversationID string, days int) error {
	conn, err := p.conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer conn.Close(ctx)

	deletedAt := time.Now().AddDate(0, 0, -days)

	_, err = conn.Exec(ctx,
		`UPDATE conversation_groups SET deleted_at = $1 WHERE id = (SELECT conversation_group_id FROM conversations WHERE id = $2)`,
		deletedAt, conversationID)
	if err != nil {
		return fmt.Errorf("failed to soft-delete conversation group: %w", err)
	}
	_, err = conn.Exec(ctx,
		`UPDATE conversations SET deleted_at = $1 WHERE id = $2`,
		deletedAt, conversationID)
	if err != nil {
		return fmt.Errorf("failed to soft-delete conversation: %w", err)
	}
	return nil
}

func (p *PostgresTestDB) DeleteAllTasks(ctx context.Context) error {
	conn, err := p.conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "DELETE FROM tasks")
	return err
}

func (p *PostgresTestDB) CreateTask(ctx context.Context, id, taskType, body string) error {
	conn, err := p.conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx,
		"INSERT INTO tasks (id, task_type, task_body, created_at, retry_at, retry_count) VALUES ($1, $2, $3::jsonb, NOW(), NOW(), 0)",
		id, taskType, body)
	return err
}

func (p *PostgresTestDB) CreateFailedTask(ctx context.Context, id, taskType, body string) error {
	conn, err := p.conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx,
		`INSERT INTO tasks (id, task_type, task_body, created_at, retry_at, retry_count, last_error)
		 VALUES ($1, $2, $3::jsonb, NOW(), NOW() - INTERVAL '1 hour', 1, 'previous failure')`,
		id, taskType, body)
	return err
}

func (p *PostgresTestDB) ClaimReadyTasks(ctx context.Context, limit int) ([]cucumber.TaskRow, error) {
	conn, err := p.conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx,
		"SELECT id, task_type, task_body::text FROM tasks WHERE retry_at <= NOW() ORDER BY created_at LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []cucumber.TaskRow
	for rows.Next() {
		var t cucumber.TaskRow
		if err := rows.Scan(&t.ID, &t.TaskType, &t.TaskBody); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, nil
}

func (p *PostgresTestDB) DeleteTask(ctx context.Context, id string) error {
	conn, err := p.conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "DELETE FROM tasks WHERE id = $1", id)
	return err
}

func (p *PostgresTestDB) FailTask(ctx context.Context, id, errMsg string) error {
	conn, err := p.conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx,
		"UPDATE tasks SET retry_count = retry_count + 1, retry_at = NOW() + INTERVAL '30 seconds', last_error = $1 WHERE id = $2",
		errMsg, id)
	return err
}

func (p *PostgresTestDB) GetTask(ctx context.Context, id string) (*cucumber.TaskRow, error) {
	conn, err := p.conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	var t cucumber.TaskRow
	err = conn.QueryRow(ctx,
		"SELECT id, task_type, retry_at, retry_count, last_error FROM tasks WHERE id = $1", id).
		Scan(&t.ID, &t.TaskType, &t.RetryAt, &t.RetryCount, &t.LastError)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (p *PostgresTestDB) CountTasks(ctx context.Context) (int, error) {
	conn, err := p.conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close(ctx)
	var count int
	err = conn.QueryRow(ctx, "SELECT COUNT(*) FROM tasks").Scan(&count)
	return count, err
}
