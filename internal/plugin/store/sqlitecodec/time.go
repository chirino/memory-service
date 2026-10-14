//go:build cgo

package sqlitecodec

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func init() { sql.Register(DriverName, &timestampDriver{}) }

type timestampDriver struct{}

func (*timestampDriver) Open(dsn string) (driver.Conn, error) {
	d := sqlite3.SQLiteDriver{ConnectHook: func(c *sqlite3.SQLiteConn) error {
		// BACKWARD COMPATIBILITY: remove in a future breaking release.
		if err := c.RegisterFunc("legacy_time_seconds", func(v any) (any, error) {
			t, e := legacyTime(v)
			if t == nil || e != nil {
				return nil, e
			}
			return t.Unix(), nil
		}, true); err != nil {
			return err
		}
		// BACKWARD COMPATIBILITY: remove in a future breaking release.
		return c.RegisterFunc("legacy_time_nanos", func(v any) (any, error) {
			t, e := legacyTime(v)
			if t == nil || e != nil {
				return nil, e
			}
			return int64(t.Nanosecond()), nil
		}, true)
	}}
	c, err := d.Open(dsn)
	if err != nil {
		return nil, err
	}
	return &timestampConn{c.(*sqlite3.SQLiteConn)}, nil
}

// BACKWARD COMPATIBILITY: remove in a future breaking release.
func legacyTime(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	var t time.Time
	switch v := value.(type) {
	case string:
		v = strings.TrimSuffix(v, "Z")
		var err error
		for _, layout := range sqlite3.SQLiteTimestampFormats {
			t, err = time.ParseInLocation(layout, v, time.UTC)
			if err == nil {
				return &t, nil
			}
		}
		return nil, fmt.Errorf("invalid legacy SQLite timestamp %q", v)
	case []byte:
		if v == nil {
			return nil, nil
		}
		return legacyTime(string(v))
	case int64:
		// Match go-sqlite3's historical interpretation of numeric DATETIME values.
		if v > 1e12 || v < -1e12 {
			t = time.UnixMilli(v)
		} else {
			t = time.Unix(v, 0)
		}
	default:
		return nil, fmt.Errorf("unsupported legacy SQLite timestamp %T", value)
	}
	return &t, nil
}

// SQL using time.Time, *time.Time, or sql.NullTime supplies two adjacent
// placeholders. Bind exact integer pairs or two NULLs for absent timestamps.
func encode(args []driver.NamedValue) ([]driver.NamedValue, error) {
	result := make([]driver.NamedValue, 0, len(args)*2)
	for _, arg := range args {
		var seconds, nanos any
		switch value := arg.Value.(type) {
		case time.Time:
			seconds, nanos = value.Unix(), int64(value.Nanosecond())
		case sql.NullTime:
			if value.Valid {
				seconds, nanos = value.Time.Unix(), int64(value.Time.Nanosecond())
			}
		default:
			result = append(result, arg)
			continue
		}
		if arg.Name != "" {
			return nil, fmt.Errorf("SQLite paired timestamps require positional parameters")
		}
		result = append(result, driver.NamedValue{Value: seconds}, driver.NamedValue{Value: nanos})
	}
	for i := range result {
		result[i].Ordinal = i + 1
	}
	return result, nil
}

type timestampConn struct{ *sqlite3.SQLiteConn }

// Preserve nullable timestamp identity before database/sql converts nil pointers
// and sql.NullTime values to untyped NULLs. Other values use its normal converter.
func checkNamedValue(arg *driver.NamedValue) error {
	switch value := arg.Value.(type) {
	case *time.Time:
		arg.Value = sql.NullTime{}
		if value != nil {
			arg.Value = sql.NullTime{Time: *value, Valid: true}
		}
	case sql.NullTime:
	case *sql.NullTime:
		arg.Value = sql.NullTime{}
		if value != nil {
			arg.Value = *value
		}
	default:
		return driver.ErrSkip
	}
	return nil
}

func (c *timestampConn) CheckNamedValue(arg *driver.NamedValue) error {
	return checkNamedValue(arg)
}

func validateBindings(count int, args []driver.NamedValue) error {
	if count != len(args) {
		return fmt.Errorf("SQLite statement expects %d parameters, got %d after timestamp expansion; bound parameters must belong to the first statement in a SQL batch", count, len(args))
	}
	return nil
}
func (c *timestampConn) validate(query string, args []driver.NamedValue) error {
	stmt, err := c.SQLiteConn.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	return validateBindings(stmt.NumInput(), args)
}

func (c *timestampConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	var err error
	args, err = encode(args)
	if err != nil {
		return nil, err
	}
	if err := c.validate(query, args); err != nil {
		return nil, err
	}
	return c.SQLiteConn.ExecContext(ctx, query, args)
}
func (c *timestampConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	var err error
	args, err = encode(args)
	if err != nil {
		return nil, err
	}
	if err := c.validate(query, args); err != nil {
		return nil, err
	}
	r, err := c.SQLiteConn.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return wrapRows(r), nil
}
func (c *timestampConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}
func (c *timestampConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	s, err := c.SQLiteConn.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &timestampStmt{s.(*sqlite3.SQLiteStmt)}, nil
}

type timestampStmt struct{ *sqlite3.SQLiteStmt }

func (s *timestampStmt) CheckNamedValue(arg *driver.NamedValue) error {
	return checkNamedValue(arg)
}

// A time.Time expands into two bound integers.
func (s *timestampStmt) NumInput() int { return -1 }

func (s *timestampStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	var err error
	args, err = encode(args)
	if err != nil {
		return nil, err
	}
	if err := validateBindings(s.SQLiteStmt.NumInput(), args); err != nil {
		return nil, err
	}
	return s.SQLiteStmt.ExecContext(ctx, args)
}
func (s *timestampStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	var err error
	args, err = encode(args)
	if err != nil {
		return nil, err
	}
	if err := validateBindings(s.SQLiteStmt.NumInput(), args); err != nil {
		return nil, err
	}
	r, err := s.SQLiteStmt.QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	return wrapRows(r), nil
}

type timestampRows struct {
	*sqlite3.SQLiteRows
	columns    []string
	indices    []int
	companions map[int]int
	raw        []driver.Value
}

func wrapRows(rows driver.Rows) driver.Rows {
	r := rows.(*sqlite3.SQLiteRows)
	names := r.Columns()
	companions := map[int]int{}
	hidden := map[int]bool{}
	for i, name := range names {
		typ := strings.ToUpper(r.ColumnTypeDatabaseTypeName(i))
		if typ != "INTEGER_SECONDS" && !(typ == "" && strings.HasSuffix(name, "_at")) {
			continue
		}
		for j, n := range names {
			if n == name+"_ns" {
				companions[i] = j
				hidden[j] = true
				break
			}
		}
	}
	result := &timestampRows{SQLiteRows: r, companions: companions, raw: make([]driver.Value, len(names))}
	for i, name := range names {
		if !hidden[i] {
			result.columns = append(result.columns, name)
			result.indices = append(result.indices, i)
		}
	}
	return result
}
func (r *timestampRows) Columns() []string { return r.columns }
func (r *timestampRows) Next(dest []driver.Value) error {
	if err := r.SQLiteRows.Next(r.raw); err != nil {
		return err
	}
	for i, index := range r.indices {
		value := r.raw[index]
		if companion, ok := r.companions[index]; ok {
			ns := r.raw[companion]
			if value == nil && ns == nil {
				dest[i] = nil
				continue
			}
			seconds, ok := value.(int64)
			nanos, nok := ns.(int64)
			if !ok || !nok || nanos < 0 || nanos >= 1e9 {
				return fmt.Errorf("invalid SQLite timestamp pair %s: %v, %v", r.columns[i], value, ns)
			}
			value = time.Unix(seconds, nanos).UTC()
		}
		dest[i] = value
	}
	return nil
}
func (r *timestampRows) ColumnTypeScanType(i int) reflect.Type {
	if _, ok := r.companions[r.indices[i]]; ok {
		return reflect.TypeOf(sql.NullTime{})
	}
	return r.SQLiteRows.ColumnTypeScanType(r.indices[i])
}
func (r *timestampRows) ColumnTypeDatabaseTypeName(i int) string {
	return r.SQLiteRows.ColumnTypeDatabaseTypeName(r.indices[i])
}
