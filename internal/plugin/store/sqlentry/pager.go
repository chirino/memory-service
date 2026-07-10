package sqlentry

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EntryIDValueFunc converts a UUID entry id into the value shape expected by a
// datastore/dialect. PostgreSQL compares UUID values directly, while SQLite
// stores entry ids as text.
type EntryIDValueFunc func(uuid.UUID) any

func UUIDValue(id uuid.UUID) any { return id }

func UUIDStringValue(id uuid.UUID) any { return id.String() }

type LookupFunc func(string) (model.Entry, bool, error)

type CursorErrorFunc func(context.Context, string, string) error

type BoundedQuery struct {
	Base             *gorm.DB
	FromSeq          *uint32
	UpToEntryID      *string
	AfterEntryID     *string
	BeforeEntryID    *string
	Tail             bool
	Limit            int
	MaxLimit         int
	UpToLookup       LookupFunc
	BaseTransform    func(*gorm.DB) (*gorm.DB, error)
	CursorEntryError CursorErrorFunc
	LimitError       func(int) error
	EntryNotFound    func(string) error
	EntryIDValue     EntryIDValueFunc
	ScanErr          string
}

func RunBoundedQuery(ctx context.Context, query BoundedQuery) ([]model.Entry, *string, *string, error) {
	if query.Limit <= 0 || query.MaxLimit > 0 && query.Limit > query.MaxLimit {
		if query.LimitError != nil {
			return nil, nil, nil, query.LimitError(query.MaxLimit)
		}
		return nil, nil, nil, fmt.Errorf("limit must be between 1 and %d", query.MaxLimit)
	}
	if query.EntryIDValue == nil {
		query.EntryIDValue = UUIDValue
	}

	base := query.Base
	if query.UpToEntryID != nil && *query.UpToEntryID != "" {
		upTo, ok, err := query.UpToLookup(*query.UpToEntryID)
		if err != nil {
			return nil, nil, nil, err
		}
		if !ok {
			if query.EntryNotFound != nil {
				return nil, nil, nil, query.EntryNotFound(*query.UpToEntryID)
			}
			return nil, nil, nil, fmt.Errorf("entry not found: %s", *query.UpToEntryID)
		}
		base = WhereEntryOrderAtOrBeforeAlias(base, "e", upTo, query.EntryIDValue)
	}
	if query.BaseTransform != nil {
		var err error
		base, err = query.BaseTransform(base)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if query.FromSeq != nil {
		base = base.Where("e.seq IS NOT NULL AND e.seq >= ?", *query.FromSeq)
	}
	lookup := func(entryID string) (model.Entry, bool, error) {
		var entry model.Entry
		result := base.Session(&gorm.Session{}).
			Where("e.id = ?", entryID).
			Limit(1).
			Find(&entry)
		if result.Error != nil {
			return model.Entry{}, false, result.Error
		}
		return entry, result.RowsAffected > 0, nil
	}
	return BoundedEntriesFromBase(ctx, base, query.FromSeq, query.AfterEntryID, query.BeforeEntryID, query.Tail, query.Limit, lookup, query.CursorEntryError, query.EntryIDValue, query.ScanErr)
}

func ApplyEpochFilter(base *gorm.DB, epochFilter *registrystore.MemoryEpochFilter, latestWhenNil bool, latestErr string) (*gorm.DB, error) {
	if epochFilter == nil && !latestWhenNil {
		return base, nil
	}
	epoch := normalizeEpochFilter(epochFilter)
	switch epoch.Mode {
	case registrystore.MemoryEpochModeAll:
		return base, nil
	case registrystore.MemoryEpochModeEpoch:
		if epoch.Epoch == nil {
			return base.Where("1 = 0"), nil
		}
		return base.Where("COALESCE(e.epoch, 0) = ?", *epoch.Epoch), nil
	default:
		var epochRow struct {
			MaxEpoch *int64 `gorm:"column:max_epoch"`
		}
		if err := base.Session(&gorm.Session{}).
			Select("MAX(COALESCE(e.epoch, 0)) AS max_epoch").
			Scan(&epochRow).Error; err != nil {
			return nil, fmt.Errorf("%s: %w", latestErr, err)
		}
		if epochRow.MaxEpoch == nil {
			return base.Where("1 = 0"), nil
		}
		return base.Where("COALESCE(e.epoch, 0) = ?", *epochRow.MaxEpoch), nil
	}
}

func normalizeEpochFilter(filter *registrystore.MemoryEpochFilter) registrystore.MemoryEpochFilter {
	if filter == nil || filter.Mode == "" {
		return registrystore.MemoryEpochFilter{Mode: registrystore.MemoryEpochModeLatest}
	}
	return *filter
}

func BoundedEntriesFromBase(ctx context.Context, base *gorm.DB, fromSeq *uint32, afterEntryID, beforeEntryID *string, tail bool, limit int, lookup LookupFunc, cursorEntryError CursorErrorFunc, entryIDValue EntryIDValueFunc, scanErr string) ([]model.Entry, *string, *string, error) {
	if limit <= 0 {
		return nil, nil, nil, fmt.Errorf("limit must be greater than zero")
	}
	if entryIDValue == nil {
		entryIDValue = UUIDValue
	}
	if tail || beforeEntryID != nil {
		order := "e.created_at DESC, e.seq DESC NULLS LAST, e.id DESC"
		if fromSeq != nil {
			order = "e.seq DESC, e.id DESC"
		}
		tx := base.Session(&gorm.Session{}).
			Order(order).
			Limit(limit + 1)
		if beforeEntryID != nil {
			anchor, ok, err := lookup(*beforeEntryID)
			if err != nil {
				return nil, nil, nil, err
			}
			if !ok {
				return nil, nil, nil, cursorEntryError(ctx, "beforeCursor", *beforeEntryID)
			}
			if fromSeq != nil {
				tx = WhereSeqOrderBeforeAlias(tx, "e", anchor, entryIDValue)
			} else {
				tx = WhereEntryOrderBeforeAlias(tx, "e", anchor, entryIDValue)
			}
		}

		var entries []model.Entry
		if err := tx.Find(&entries).Error; err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", scanErr, err)
		}
		for lo, hi := 0, len(entries)-1; lo < hi; lo, hi = lo+1, hi-1 {
			entries[lo], entries[hi] = entries[hi], entries[lo]
		}
		hasMore := len(entries) > limit
		if hasMore {
			entries = entries[1:]
			c := entries[0].ID.String()
			beforeCursor := &c
			var afterCursor *string
			if beforeEntryID != nil && len(entries) > 0 {
				ac := entries[len(entries)-1].ID.String()
				afterCursor = &ac
			}
			return entries, afterCursor, beforeCursor, nil
		}
		var afterCursor *string
		if beforeEntryID != nil && len(entries) > 0 {
			c := entries[len(entries)-1].ID.String()
			afterCursor = &c
		}
		return entries, afterCursor, nil, nil
	}

	order := "e.created_at ASC, e.seq ASC NULLS FIRST, e.id ASC"
	if fromSeq != nil {
		order = "e.seq ASC, e.id ASC"
	}
	tx := base.Session(&gorm.Session{}).
		Order(order).
		Limit(limit + 1)
	if afterEntryID != nil {
		anchor, ok, err := lookup(*afterEntryID)
		if err != nil {
			return nil, nil, nil, err
		}
		if !ok {
			return nil, nil, nil, cursorEntryError(ctx, "afterCursor", *afterEntryID)
		}
		if fromSeq != nil {
			tx = WhereSeqOrderStrictlyAfterAlias(tx, "e", anchor, entryIDValue)
		} else {
			tx = WhereEntryOrderStrictlyAfterAlias(tx, "e", anchor, entryIDValue)
		}
	}

	var entries []model.Entry
	if err := tx.Find(&entries).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", scanErr, err)
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
		afterCursor := entries[len(entries)-1].ID.String()
		var beforeCursor *string
		if afterEntryID != nil && len(entries) > 0 {
			bc := entries[0].ID.String()
			beforeCursor = &bc
		}
		return entries, &afterCursor, beforeCursor, nil
	}
	var beforeCursor *string
	if afterEntryID != nil && len(entries) > 0 {
		c := entries[0].ID.String()
		beforeCursor = &c
	}
	return entries, nil, beforeCursor, nil
}

func WhereEntryOrderBeforeAlias(tx *gorm.DB, alias string, bound model.Entry, entryIDValue EntryIDValueFunc) *gorm.DB {
	id := entryIDValue(bound.ID)
	if bound.Seq == nil {
		return tx.Where(
			fmt.Sprintf("%s.created_at < ? OR (%s.created_at = ? AND %s.seq IS NULL AND %s.id < ?)", alias, alias, alias, alias),
			bound.CreatedAt, bound.CreatedAt, id,
		)
	}
	return tx.Where(
		fmt.Sprintf("%s.created_at < ? OR (%s.created_at = ? AND (%s.seq IS NULL OR %s.seq < ? OR (%s.seq = ? AND %s.id < ?)))", alias, alias, alias, alias, alias, alias),
		bound.CreatedAt, bound.CreatedAt, *bound.Seq, *bound.Seq, id,
	)
}

func WhereEntryOrderStrictlyAfterAlias(tx *gorm.DB, alias string, bound model.Entry, entryIDValue EntryIDValueFunc) *gorm.DB {
	id := entryIDValue(bound.ID)
	if bound.Seq == nil {
		return tx.Where(
			fmt.Sprintf("%s.created_at > ? OR (%s.created_at = ? AND (%s.seq IS NOT NULL OR %s.id > ?))", alias, alias, alias, alias),
			bound.CreatedAt, bound.CreatedAt, id,
		)
	}
	return tx.Where(
		fmt.Sprintf("%s.created_at > ? OR (%s.created_at = ? AND (%s.seq > ? OR (%s.seq = ? AND %s.id > ?)))", alias, alias, alias, alias, alias),
		bound.CreatedAt, bound.CreatedAt, *bound.Seq, *bound.Seq, id,
	)
}

func WhereEntryOrderAtOrBeforeAlias(tx *gorm.DB, alias string, bound model.Entry, entryIDValue EntryIDValueFunc) *gorm.DB {
	id := entryIDValue(bound.ID)
	if bound.Seq == nil {
		return tx.Where(
			fmt.Sprintf("%s.created_at < ? OR (%s.created_at = ? AND %s.seq IS NULL AND %s.id <= ?)", alias, alias, alias, alias),
			bound.CreatedAt, bound.CreatedAt, id,
		)
	}
	return tx.Where(
		fmt.Sprintf("%s.created_at < ? OR (%s.created_at = ? AND (%s.seq IS NULL OR %s.seq < ? OR (%s.seq = ? AND %s.id <= ?)))", alias, alias, alias, alias, alias, alias),
		bound.CreatedAt, bound.CreatedAt, *bound.Seq, *bound.Seq, id,
	)
}

func WhereSeqOrderBeforeAlias(tx *gorm.DB, alias string, bound model.Entry, entryIDValue EntryIDValueFunc) *gorm.DB {
	if bound.Seq == nil {
		return tx.Where("1 = 0")
	}
	return tx.Where(
		fmt.Sprintf("%s.seq < ? OR (%s.seq = ? AND %s.id < ?)", alias, alias, alias),
		*bound.Seq, *bound.Seq, entryIDValue(bound.ID),
	)
}

func WhereSeqOrderStrictlyAfterAlias(tx *gorm.DB, alias string, bound model.Entry, entryIDValue EntryIDValueFunc) *gorm.DB {
	if bound.Seq == nil {
		return tx.Where("1 = 0")
	}
	return tx.Where(
		fmt.Sprintf("%s.seq > ? OR (%s.seq = ? AND %s.id > ?)", alias, alias, alias),
		*bound.Seq, *bound.Seq, entryIDValue(bound.ID),
	)
}
