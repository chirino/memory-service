---
status: complete
---

# Enhancement 064: Unified Encryption Configuration

> **Status**: Complete.

## Summary

Move `data.encryption.*` config properties under `memory-service.*`, default file-store encryption to enabled whenever real data encryption is configured, and make the plain (no-op) provider a true passthrough — no `EncryptionHeader` written or expected when encryption is disabled.

## Motivation

### Scattered config prefix

The `quarkus-data-encryption` extension currently uses the `data.encryption.*` prefix, while every other memory-service config key uses `memory-service.*`. Operators must consult two separate namespaces to configure encryption:

```properties
data.encryption.providers=dek
data.encryption.dek.key=<key>
memory-service.attachments.encryption.enabled=true
```

Unifying under `memory-service.encryption.*` makes the config surface consistent and discoverable.

### Manual opt-in for file encryption

When an operator configures `data.encryption.providers=dek`, conversation titles and entry content are encrypted automatically. Attachments are **not** encrypted unless they additionally set `memory-service.attachments.encryption.enabled=true`. This asymmetry is surprising — enabling encryption for one kind of data should protect all data.

### Spurious encryption header on plain data

Enhancement [063](063-encrypted-file-store.md) updated `PlainDataEncryptionProvider` to write an `EncryptionHeader` (the `MSEH` magic + protobuf header) even when no encryption is performed. This adds ~30 bytes of overhead to every stored value and to every streamed attachment in no-encryption deployments. It also complicates raw inspection of stored data and means the format is no longer "plaintext" even when encryption is disabled.

## Design

### 1. Rename config prefix: `data.encryption.*` → `memory-service.encryption.*`

`DataEncryptionConfig` changes its `@ConfigMapping` prefix:

```java
// Before
@ConfigMapping(prefix = "data.encryption")
public interface DataEncryptionConfig { ... }

// After
@ConfigMapping(prefix = "memory-service.encryption")
public interface DataEncryptionConfig { ... }
```

All `@ConfigProperty` annotations in provider classes update their `name` accordingly:

| Old key | New key |
|---------|---------|
| `data.encryption.providers` | `memory-service.encryption.providers` |
| `data.encryption.provider.<id>.type` | `memory-service.encryption.provider.<id>.type` |
| `data.encryption.provider.<id>.enabled` | `memory-service.encryption.provider.<id>.enabled` |
| `data.encryption.dek.key` | `memory-service.encryption.dek.key` |
| `data.encryption.dek.decryption-keys` | `memory-service.encryption.dek.decryption-keys` |
| `data.encryption.vault.transit-key` | `memory-service.encryption.vault.transit-key` |

`application.properties` in `memory-service` updates accordingly:

```properties
# Before
data.encryption.providers=plain
data.encryption.provider.plain.type=plain

# After
memory-service.encryption.providers=plain
memory-service.encryption.provider.plain.type=plain
```

### 2. Default file encryption to match data encryption

`FileStoreSelector` (or `AttachmentConfig`) derives whether file encryption should be active from whether a non-plain provider is the primary encryption provider. The explicit `memory-service.attachments.encryption.enabled` property is removed; the decision is automatic:

- **File encryption enabled** when `memory-service.encryption.providers` is non-empty and the first active provider is not `plain`.
- **File encryption disabled** when the primary provider is `plain` (the default).

`FileStoreSelector.init()` changes from:

```java
if (encryptionEnabled) { // explicit config flag
    selected = new EncryptingFileStore(base, dataEncryptionService);
} else {
    selected = base;
}
```

to:

```java
if (dataEncryptionService.isPrimaryProviderReal()) { // derived from encryption config
    if ("s3".equals(storeType) && s3DirectDownloadEnabled) {
        throw new ConfigurationException(...);
    }
    selected = new EncryptingFileStore(base, dataEncryptionService);
} else {
    selected = base;
}
```

`DataEncryptionService` gains a helper:

```java
/** Returns true when the primary provider performs real encryption (not plain/no-op). */
public boolean isPrimaryProviderReal() {
    return !(primaryProvider instanceof PlainDataEncryptionProvider);
}
```

`AttachmentConfig.isEncryptionEnabled()` and the `memory-service.attachments.encryption.enabled` property are deleted.

### 3. Plain provider is a true no-op — no header written or read

`PlainDataEncryptionProvider` becomes completely transparent: no `EncryptionHeader` is written or expected.

```java
@Override
public byte[] encrypt(byte[] plaintext) {
    return plaintext; // identity
}

@Override
public byte[] decrypt(byte[] data) {
    return data; // identity
}

@Override
public OutputStream encryptingStream(OutputStream sink) {
    return sink; // passthrough, no header
}

@Override
public InputStream decryptingStream(InputStream source, EncryptionHeader header) {
    return source; // passthrough
}
```

### 4. `DataEncryptionService` handles header-less data gracefully

Because existing deployments may have data stored without a header (plain provider) alongside newer data with a DEK header, `decrypt()` and `decryptingStream()` must tolerate missing headers:

**`decrypt(byte[])`**:

```java
public byte[] decrypt(byte[] data) {
    EncryptionHeader header;
    try {
        header = EncryptionHeader.read(new ByteArrayInputStream(data));
    } catch (IOException e) {
        // No valid MSEH header — data was stored without encryption; return as-is.
        return data;
    }
    DataEncryptionProvider provider = providersById.get(header.getProviderId());
    if (provider == null) {
        throw new DecryptionFailedException(
            "No data encryption provider registered with id: " + header.getProviderId());
    }
    return provider.decrypt(data);
}
```

**`decryptingStream(InputStream)`**:

Uses a `PushbackInputStream` to peek at the first 4 bytes. If the magic `MSEH` is absent, the stream is returned as-is:

```java
public InputStream decryptingStream(InputStream source) throws IOException {
    PushbackInputStream pis = new PushbackInputStream(source, 4);
    byte[] magic = new byte[4];
    int n = pis.read(magic);
    if (n < 4 || readBigEndianInt(magic) != EncryptionHeader.MAGIC) {
        if (n > 0) pis.unread(magic, 0, n);
        return pis; // no encryption header present — passthrough
    }
    // Valid MSEH prefix; parse remaining header fields and dispatch.
    EncryptionHeader header = EncryptionHeader.readAfterMagic(pis);
    DataEncryptionProvider provider = providersById.get(header.getProviderId());
    if (provider == null) {
        throw new DecryptionFailedException(
            "No data encryption provider registered with id: " + header.getProviderId());
    }
    return provider.decryptingStream(pis, header);
}
```

`EncryptionHeader` gains a package-accessible `readAfterMagic(InputStream)` helper (reads the varint + proto, skipping the already-consumed magic).

### Configuration example after this change

```properties
# AES-256-GCM encryption for conversations and attachments:
memory-service.encryption.providers=dek
memory-service.encryption.provider.dek.type=dek
memory-service.encryption.dek.key=<base64-encoded-32-byte-key>
# memory-service.attachments.encryption.enabled is gone; file encryption
# is now automatic when a real (non-plain) encryption provider is active.

# No encryption (default):
memory-service.encryption.providers=plain
memory-service.encryption.provider.plain.type=plain
```

## Non-Goals

- **Per-tenant encryption keys**: Not in scope.
- **Migration tooling**: Existing data with old-format headers is reset; no re-encryption tooling needed.
- **Vault/AWS-KMS providers**: Config key renaming only; provider implementations are otherwise unchanged.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Unified encryption configuration

  Scenario: File encryption enabled automatically when DEK provider is active
    Given memory-service.encryption.providers is "dek"
    And a valid DEK key is configured
    And I have a conversation with an entry
    When I upload an attachment with content "secret data"
    Then the raw bytes in the file store should not contain "secret data"
    When I download the attachment
    Then the response body should be "secret data"

  Scenario: File encryption disabled when plain provider is active
    Given memory-service.encryption.providers is "plain"
    And I have a conversation with an entry
    When I upload an attachment with content "plain data"
    Then the raw bytes in the file store should contain "plain data"

  Scenario: Plain provider stores conversation entries without encryption header
    Given memory-service.encryption.providers is "plain"
    When I create a conversation with title "my conversation"
    Then the stored title bytes should equal the UTF-8 encoding of "my conversation"

  Scenario: Old config key is rejected at startup
    Given the application.properties contains "data.encryption.providers=plain"
    Then the server should fail to start with an unknown property error
```

### Unit Tests

- `DataEncryptionServiceTest`: `decrypt(byte[])` with header-less plaintext returns plaintext unchanged. `decryptingStream()` with a non-MSEH stream returns it unchanged. Both paths still work with a valid DEK-encrypted payload.
- `PlainDataEncryptionProviderTest`: `encrypt` is identity. `encryptingStream` writes no bytes before plaintext. `decrypt` is identity.
- `FileStoreSelectorTest`: `isPrimaryProviderReal()=false` → base store used directly. `isPrimaryProviderReal()=true` → `EncryptingFileStore` wraps. S3 + direct-download conflict still validated when real encryption is active.

## Tasks

- [x]Update `DataEncryptionConfig` prefix from `data.encryption` to `memory-service.encryption`
- [x]Update `@ConfigProperty` names in `DekDataEncryptionProvider` (`data.encryption.dek.*` → `memory-service.encryption.dek.*`)
- [x]Update `@ConfigProperty` names in `VaultKeyEncryptionService` (`data.encryption.vault.*` → `memory-service.encryption.vault.*`)
- [x]Update `application.properties` in `memory-service` to use `memory-service.encryption.*` keys
- [x]Update `PlainDataEncryptionProvider` to be a true no-op (no header written/read)
- [x]Add `EncryptionHeader.readAfterMagic(InputStream)` helper
- [x]Update `DataEncryptionService.decrypt(byte[])` to handle missing MSEH header gracefully
- [x]Update `DataEncryptionService.decryptingStream(InputStream)` to use `PushbackInputStream` peek
- [x]Add `DataEncryptionService.isPrimaryProviderReal()` method
- [x]Remove `memory-service.attachments.encryption.enabled` from `AttachmentConfig`
- [x]Update `FileStoreSelector` to derive file encryption from `isPrimaryProviderReal()`
- [x]Update Cucumber feature files and step definitions referencing old config keys
- [x]Add an **Encryption** section to `site/src/pages/docs/configuration.mdx` documenting `memory-service.encryption.*` properties, DEK key generation, key rotation, and the S3 direct-download incompatibility
- [x]Update README / docs referencing `data.encryption.*`

## Files to Modify

| File | Change |
|------|--------|
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../DataEncryptionConfig.java` | Change `@ConfigMapping` prefix to `memory-service.encryption` |
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../DataEncryptionService.java` | Add `isPrimaryProviderReal()`; update `decrypt`/`decryptingStream` to handle missing header |
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../PlainDataEncryptionProvider.java` | Make true no-op; remove header write/read |
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../EncryptionHeader.java` | Add `readAfterMagic(InputStream)` helper; expose `MAGIC` constant |
| `quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/src/main/java/.../DekDataEncryptionProvider.java` | Update `@ConfigProperty` names to `memory-service.encryption.dek.*` |
| `quarkus/quarkus-data-encryption/quarkus-data-encryption-vault/src/main/java/.../VaultKeyEncryptionService.java` | Update `@ConfigProperty` name to `memory-service.encryption.vault.*` |
| `memory-service/src/main/resources/application.properties` | Rename keys; remove `memory-service.attachments.encryption.enabled` |
| `memory-service/src/main/java/.../config/AttachmentConfig.java` | Remove `encryptionEnabled` field and `isEncryptionEnabled()` |
| `memory-service/src/main/java/.../attachment/FileStoreSelector.java` | Derive file encryption from `isPrimaryProviderReal()`; remove explicit flag |
| `memory-service/src/test/resources/features/*.feature` | Update config key references |
| `memory-service/src/test/java/.../*Test.java` | Update config key references in test setup |
| `site/src/pages/docs/configuration.mdx` | Add **Encryption** section documenting `memory-service.encryption.*` |
| `README.md` / `docs/` | Update config key references |

## Verification

```bash
# Compile
./mvnw compile

# Run tests
./mvnw test -pl memory-service > test.log 2>&1
# Search for failures using Grep tool on test.log
```

## Design Decisions

1. **Derive file encryption automatically**: Asking operators to set both `memory-service.encryption.providers=dek` and `memory-service.attachments.encryption.enabled=true` is error-prone. The intent of "enable encryption" should encrypt everything; making file encryption automatic when a real provider is active removes the footgun.
2. **Plain provider as true no-op**: Writing an `MSEH` header even when no encryption is performed is misleading and adds overhead. Making the plain provider completely transparent means "no encryption" truly means no overhead and no format change to stored bytes.
3. **Graceful handling of missing header in `DataEncryptionService`**: Because datastores may contain a mix of old-format plain-header data and new headerless plain data (or DEK-encrypted data), the service must try to read the header and fall back to passthrough on failure. This handles the transition without a migration step.
4. **Removing the explicit flag rather than defaulting it**: A boolean `memory-service.attachments.encryption.enabled` that operators could set to `false` while using a real encryption provider would create a confusing half-encrypted state. Deriving it from the encryption provider state removes ambiguity.

## Docs Section Design (`configuration.mdx`)

Add an **Encryption** section between the existing "Attachment Storage" and "Vector Store Configuration" sections. It should cover:

### Property table

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.encryption.providers` | comma-separated provider IDs | `plain` | Ordered list of providers; first active provider encrypts new data |
| `memory-service.encryption.provider.<id>.type` | `plain`, `dek`, `vault` | _(required)_ | Provider type for the named ID |
| `memory-service.encryption.provider.<id>.enabled` | `true`, `false` | `true` | Whether this provider is active |
| `memory-service.encryption.dek.key` | base64 string | _(required for dek)_ | Primary AES-256-GCM key (base64-encoded 32 bytes) |
| `memory-service.encryption.dek.decryption-keys` | comma-separated base64 strings | _(none)_ | Additional old keys for decryption during key rotation |
| `memory-service.encryption.vault.transit-key` | string | _(required for vault)_ | Vault transit key name |

### DEK config block example

```properties
# AES-256-GCM encryption for all stored data and attachments
memory-service.encryption.providers=dek
memory-service.encryption.provider.dek.type=dek
memory-service.encryption.dek.key=<base64-encoded-32-byte-key>
```

Include a tip showing how to generate a key:

```bash
openssl rand -base64 32
```

### Key rotation note

Explain that `memory-service.encryption.dek.decryption-keys` accepts old keys as a comma-separated list. New data is always written with the primary `dek.key`; old data is decrypted by trying keys in order.

### Attachment note

Explain that enabling a non-plain provider automatically protects attachments too — no extra flag needed. Also note the S3 direct-download incompatibility (must set `memory-service.attachments.s3.direct-download=false` or leave it disabled when encryption is active).
