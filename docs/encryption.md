# Encryption at Rest

Memory Service supports pluggable at-rest encryption for conversation data and attachments.

## How It Works

All encryption goes through a `Provider` interface with `Encrypt`/`Decrypt` and streaming `EncryptStream`/`DecryptStream` methods. The active provider order is selected by the `MEMORY_SERVICE_ENCRYPTION_KIND` config key (default: `plain`). The first provider is primary and encrypts new data; any later providers are decryption-only fallbacks for migration or key rotation. Outside testing, `plain` as the primary provider is rejected unless `MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN=true` is set explicitly.

Encrypted values are wrapped in an **MSEH envelope**:

```
[4 bytes magic: "MSEH"][varint: header length][EncryptionHeader proto][ciphertext]
```

The header records the provider ID, version, and nonce, allowing multiple providers to
coexist for key rotation and migration from plain to encrypted data. Persisted database
fields now use MSEH v4, which authenticates the field purpose and immutable record identity
as AES-GCM AAD so ciphertext cannot be swapped between rows or field types.

Headerless legacy plaintext reads are disabled by default. During a migration from
headerless plaintext to an encrypted provider, configure the encrypted provider first,
include `plain` as a fallback, and set
`MEMORY_SERVICE_ENCRYPTION_LEGACY_PLAIN_READ_ENABLED=true` only while migration reads are
required. Malformed `MSEH` envelopes always fail closed and are never treated as plaintext.
Legacy MSEH v1 byte-encrypted database fields remain readable only while
`MEMORY_SERVICE_ENCRYPTION_LEGACY_BYTE_V1_READ_ENABLED=true`; disable it after the field
migration reports no remaining v1 values.

## Providers

| Provider ID | Algorithm | Key Source |
|-------------|-----------|------------|
| `plain` | None (passthrough) | — |
| `dek` | AES-256-GCM for MSEH v4 field values and MSEH v3 attachment streams | `MEMORY_SERVICE_ENCRYPTION_DEK_KEY` (base64 or hex, comma-separated for rotation) |
| `kms` | AES-256-GCM for MSEH v4 field values and MSEH v3 attachment streams; KEK via AWS KMS | `MEMORY_SERVICE_ENCRYPTION_KMS_KEY_ID` |
| `vault` | AES-256-GCM for MSEH v4 field values and MSEH v3 attachment streams; KEK via Vault Transit | `MEMORY_SERVICE_ENCRYPTION_VAULT_TRANSIT_KEY` |

For `kms` and `vault`, a random 32-byte Data Encryption Key (DEK) is generated on first start, wrapped by the external KMS, and stored in an `encryption_deks` table. The DEK is cached in memory; AWS KMS / Vault is **not** called on every request.

Key rotation for the `dek` provider: supply multiple comma-separated keys in `MEMORY_SERVICE_ENCRYPTION_DEK_KEY`; the first is used for new encryptions, the rest for decryption only.

Signed attachment download URLs do not use `MEMORY_SERVICE_ATTACHMENT_SIGNING_SECRET`. Signing keys are derived from the configured encryption provider material via HKDF-SHA256. For the `dek` provider that means `MEMORY_SERVICE_ENCRYPTION_DEK_KEY`; for `kms` and `vault` it means the provider-managed DEKs loaded from the `encryption_deks` table.

Attachment writes use MSEH v3 authenticated AES-GCM records. Legacy MSEH v2 AES-CTR
attachment streams remain readable only while
`MEMORY_SERVICE_ENCRYPTION_LEGACY_STREAM_V2_READ_ENABLED=true`; disable that flag after the
attachment migration reports no remaining v2 objects.

### Migrating Legacy Attachment Streams

After deploying a binary that writes MSEH v3 streams, inventory legacy v2 objects first:

```bash
memory-service migrate attachments \
  --db-url "$MEMORY_SERVICE_DB_URL" \
  --db-kind postgres \
  --attachments-kind db \
  --encryption-kind dek \
  --encryption-dek-key "$MEMORY_SERVICE_ENCRYPTION_DEK_KEY" \
  --to-stream-version=3 \
  --dry-run
```

Then run the same command without `--dry-run`. The migrator pages through admin attachment
metadata, skips non-v2 and already-v3 objects, decrypts v2 objects, verifies the plaintext
size and SHA-256 against attachment metadata, rewrites through the v3 stream writer into a
local ciphertext temp file, and atomically replaces the existing storage key. It refuses to
mutate content when the selected attachment store does not support atomic replacement.

### Migrating Legacy Database Fields

After deploying a binary that writes MSEH v4 fields, inventory legacy database values first:

```bash
memory-service migrate encryption-fields \
  --db-url "$MEMORY_SERVICE_DB_URL" \
  --db-kind postgres \
  --encryption-kind dek \
  --encryption-dek-key "$MEMORY_SERVICE_ENCRYPTION_DEK_KEY" \
  --to-version=4 \
  --dry-run
```

Then run the same command without `--dry-run`. The migrator scans conversation titles,
entry content, admin checkpoint values, and episodic memory values in stable order. It skips
already-v4 values, decrypts legacy v1 values under
`MEMORY_SERVICE_ENCRYPTION_LEGACY_BYTE_V1_READ_ENABLED=true`, optionally decrypts headerless
values when `MEMORY_SERVICE_ENCRYPTION_LEGACY_PLAIN_READ_ENABLED=true` and `plain` is a
fallback provider, rewrites each value with the v4 field context, and conditionally updates
only when the stored ciphertext still matches the value that was read. Concurrently changed
rows are counted and left for the next run.

After a successful non-dry run, verify that
`memory_service_encryption_legacy_field_reads_total` remains zero during normal traffic,
then disable `MEMORY_SERVICE_ENCRYPTION_LEGACY_BYTE_V1_READ_ENABLED` and any temporary
headerless-plaintext compatibility flag.

## What Is Encrypted

| Data | Encrypted | Notes |
|------|-----------|-------|
| Conversation title | Yes | Stored in DB |
| Entry content | Yes | Stored in DB |
| Attachment file content | Yes | Stored in file store (DB, S3, filesystem) |
| Conversation/entry metadata | **No** | Timestamps, UUIDs, `channel`, membership records |
| Attachment metadata | **No** | Size, content-type, storage key |
| Vector embeddings / search indexes | **No** | Stored in PGVector / Qdrant |

Attachment encryption can be disabled independently with `MEMORY_SERVICE_ENCRYPTION_ATTACHMENTS_DISABLED=true`. DB encryption can be disabled with `MEMORY_SERVICE_ENCRYPTION_DB_DISABLED=true`.

> **Note**: Encrypted attachments are incompatible with S3 direct-download (pre-signed URLs). The service automatically proxies attachment downloads when encryption is active.

## Configuration

```bash
# Direct AES key (DEK provider)
MEMORY_SERVICE_ENCRYPTION_KIND=dek
MEMORY_SERVICE_ENCRYPTION_DEK_KEY=<base64-or-hex-encoded-32-byte-key>

# AWS KMS
MEMORY_SERVICE_ENCRYPTION_KIND=kms
MEMORY_SERVICE_ENCRYPTION_KMS_KEY_ID=arn:aws:kms:us-east-1:…:key/…

# HashiCorp Vault Transit
MEMORY_SERVICE_ENCRYPTION_KIND=vault
MEMORY_SERVICE_ENCRYPTION_VAULT_TRANSIT_KEY=my-transit-key

# No encryption (explicit unsafe production opt-in)
MEMORY_SERVICE_ENCRYPTION_KIND=plain
MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN=true

# Read headerless legacy plaintext during migration to DEK
MEMORY_SERVICE_ENCRYPTION_KIND=dek,plain
MEMORY_SERVICE_ENCRYPTION_DEK_KEY=<base64-or-hex-encoded-32-byte-key>
MEMORY_SERVICE_ENCRYPTION_LEGACY_PLAIN_READ_ENABLED=true
```

## What Is NOT Encrypted: Temporary Files

Several categories of temporary files are written to disk in plaintext and are **never encrypted**, regardless of the encryption configuration:

### Response Recording and Resumption Tokens (highest risk)

**Pattern**: `response-resume-*.tokens` in `$MEMORY_SERVICE_TEMP_DIR` (defaults to OS `/tmp`)

During streaming LLM responses, tokens are spooled to disk so that a second client can replay the in-progress response. These files contain the raw LLM output — which may include conversation context and user-provided content — in plaintext. They persist for up to 30 minutes after the stream completes (configurable via `MEMORY_SERVICE_RESUMER_RETENTION`).

**Mitigations**:
- Set `MEMORY_SERVICE_TEMP_DIR` to a directory on an encrypted filesystem or a RAM-backed tmpfs.
- Individual files are created with `0600` (owner read/write only). When the service creates its own temp directory it uses `0700`, but the OS default `/tmp` is world-listable (`1777`), meaning other local users can see the filenames (which include the conversation ID) even though they cannot read the file content.
- Reduce retention: `MEMORY_SERVICE_RESUMER_RETENTION=5m`

### Attachment Transit Buffers (low risk)

Attachment content is buffered in plaintext to disk during ingestion and retrieval in four stores:

| Pattern | Store | Lifetime |
|---------|-------|----------|
| `memory-service-source-url-*` | HTTP source URL download | Deleted after store |
| `memory-service-s3-upload-*` | S3 upload buffer | Deleted after upload |
| `memory-service-pg-upload-*` / `memory-service-pg-lo-*` / `memory-service-pg-attachment-*` | PostgreSQL LargeObject | Deleted on close |

These files are transient (seconds), always deleted immediately after use, and represent far lower risk than the resumption files.

### Java Client Attachment Buffers (low risk)

The Quarkus and Spring client libraries write `attachment-*.tmp` files when resolving attachments for LLM context. These are deleted immediately after base64-encoding. Same low-risk profile as the Go transit buffers above.
