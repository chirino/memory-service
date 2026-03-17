---
name: gorm-query-patterns
description: Review and edit Go code that uses GORM, especially under `internal/plugin/store/postgres` and `internal/plugin/store/sqlite` or any query path where missing rows may be expected. Use this skill when choosing between `Take`/`First`/`Last` and `Find`, and when you need patterns that avoid noisy `record not found` logs by treating expected absence with `Limit(1).Find(...)` plus `RowsAffected`.
---

# GORM Query Patterns

Use this skill when touching GORM-backed persistence code in this repo.

Base the query shape on whether "no row" is expected control flow or an exceptional condition.

## Rules

- For slice reads, use `Find(&rows)`. An empty result set is normal.
- For a single-row lookup where "not found" is expected, use `Limit(1).Find(&row)` and branch on `RowsAffected`.
- Do not use `Find(&row)` without `Limit(1)` for a single struct destination. GORM's query docs warn that it can read more than one row and is not deterministic without a limit.
- Use `Take`, `First`, or `Last` only when you want GORM to return `ErrRecordNotFound`.
- Use `First` or `Last` only when the ordering is part of the behavior. Otherwise prefer `Take` for required single-row fetches.
- When the store API returns a domain-level `ErrNotFound` or `nil, nil` for absence, do not route that control flow through `gorm.ErrRecordNotFound`; check `RowsAffected` instead.

## Preferred Patterns

Expected absence, returning a domain `ErrNotFound`:

```go
var row SomeRecord
result := tx.Where("user_id = ? AND external_id = ?", companyID, externalID).
	Limit(1).
	Find(&row)
if result.Error != nil {
	return nil, result.Error
}
if result.RowsAffected == 0 {
	return nil, ErrNotFound
}
return &row, nil
```

Expected absence, returning no result:

```go
var row SomeRecord
result := tx.Where("user_id = ? AND status = ?", companyID, "running").
	Order("started_at desc").
	Limit(1).
	Find(&row)
if result.Error != nil {
	return nil, result.Error
}
if result.RowsAffected == 0 {
	return nil, nil
}
return &row, nil
```

Required row, missing row is exceptional:

```go
var row SomeRecord
if err := tx.Where("id = ? AND user_id = ?", id, companyID).Take(&row).Error; err != nil {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
```

## Repo Notes

- The noisy log pattern in this repo comes from optional lookups implemented with `Take(...)` and then translated from `gorm.ErrRecordNotFound`.
- When reviewing `internal/store/gormstore`, inspect helper methods first. A single helper often drives many log lines.
- Prefer consistent query shapes within one file. If a helper uses the optional-single-row pattern, keep nearby helpers aligned.

Current local examples:

- Optional single-row lookups: `internal/store/gormstore/qbo.go`
- Required single-row lookups: `internal/store/gormstore/store.go`, `internal/store/gormstore/extensions.go`

## Review Checklist

- Is zero rows an expected branch?
- If yes, is the code using `Limit(1).Find(...)` and `RowsAffected`?
- If no, is the code using `Take`/`First`/`Last` intentionally?
- If `First` or `Last` is used, is the ordering requirement real and explicit?
- Is `ErrNotFound` coming from store logic rather than from GORM logger noise?

Reference: `https://gorm.io/docs/query.html`
