# Release Notes

## Unreleased

### Clean-break datastore and compatibility reset

This release intentionally drops backward compatibility with pre-release datastore layouts,
encryption formats, deprecated response aliases, and unsafe authentication fallbacks. Reset all
Memory Service datastores and attachment stores before deploying it; rolling upgrades from an
older binary are not supported.

- Production deployments that still use `MEMORY_SERVICE_ENCRYPTION_KIND=plain` must either
  switch to `dek`, `kms`, or `vault`, or set the explicit unsafe opt-in
  `MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN=true`.
- Encrypted database fields accept only MSEH v4. Encrypted attachment streams accept only
  MSEH v3. Headerless data is accepted only when `plain` is the primary provider.
- The one-time `migrate encryption-fields` and `migrate attachments` commands and all legacy
  encryption-read flags have been removed.
- OIDC issuers always require at least one configured accepted audience.
- API keys are accepted through `X-API-Key`, not `Authorization: Bearer`.
- Search cursors must use the current opaque typed token format.
- Error responses no longer duplicate `details.availableTypes`,
  `details.existingTransferId`, or `error` into deprecated top-level aliases.
- TCP listeners bind to `127.0.0.1` by default. Container, Kubernetes, and Fly deployments
  that expose the service must set `MEMORY_SERVICE_HOST=0.0.0.0`, and dedicated management
  probes must set `MEMORY_SERVICE_MANAGEMENT_HOST=0.0.0.0`.
- Existing omitted/unsafe attachment dispositions now download as
  `application/octet-stream`; active content such as HTML and SVG cannot be forced inline.
- PostgreSQL, SQLite, and MongoDB now use a fresh schema version 1 baseline.
- Fork lineage is stored in `conversation_ancestry` instead of direct fork fields on conversation rows/documents.
- Existing datastores from any earlier build must be reset before startup.
- Startup rejects older datastore layouts with an explicit reset-required error.
- Entry listing uses bounded ancestry/group queries for history, context, journal, all-channel, pagination, `fromSeq`, `upToEntryId`, and `forks=all` reads.
- Generated Agent/Admin wrappers are now the only HTTP route registration path; the legacy Gin route-mounting and episodic proxy-adapter layers have been removed.
- Episodic authorization policies now compile as Rego v1. Custom policy directories must use Rego v1 syntax.
- Quarkus clients must configure `memory-service.client.url`; the `quarkus.rest-client.memory-service-client.url` alias is no longer recognized.
- Parsed-but-unused configuration options were removed, including the SSE membership-cache TTL and Infinispan vector TLS booleans. Use an `https://` Infinispan URL to enable TLS.

See [Datastore Reset Notes](datastore-reset.md) for reset guidance.
