# Datastore Reset Notes

Memory Service schema version 110 squashes earlier datastore migrations and changes fork lineage persistence.

## When reset is required

Reset PostgreSQL, SQLite, and MongoDB datastores before starting a build that contains schema version 110 if the datastore was created by any earlier schema version.

The service writes schema metadata for fresh version-110 stores and rejects older layouts with a clear startup error. This is intentional: version 110 removes direct fork columns/documents and stores fork lineage in `conversation_ancestry`.

## What changes

- SQL stores use a squashed baseline schema with `schema_metadata` and `conversation_ancestry`.
- MongoDB uses a squashed baseline with `schema_metadata` and a `conversation_ancestry` collection.
- Public fork fields remain in API responses, but stores hydrate them from ancestry rows/documents.
- Entry listing uses bounded ancestry/group queries instead of full group materialization for supported history, context, journal, all-channel, and `forks=all` reads.

## Local development reset

For local development, remove the backing datastore volume or database file, then restart the service so it can initialize the version-110 baseline.

- SQLite: delete the local SQLite database file configured by `DBURL`.
- PostgreSQL: drop/recreate the development database or remove the local Docker volume.
- MongoDB: drop/recreate the development database or remove the local Docker volume.

Do not point a version-110 build at a pre-110 production datastore without a planned reset.
