---
status: implemented
---

# Enhancement 066: DB-Stored DEK Table for KEK Providers

> **Status**: Implemented.

## Summary

Replace the current per-message Vault Transit envelope encryption with a
single-record DEK table stored in the application database. The `vault` and
`kms` providers wrap/unwrap DEKs via the KEK backend **once at startup**, cache
the plaintext DEKs in memory, and encrypt/decrypt fields identically to the
`dek` provider. One row per provider stores an ordered array of wrapped DEKs
(`wrapped_deks[0]` = primary, rest = legacy) plus a `revision` counter for
optimistic updates. Key rotation prepends a new wrapped DEK to the array.

## Motivation

The current `vault` provider calls Vault Transit `generate-data-key` on every
encrypt and `transit/decrypt` on every decrypt, and stores the wrapped DEK
(~89 bytes) in each MSEH field header. This has two problems:

1. **Cost**: one Vault/KMS API call per encrypted field per request.
2. **Size**: ~89–184 bytes of per-field header overhead (vs. ~28 bytes for `dek`).

Both problems disappear if the wrapped DEK is stored once and loaded at startup
rather than generated and stored per message. The application database is the
ideal location:

- It is already a required dependency — no new infrastructure.
- The table provides a natural audit trail (one row per DEK, with timestamps).
- Key rotation is a single `INSERT`; old DEKs remain for decryption until retired.
- Vault/KMS is still required to unwrap DEKs at startup — the security boundary
  (database alone is not enough to decrypt) is preserved.

## Non-Goals

- Changes to the `dek` or `plain` providers.
- Re-encryption of existing data.
- Attachment encryption (attachments are already amortised single-blobs).

## Design

### encryption_deks Table

One row per provider, shared by both `vault` and `kms`:

```sql
CREATE TABLE encryption_deks (
    provider     TEXT    NOT NULL PRIMARY KEY,   -- "vault" or "kms"
    wrapped_deks BYTEA[] NOT NULL,               -- wrapped_deks[1] = primary, rest = legacy
    revision     BIGINT  NOT NULL DEFAULT 0      -- incremented on each key rotation
);
```

`wrapped_deks[0]` is always the primary DEK (newest, used for new
encryptions). Subsequent elements are legacy keys retained for
decryption-only rotation. `revision` enables optimistic updates — a key
rotation CLI prepends a new wrapped DEK and increments the revision atomically.

The equivalent MongoDB document:
```json
{ "provider": "vault", "wrapped_deks": [<bytes>,...], "revision": 0 }
```
with a unique index on `provider`.

### Startup Flow

```
rec = SELECT wrapped_deks, revision FROM encryption_deks WHERE provider=$1

if rec is empty:
    plaintextDEK = crypto/rand 32 bytes
    wrappedDEK   = KEK.Encrypt(plaintextDEK)
    INSERT INTO encryption_deks (provider, wrapped_deks, revision)
      VALUES ($1, ARRAY[$2], 0)
      ON CONFLICT (provider) DO NOTHING   -- concurrent replica wins, that's OK
    rec = SELECT again (use whoever won the race)

keys = [KEK.Decrypt(w) for w in rec.wrapped_deks]
```

`keys[0]` is the primary; `keys[1:]` are legacy. With `ON CONFLICT DO NOTHING`,
10 replicas starting simultaneously each attempt the INSERT but only one
succeeds; the others fall back to `SELECT` and use the winner's key.
Total Vault/KMS API calls at startup: one per element in `wrapped_deks`
(typically 1–2, never one per request).

### MSEH Header

No `EncryptedDEK` field. Identical to the `dek` provider:

```
[4 magic][varint proto_len]
[field 1: version=1]     tag 0x08
[field 2: provider_id]   tag 0x12  ("vault" or "kms")
[field 3: iv (12 bytes)] tag 0x1A
[AES-GCM ciphertext]
```

### vault Provider Rewrite

```go
type vaultProvider struct {
    client      *vaultapi.Client
    transitKey  string         // MEMORY_SERVICE_ENCRYPTION_VAULT_TRANSIT_KEY

    once        sync.Once
    primaryKey  []byte
    legacyKeys  [][]byte
    loadErr     error
}
```

`load()` (called via `sync.Once`):
1. Query `encryption_deks` for `provider="vault"` ordered by `id DESC`.
2. Call `transit/decrypt/<transitKey>` once per row.
3. If empty: generate DEK, call `transit/encrypt/<transitKey>`, INSERT row.

Encrypt/Decrypt/EncryptStream/DecryptStream mirror the `dek` provider exactly.

The `GenerateDEK` / `DecryptDEK` per-message methods and the
`KeyEncryptionService` interface are **removed**.

### kms Provider Rewrite

Same pattern. Uses `kms:Decrypt` (and `kms:Encrypt` for new DEKs) instead of
Vault Transit. `MEMORY_SERVICE_ENCRYPTION_KMS_KEY_ID` identifies the KMS CMK
used for wrapping.

### Config Changes

Remove (no longer needed):
- `EncryptionVaultSigningKeyPath` / `--encryption-vault-signing-key-path`
- `EncryptionKMSSigningKeyPath` / `--encryption-kms-signing-key-path`

Keep (still needed for wrapping/unwrapping DEKs at startup):
- `EncryptionVaultTransitKey` / `--encryption-vault-transit-key`
- `EncryptionKMSKeyID` / `--encryption-kms-key-id`

### Attachment Signing Keys

Both providers derive signing keys from their loaded plaintext DEKs via the
same HKDF-SHA256 chain as the `dek` provider. No separate signing-key path in
Vault KV or Secrets Manager is needed.

### Key Rotation (operational)

```bash
# Generate a new DEK and insert it as the new primary:
memory-service keys rotate --provider vault
```

The CLI command (future enhancement):
1. Generates a new 32-byte random DEK.
2. Wraps it via Vault Transit / KMS.
3. INSERTs the new row into `encryption_deks`.

On next restart (or reload), the new DEK is primary; the previous DEK is
legacy and decrypts all existing ciphertext.

## Testing

### Unit Tests

`internal/plugin/encrypt/vault/vault_test.go` (mock Vault client + in-memory DB):

- `TestVaultFirstStart` — empty table → DEK generated, row inserted, encrypt/decrypt works.
- `TestVaultLoadExisting` — row already in table → loaded, no insert.
- `TestVaultKeyRotation` — two rows → newest is primary, oldest is legacy; old ciphertext decrypts.
- `TestVaultEncryptDecryptRoundTrip` — MSEH header has no `EncryptedDEK`.

### BDD

Existing `cucumber_pg_encrypted_test.go` passes unchanged — the provider API
is identical, only the key source changes.

## Tasks

- [x] Add `encryption_deks` table migration for postgres
- [x] Add `encryption_deks` collection for mongo
- [x] Rewrite `vault/vault.go`: load/bootstrap from `encryption_deks`; remove
      Vault Transit per-message methods
- [x] Rewrite `awskms/awskms.go`: same pattern using `kms:Encrypt`/`kms:Decrypt`
- [x] Remove `dataencryption/kek.go` (`KeyEncryptionService` interface)
- [x] Remove `EncryptionVaultSigningKeyPath` and `EncryptionKMSSigningKeyPath`
      from `config.go` and `serve.go`
- [x] Update NOTES in `serve.go`
- [ ] Unit tests (mock Vault + mock DB)
- [ ] Verify BDD encrypted suite passes

## Files to Modify

| File | Change |
|---|---|
| `internal/config/config.go` | Remove `EncryptionVaultSigningKeyPath`, `EncryptionKMSSigningKeyPath` |
| `internal/cmd/serve/serve.go` | Remove signing-key-path flags; update NOTES |
| `internal/plugin/encrypt/vault/vault.go` | Rewrite: load from DB table via Vault Transit unwrap |
| `internal/plugin/encrypt/awskms/awskms.go` | Rewrite: load from DB table via KMS Decrypt |
| `internal/plugin/encrypt/dekstore/dekstore.go` | **new**: shared postgres+mongo DEK table helper |
| `internal/dataencryption/kek.go` | **deleted** |
| `internal/plugin/store/postgres/db/schema.sql` | Add `encryption_deks` table |
| `internal/plugin/store/mongo/mongo.go` | Add `encryption_deks` collection to migration |

## Verification

```bash
go build ./...
go test ./internal/dataencryption/... ./internal/plugin/encrypt/...
go test ./internal/bdd -run TestFeaturesPgEncrypted -count=1
```

## Design Decisions

**Why the application database rather than Vault KV or Secrets Manager?**
The database is already required. Adding a Vault KV path or Secrets Manager
secret introduces a second required external service path to configure, secure,
and back up. Keeping wrapped DEKs in the same store as the data they protect
simplifies operations.

**Why keep `MEMORY_SERVICE_ENCRYPTION_VAULT_TRANSIT_KEY`?**
Vault Transit is still the right primitive for wrapping/unwrapping DEKs — it
provides key versioning, audit logging, and access control. The change is that
it is called once at startup per DEK in the table, not once per encrypted field.

**Security boundary unchanged**
An attacker with only the database sees wrapped DEKs in `encryption_deks` but
cannot unwrap them without Vault/KMS access. The security guarantee — database
alone is insufficient to decrypt — is preserved.
