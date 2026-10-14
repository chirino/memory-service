package sqlentry

import (
	"fmt"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/store/sqlitecodec"
	"gorm.io/gorm"
)

func entryColumn(alias, name string) string {
	if alias == "" {
		return name
	}
	return alias + "." + name
}

func EntryTimeOrderSQL(db *gorm.DB, alias string, descending bool) string {
	direction := "ASC"
	if descending {
		direction = "DESC"
	}
	if db.Dialector.Name() == "sqlite" {
		return sqlitecodec.Order(entryColumn(alias, "created_at"), direction)
	}
	return entryColumn(alias, "created_at") + " " + direction
}

func EntryOrderSQL(db *gorm.DB, alias string, descending bool) string {
	direction, nulls := "ASC", "FIRST"
	if descending {
		direction, nulls = "DESC", "LAST"
	}
	return EntryTimeOrderSQL(db, alias, descending) + fmt.Sprintf(", %s %s NULLS %s, %s %s", entryColumn(alias, "seq"), direction, nulls, entryColumn(alias, "id"), direction)
}

func entryTimeBound(db *gorm.DB, alias string, bound model.Entry) (string, string, []any) {
	if db.Dialector.Name() == "sqlite" {
		return sqlitecodec.Pair(entryColumn(alias, "created_at")), "(?, ?)", sqlitecodec.Bounds(bound.CreatedAt)
	}
	return entryColumn(alias, "created_at"), "?", []any{bound.CreatedAt}
}

func WhereEntryTimeAfterAlias(tx *gorm.DB, alias string, bound time.Time, id any) *gorm.DB {
	key, placeholder, values := entryTimeBound(tx, alias, model.Entry{CreatedAt: bound})
	tx = tx.Where(key+" >= "+placeholder, values...)
	args := append(append([]any(nil), values...), values...)
	args = append(args, id)
	return tx.Where(fmt.Sprintf("%s > %s OR (%s = %s AND %s > ?)", key, placeholder, key, placeholder, entryColumn(alias, "id")), args...)
}

func whereEntryOrder(tx *gorm.DB, alias string, bound model.Entry, idValue EntryIDValueFunc, after, inclusive bool) *gorm.DB {
	// Keep an indexed time range before the timestamp/sequence/ID tie-break.
	rangeOp := " <= "
	if after {
		rangeOp = " >= "
	}
	key, placeholder, values := entryTimeBound(tx, alias, bound)
	tx = tx.Where(key+rangeOp+placeholder, values...)
	seq, id := entryColumn(alias, "seq"), entryColumn(alias, "id")
	op, idOp := "<", "<"
	if after {
		op, idOp = ">", ">"
	}
	if inclusive {
		idOp += "="
	}
	args := append(append([]any(nil), values...), values...)
	var tie string
	if bound.Seq == nil {
		tie = fmt.Sprintf("%s IS NULL AND %s %s ?", seq, id, idOp)
		if after {
			tie = fmt.Sprintf("%s IS NOT NULL OR %s %s ?", seq, id, idOp)
		}
	} else {
		tie = fmt.Sprintf("%s IS NULL OR %s < ? OR (%s = ? AND %s %s ?)", seq, seq, seq, id, idOp)
		if after {
			tie = fmt.Sprintf("%s > ? OR (%s = ? AND %s %s ?)", seq, seq, id, idOp)
		}
		args = append(args, *bound.Seq, *bound.Seq)
	}
	args = append(args, idValue(bound.ID))
	return tx.Where(fmt.Sprintf("%s %s %s OR (%s = %s AND (%s))", key, op, placeholder, key, placeholder, tie), args...)
}
