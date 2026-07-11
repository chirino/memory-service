# Release Notes

## Unreleased

### Breaking datastore reset: schema version 110

This release squashes datastore migrations and changes conversation fork lineage persistence.

- PostgreSQL and SQLite now use a version-110 baseline schema.
- MongoDB now uses a version-110 baseline collection/index setup.
- Fork lineage is stored in `conversation_ancestry` instead of direct fork fields on conversation rows/documents.
- Existing pre-110 datastores must be reset before startup.
- Startup rejects older datastore layouts with an explicit reset-required error.
- Entry listing uses bounded ancestry/group queries for history, context, journal, all-channel, pagination, `fromSeq`, `upToEntryId`, and `forks=all` reads.

See [Datastore Reset Notes](datastore-reset.md) for reset guidance.
