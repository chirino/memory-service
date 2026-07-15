# Release Notes

## Unreleased

### Security hardening rollout notes

This release starts the Security Hardening work tracked by
`docs/enhancements/111-security-hardening-findings.md`. Operators should validate production
configuration in staging before upgrade because several unsafe combinations now fail at
startup or require explicit opt-in flags.

- Production deployments that still use `MEMORY_SERVICE_ENCRYPTION_KIND=plain` must either
  switch to `dek`, `kms`, or `vault`, or set the explicit unsafe opt-in
  `MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN=true`.
- Headerless legacy plaintext reads are disabled by default. During a bounded migration
  window, configure a real provider first, include `plain` only as a fallback, and set
  `MEMORY_SERVICE_ENCRYPTION_LEGACY_PLAIN_READ_ENABLED=true`.
- Malformed `MSEH` envelopes no longer fall back to plaintext.
- OIDC issuers require an accepted audience unless
  `MEMORY_SERVICE_OIDC_ALLOW_MISSING_AUDIENCE=true` is set explicitly. Claim-derived
  application roles come only from configured JSON Pointer paths.
- TCP listeners bind to `127.0.0.1` by default. Container, Kubernetes, and Fly deployments
  that expose the service must set `MEMORY_SERVICE_HOST=0.0.0.0`, and dedicated management
  probes must set `MEMORY_SERVICE_MANAGEMENT_HOST=0.0.0.0`.
- Existing omitted/unsafe attachment dispositions now download as
  `application/octet-stream`; active content such as HTML and SVG cannot be forced inline.

Before enabling the planned MSEH v3 attachment-stream or MSEH v4 field migrations, stop all
old memory-service replicas and take a coordinated database plus attachment-object backup.
After any v3/v4 write or migration replacement, rollback to a binary that does not understand
that version requires restoring that pre-upgrade backup. Mixed old/new memory-service
deployments are not supported by this hardening plan.

### Breaking datastore reset: schema version 110

This release squashes datastore migrations and changes conversation fork lineage persistence.

- PostgreSQL and SQLite now use a version-110 baseline schema.
- MongoDB now uses a version-110 baseline collection/index setup.
- Fork lineage is stored in `conversation_ancestry` instead of direct fork fields on conversation rows/documents.
- Existing pre-110 datastores must be reset before startup.
- Startup rejects older datastore layouts with an explicit reset-required error.
- Entry listing uses bounded ancestry/group queries for history, context, journal, all-channel, pagination, `fromSeq`, `upToEntryId`, and `forks=all` reads.

See [Datastore Reset Notes](datastore-reset.md) for reset guidance.
