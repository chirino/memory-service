# Encryption at Rest

Memory Service supports pluggable at-rest encryption for conversation data and attachments.

## How It Works

All encryption goes through a `Provider` interface with `Encrypt`/`Decrypt` and streaming `EncryptStream`/`DecryptStream` methods. The active provider is selected by the `MEMORY_SERVICE_ENCRYPTION_PROVIDERS` config key (default: `plain`).

Encrypted values are wrapped in an **MSEH envelope**:

```
[4 bytes magic: "MSEH"][varint: header length][EncryptionHeader proto][ciphertext]
```

The header records the provider ID and nonce, allowing multiple providers to coexist (enabling zero-downtime key rotation and migration from plain to encrypted).

## Providers

| Provider ID | Algorithm | Key Source |
|-------------|-----------|------------|
| `plain` | None (passthrough) | — |
| `dek` | AES-256-GCM | `MEMORY_SERVICE_ENCRYPTION_KEY` (base64 or hex, comma-separated for rotation) |
| `kms` | AES-256-GCM, KEK via AWS KMS | `MEMORY_SERVICE_ENCRYPTION_KMS_KEY_ID` |
| `vault` | AES-256-GCM, KEK via Vault Transit | `MEMORY_SERVICE_ENCRYPTION_VAULT_TRANSIT_KEY` |

For `kms` and `vault`, a random 32-byte Data Encryption Key (DEK) is generated on first start, wrapped by the external KMS, and stored in an `encryption_deks` table. The DEK is cached in memory; AWS KMS / Vault is **not** called on every request.

Key rotation: supply multiple comma-separated keys in `MEMORY_SERVICE_ENCRYPTION_KEY`; the first is used for new encryptions, the rest for decryption only.

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
MEMORY_SERVICE_ENCRYPTION_PROVIDERS=dek
MEMORY_SERVICE_ENCRYPTION_KEY=<base64-encoded-32-byte-key>

# AWS KMS
MEMORY_SERVICE_ENCRYPTION_PROVIDERS=kms
MEMORY_SERVICE_ENCRYPTION_KMS_KEY_ID=arn:aws:kms:us-east-1:…:key/…

# HashiCorp Vault Transit
MEMORY_SERVICE_ENCRYPTION_PROVIDERS=vault
MEMORY_SERVICE_ENCRYPTION_VAULT_TRANSIT_KEY=my-transit-key

# No encryption (default)
MEMORY_SERVICE_ENCRYPTION_PROVIDERS=plain
```

## What Is NOT Encrypted: Temporary Files

Several categories of temporary files are written to disk in plaintext and are **never encrypted**, regardless of the encryption configuration:

### Response Resumption Tokens (highest risk)

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
| `memory-service-mongo-gridfs-*` / `memory-service-mongo-attachment-*` | MongoDB GridFS | Deleted on close |
| `memory-service-pg-upload-*` / `memory-service-pg-lo-*` / `memory-service-pg-attachment-*` | PostgreSQL LargeObject | Deleted on close |

These files are transient (seconds), always deleted immediately after use, and represent far lower risk than the resumption files.

### Java Client Attachment Buffers (low risk)

The Quarkus and Spring client libraries write `attachment-*.tmp` files when resolving attachments for LLM context. These are deleted immediately after base64-encoding. Same low-risk profile as the Go transit buffers above.
