package sqlitecodec

import (
	"database/sql"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Timestamp columns use the _at suffix in the SQLite schemas.
func isTimestamp(name string) bool { return strings.HasSuffix(name, "_at") }

func split(value any) (any, any) {
	switch v := value.(type) {
	case time.Time:
		return v.Unix(), int64(v.Nanosecond())
	case *time.Time:
		if v != nil {
			return v.Unix(), int64(v.Nanosecond())
		}
		return nil, nil
	case sql.NullTime:
		if v.Valid {
			return v.Time.Unix(), int64(v.Time.Nanosecond())
		}
		return nil, nil
	case *sql.NullTime:
		if v != nil {
			return split(*v)
		}
		return nil, nil
	case clause.Column:
		n := v
		n.Name += "_ns"
		return v, n
	case clause.Expr:
		// GORM uses excluded.column expressions in conflict assignments.
		if strings.HasPrefix(v.SQL, "excluded.") && len(v.Vars) == 0 {
			n := v
			n.SQL += "_ns"
			return v, n
		}
	}
	return value, nil
}

// Install expands timestamp writes before GORM builds SQL, leaving shared models
// unchanged for the other datastores. Raw SQL writes supply the pair explicitly.
func Install(db *gorm.DB) {
	db.ClauseBuilders["VALUES"] = func(c clause.Clause, b clause.Builder) {
		v, ok := c.Expression.(clause.Values)
		if !ok {
			c.Build(b)
			return
		}
		columns := make([]clause.Column, 0, len(v.Columns)*2)
		rows := make([][]any, len(v.Values))
		for i, col := range v.Columns {
			columns = append(columns, col)
			if isTimestamp(col.Name) {
				n := col
				n.Name += "_ns"
				columns = append(columns, n)
			}
			for j, row := range v.Values {
				if isTimestamp(col.Name) {
					sec, ns := split(row[i])
					rows[j] = append(rows[j], sec, ns)
				} else {
					rows[j] = append(rows[j], row[i])
				}
			}
		}
		c.Expression = clause.Values{Columns: columns, Values: rows}
		c.Build(b)
	}
	db.ClauseBuilders["SET"] = func(c clause.Clause, b clause.Builder) {
		set, ok := c.Expression.(clause.Set)
		if !ok {
			c.Build(b)
			return
		}
		expanded := make(clause.Set, 0, len(set)*2)
		for _, a := range set {
			if isTimestamp(a.Column.Name) {
				sec, ns := split(a.Value)
				a.Value = sec
				expanded = append(expanded, a)
				a.Column.Name += "_ns"
				a.Value = ns
			}
			expanded = append(expanded, a)
		}
		c.Expression = expanded
		c.Build(b)
	}
	expandColumns := func(columns []clause.Column) []clause.Column {
		result := make([]clause.Column, 0, len(columns)*2)
		for _, column := range columns {
			result = append(result, column)
			if isTimestamp(column.Name) {
				companion := column
				companion.Name += "_ns"
				companion.Alias = ""
				if column.Alias != "" {
					companion.Alias = column.Alias + "_ns"
				}
				result = append(result, companion)
			}
		}
		return result
	}
	db.ClauseBuilders["SELECT"] = func(c clause.Clause, b clause.Builder) {
		selected, ok := c.Expression.(clause.Select)
		if !ok {
			c.Build(b)
			return
		}
		selected.Columns = expandColumns(selected.Columns)
		c.Expression = selected
		c.Build(b)
	}
	db.ClauseBuilders["RETURNING"] = func(c clause.Clause, b clause.Builder) {
		returning, ok := c.Expression.(clause.Returning)
		if !ok {
			c.Build(b)
			return
		}
		returning.Columns = expandColumns(returning.Columns)
		c.Expression = returning
		c.Build(b)
	}

	// ON CONFLICT builds its SET directly rather than through ClauseBuilders.
	db.ClauseBuilders["ON CONFLICT"] = func(c clause.Clause, b clause.Builder) {
		conflict, ok := c.Expression.(clause.OnConflict)
		if !ok {
			c.Build(b)
			return
		}
		expanded := make(clause.Set, 0, len(conflict.DoUpdates)*2)
		for _, a := range conflict.DoUpdates {
			if isTimestamp(a.Column.Name) {
				sec, ns := split(a.Value)
				a.Value = sec
				expanded = append(expanded, a)
				a.Column.Name += "_ns"
				a.Value = ns
			}
			expanded = append(expanded, a)
		}
		conflict.DoUpdates = expanded
		c.Expression = conflict
		c.Build(b)
	}
}

// Pair is a SQL row-value expression, compared lexicographically as integers.
func Pair(column string) string { return "(" + column + ", " + column + "_ns)" }
func Bounds(t time.Time) []any  { return []any{t.Unix(), int64(t.Nanosecond())} }
func Order(column, direction string) string {
	return column + " " + direction + ", " + column + "_ns " + direction
}
