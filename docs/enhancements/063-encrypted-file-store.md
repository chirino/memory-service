---
status: proposed
---

# Enhancement 063: Encrypted File Store

> **Status**: Proposed.

## Summary

Add transparent streaming encryption to the file store layer so that attachment data is encrypted at rest regardless of the underlying storage backend (S3, PostgreSQL, MongoDB). A new `EncryptionHeader` class replaces `EncryptionEnvelope` in `quarkus-data-encryption`, unifying the encryption format for both small byte-array data (MemoryStores) and large streaming data (FileStore). `DataEncryptionService` gains stream-oriented methods that `EncryptingFileStore` — a new decorator — uses via a virtual-thread pipe to preserve end-to-end streaming without buffering entire files.

## Motivation

Attachment data stored via `FileStore` is currently written in plaintext. This is a concern in environments where:

1. **Shared infrastructure**: The S3 bucket or database may be managed by a team that should not have access to attachment contents.
2. **Compliance**: Regulations (HIPAA, GDPR, SOC 2) may require application-level encryption at rest, beyond what the storage provider offers natively.
3. **Defense in depth**: Even when S3 server-side encryption (SSE) or PostgreSQL TDE is enabled, application-level encryption ensures that a storage credential leak does not expose plaintext data.
4. **Consistency**: Conversation titles and entry content are already encrypted via `DataEncryptionService`. Attachments should have the same protection.

### Current State

- `FileStore` interface: `store()`, `retrieve()`, `delete()`, `getSignedUrl()` — defined in `FileStore.java`.
- Two implementations: `S3FileStore` (AWS S3) and `DatabaseFileStore` (PostgreSQL LargeObject / MongoDB GridFS).
- `FileStoreSelector` picks the implementation based on `memory-service.attachments.store` config (`db` or `s3`).
- `AttachmentsResource` computes a SHA-256 digest of the plaintext during upload and stores it in `AttachmentDto`.
- S3 presigned URL redirects (`getSignedUrl`) bypass the application entirely — the client downloads directly from S3.

### Existing Encryption Infrastructure

The project already has a full encryption stack in `quarkus/quarkus-data-encryption/`:

- **`DataEncryptionService`** — `encrypt(byte[])` / `decrypt(byte[])` with pluggable provider routing.
- **`DataEncryptionProvider`** — interface with implementations:
  - `PlainDataEncryptionProvider` — no-op, used when encryption is disabled.
  - `DekDataEncryptionProvider` — AES-256-GCM. Key rotation via `data.encryption.dek.key` / `data.encryption.dek.decryption-keys`.
  - Vault integration via `VaultKeyEncryptionService`.
- **`EncryptionEnvelope`** — current protobuf wrapper that embeds provider ID + full ciphertext payload as a `bytes` field. **Replaced by `EncryptionHeader` in this enhancement.**

The byte[]-based `DataEncryptionService` API is unsuitable for large files — buffering a 10 MB attachment into a `byte[]` for every upload/download is wasteful. This enhancement adds a streaming API using a new framing format, and migrates the byte[] path to the same format.

## Design

### New Class: `EncryptionHeader`

`EncryptionHeader` replaces `EncryptionEnvelope`. It is only the _prefix_ before the ciphertext; the payload is never embedded inside it. This makes it equally suitable for streaming large files or prefixing small byte arrays.

#### Binary Wire Format

```
[4 bytes magic: 0x4D534548]        // "MSEH" — Memory Service Encryption Header
[protobuf uint32 varint: header length]
[N bytes: protobuf-encoded EncryptionHeader message]
```

Followed immediately by the raw ciphertext payload (for streaming) or the ciphertext bytes (for the byte[] path).

#### Protobuf Definition

```protobuf
message EncryptionHeader {
  uint32 version    = 1;
  string provider_id = 2;
  bytes  iv          = 3;  // 12 bytes for AES-GCM; empty for plain provider
}
```

The `version` field allows future format evolution. The `provider_id` identifies which `DataEncryptionProvider` to dispatch to for decryption. The `iv` carries the GCM nonce generated fresh for each encryption operation.

#### Java Class

```java
public final class EncryptionHeader {

    private static final int MAGIC = 0x4D534548; // "MSEH"

    private final int version;
    private final String providerId;
    private final byte[] iv;

    /** Read [magic][2-byte length][proto] from the start of a stream. */
    public static EncryptionHeader read(InputStream is) throws IOException { ... }

    /** Write [magic][2-byte length][proto] to a stream. */
    public void write(OutputStream os) throws IOException { ... }

    public int getVersion()      { return version; }
    public String getProviderId(){ return providerId; }
    public byte[] getIv()        { return iv; }
}
```

### Updated `DataEncryptionProvider` Interface

Two default streaming methods are added. Providers that support streaming override them; those that don't throw `UnsupportedOperationException`:

```java
public interface DataEncryptionProvider {

    String id();
    byte[] encrypt(byte[] plaintext);
    byte[] decrypt(byte[] ciphertext) throws DecryptionFailedException;

    /**
     * Write an EncryptionHeader to sink, then return a stream that encrypts
     * all subsequently written bytes into sink.
     * The caller must close the returned stream to flush the final GCM tag.
     */
    default OutputStream encryptingStream(OutputStream sink) throws IOException {
        throw new UnsupportedOperationException("Provider " + id() + " does not support streaming");
    }

    /**
     * Given an already-read EncryptionHeader, return a decrypting InputStream
     * over the remaining bytes in source.
     */
    default InputStream decryptingStream(InputStream source, EncryptionHeader header)
            throws IOException {
        throw new UnsupportedOperationException("Provider " + id() + " does not support streaming");
    }
}
```

The existing `encrypt(byte[])` / `decrypt(byte[])` methods are updated in each provider to use the new `EncryptionHeader` format instead of `EncryptionEnvelope`.

#### `DekDataEncryptionProvider`

```java
@Override
public byte[] encrypt(byte[] plaintext) {
    ByteArrayOutputStream baos = new ByteArrayOutputStream();
    try (OutputStream out = encryptingStream(baos)) {
        out.write(plaintext);
    }
    return baos.toByteArray();
}

@Override
public byte[] decrypt(byte[] data) throws DecryptionFailedException {
    InputStream in = new ByteArrayInputStream(data);
    EncryptionHeader header = EncryptionHeader.read(in);
    return decryptingStream(in, header).readAllBytes();
}

@Override
public OutputStream encryptingStream(OutputStream sink) throws IOException {
    byte[] iv = new byte[12];
    secureRandom.nextBytes(iv);
    new EncryptionHeader(1, id(), iv).write(sink);
    Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
    cipher.init(ENCRYPT_MODE, secretKey, new GCMParameterSpec(128, iv));
    return new CipherOutputStream(sink, cipher); // close() appends GCM tag
}

@Override
public InputStream decryptingStream(InputStream source, EncryptionHeader header)
        throws IOException {
    // Try all decryptionKeys in order to support key rotation.
    for (SecretKey key : decryptionKeys) {
        try {
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(DECRYPT_MODE, key, new GCMParameterSpec(128, header.getIv()));
            return new CipherInputStream(source, cipher); // GCM tag verified at end of stream
        } catch (InvalidKeyException ignored) { }
    }
    throw new DecryptionFailedException("No configured key could decrypt this data");
}
```

#### `PlainDataEncryptionProvider`

```java
@Override
public byte[] encrypt(byte[] plaintext) {
    ByteArrayOutputStream baos = new ByteArrayOutputStream();
    try (OutputStream out = encryptingStream(baos)) {
        out.write(plaintext);
    }
    return baos.toByteArray();
}

@Override
public byte[] decrypt(byte[] data) {
    ByteArrayInputStream bais = new ByteArrayInputStream(data);
    EncryptionHeader.read(bais); // consume and discard header
    return bais.readAllBytes();
}

@Override
public OutputStream encryptingStream(OutputStream sink) throws IOException {
    new EncryptionHeader(1, id(), new byte[0]).write(sink);
    return sink; // passthrough
}

@Override
public InputStream decryptingStream(InputStream source, EncryptionHeader header) {
    return source; // passthrough
}
```

### Updated `DataEncryptionService`

`EncryptionEnvelope` is removed. The service now delegates the full header+payload framing to the provider:

```java
// Unchanged signature, updated implementation — no more EncryptionEnvelope:
public byte[] encrypt(byte[] plaintext) {
    return primaryProvider.encrypt(plaintext);
}

public byte[] decrypt(byte[] data) {
    EncryptionHeader header;
    try {
        header = EncryptionHeader.read(new ByteArrayInputStream(data));
    } catch (IOException e) {
        throw new DecryptionFailedException("Not a valid encrypted payload (missing MSEH header)");
    }
    DataEncryptionProvider provider = providersById.get(header.getProviderId());
    if (provider == null) {
        throw new DecryptionFailedException(
            "No data encryption provider registered with id: " + header.getProviderId());
    }
    // Re-wrap data as stream starting after the header bytes already consumed above.
    return provider.decrypt(data); // provider rereads header internally via byte[] path
}

/** Returns an OutputStream that encrypts into sink (provider writes header first). */
public OutputStream encryptingStream(OutputStream sink) throws IOException {
    return primaryProvider.encryptingStream(sink);
}

/** Reads the EncryptionHeader from source and returns a decrypting InputStream. */
public InputStream decryptingStream(InputStream source) throws IOException {
    EncryptionHeader header = EncryptionHeader.read(source);
    DataEncryptionProvider provider = providersById.get(header.getProviderId());
    if (provider == null) {
        throw new DecryptionFailedException(
            "No data encryption provider registered with id: " + header.getProviderId());
    }
    return provider.decryptingStream(source, header);
}
```

### Deletion of `EncryptionEnvelope`

`EncryptionEnvelope.java` is deleted. All call sites in `DataEncryptionService` that previously wrapped output in `EncryptionEnvelope` are replaced by the provider's own `encryptingStream` / `encrypt` implementations, which write `EncryptionHeader` instead.

The MemoryStore data format changes — existing encrypted data (titles, entry content) stored as `EncryptionEnvelope` will not be readable after this migration. Since the datastores are reset frequently and no backward compatibility is required, this is acceptable.

### `EncryptingFileStore` Decorator

```java
@ApplicationScoped
public class EncryptingFileStore implements FileStore {

    private final FileStore delegate;
    private final DataEncryptionService encryption;

    // store(): virtual-thread pipe — encrypt while delegate reads
    // retrieve(): delegate.retrieve() → decryptingStream()
    // delete(): delegate.delete() — no crypto
    // getSignedUrl(): Optional.empty() — must proxy through server
}
```

#### `store(InputStream data, long maxSize, String contentType)`

Uses a virtual-thread pipe — the delegate reads ciphertext as the encryptor produces it, with no full-file buffering:

```java
@Override
public FileStoreResult store(InputStream data, long maxSize, String contentType)
        throws FileStoreException {
    try {
        PipedInputStream pipedIn = new PipedInputStream(65536);
        PipedOutputStream pipedOut = new PipedOutputStream(pipedIn);
        AtomicReference<Exception> encryptError = new AtomicReference<>();

        Thread.ofVirtual().start(() -> {
            try (OutputStream encStream = encryption.encryptingStream(pipedOut)) {
                data.transferTo(encStream);
            } catch (Exception e) {
                encryptError.set(e);
            } finally {
                try { pipedOut.close(); } catch (IOException ignored) {}
            }
        });

        // Delegate reads from pipe while the virtual thread encrypts.
        // maxSize is widened to accommodate header bytes + 16-byte GCM tag.
        FileStoreResult result = delegate.store(pipedIn, maxSize + ENCRYPTION_OVERHEAD, contentType);

        if (encryptError.get() != null) {
            throw new FileStoreException("ENCRYPTION_ERROR", 500, encryptError.get().getMessage());
        }
        return result;
    } catch (IOException e) {
        throw new FileStoreException("STORAGE_ERROR", 500, e.getMessage());
    }
}
```

`ENCRYPTION_OVERHEAD` = `EncryptionHeader` size (~30 bytes) + 16-byte GCM tag.

#### `retrieve(String storageKey)`

```java
@Override
public InputStream retrieve(String storageKey) throws FileStoreException {
    InputStream cipherStream = delegate.retrieve(storageKey);
    try {
        return encryption.decryptingStream(cipherStream);
    } catch (IOException e) {
        throw new FileStoreException("DECRYPTION_ERROR", 500, e.getMessage());
    }
}
```

The caller reads plaintext while `CipherInputStream` decrypts on the fly. The GCM authentication tag is verified when the stream is fully consumed.

#### `getSignedUrl(String storageKey, Duration expiry)`

Always returns `Optional.empty()`. Stored bytes are ciphertext + header; a presigned S3 URL would serve garbage. All downloads must proxy through the server.

### SHA-256 Digest Handling

The plaintext SHA-256 is computed in `AttachmentsResource` *before* the stream reaches `FileStore.store()`. Ordering is unchanged:

```
Raw upload → DigestInputStream (SHA-256) → CountingInputStream → EncryptingFileStore.store()
                                                                   └─[virtual thread pipe]→ delegate.store()
```

The SHA-256 still reflects the plaintext, correct for deduplication and integrity checks.

### No New Database Schema

`EncryptionHeader` is a binary prefix prepended to stored bytes. The file store already stores opaque bytes identified by a storage key — no new columns or metadata fields needed.

### Wiring in `FileStoreSelector`

```java
@PostConstruct
void init() {
    FileStore base = "s3".equals(storeType) ? s3FileStore : databaseFileStore;
    if (encryptionEnabled) {
        if ("s3".equals(storeType) && s3DirectDownloadEnabled) {
            throw new ConfigurationException(
                "S3 direct download (memory-service.attachments.s3.direct-download=true) " +
                "is incompatible with file encryption " +
                "(memory-service.attachments.encryption.enabled=true). " +
                "Disable S3 direct download or disable encryption.");
        }
        selected = new EncryptingFileStore(base, dataEncryptionService);
    } else {
        selected = base;
    }
}
```

Fails fast rather than silently serving encrypted bytes via presigned URLs.

### Configuration

One new property for the file store. All key management uses the existing `data.encryption.*` config:

```properties
# Enable/disable file store encryption (default: false)
memory-service.attachments.encryption.enabled=false
```

Full example — AES-256-GCM for both MemoryStores and FileStore:

```properties
data.encryption.providers=dek
data.encryption.dek.key=<base64-encoded-32-byte-key>
data.encryption.dek.decryption-keys=<old-base64-key>  # for key rotation
memory-service.attachments.encryption.enabled=true
```

## Non-Goals

- **Per-tenant encryption keys**: Deferred to a future enhancement.
- **Re-encryption migration tooling**: An admin endpoint to re-encrypt existing data under a new key. Can be added later.
- **Client-side encryption**: Transparent to API consumers. They upload/download plaintext; the server handles crypto.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Encrypted file store

  Scenario: Upload and download with encryption enabled
    Given encryption is enabled
    And I have a conversation with an entry
    When I upload an attachment with content "hello encrypted world"
    Then the response status should be 200
    When I download the attachment
    Then the response status should be 200
    And the response body should be "hello encrypted world"

  Scenario: SHA-256 digest reflects plaintext not ciphertext
    Given encryption is enabled
    And I have a conversation with an entry
    When I upload an attachment with known content and SHA-256
    Then the attachment SHA-256 should match the plaintext digest

  Scenario: Encrypted attachments cannot be read raw from storage
    Given encryption is enabled
    And I have a conversation with an entry
    When I upload an attachment with content "secret data"
    Then the raw bytes in the file store should not contain "secret data"

  Scenario: Encryption disabled by default
    Given encryption is not enabled
    And I have a conversation with an entry
    When I upload an attachment with content "plain data"
    Then the download succeeds and response body is "plain data"

  Scenario: S3 direct download disabled when encryption is active
    Given encryption is enabled
    And the file store is S3
    When I request a download URL for an attachment
    Then the response should use proxy download not redirect

  Scenario: Server fails to start with encryption and S3 direct download both enabled
    Given encryption is enabled
    And the file store is S3
    And S3 direct download is enabled
    Then the server should fail to start with a configuration error
    And the error message should mention both "encryption" and "direct-download"
```

### Unit Tests

- `EncryptionHeaderTest`: Round-trip `write` → `read`. Verify magic bytes are written. Verify `IOException` on missing or truncated magic. Verify proto fields (version, provider_id, iv) round-trip correctly.
- `DekDataEncryptionProviderTest` (additions): byte[] `encrypt`/`decrypt` round-trip with new header format. `encryptingStream` → `decryptingStream` streaming round-trip. Key rotation via `decryption-keys`. GCM tag failure on tampered ciphertext.
- `EncryptingFileStoreTest`: Full round-trip with mock delegate. Verify delegate receives ciphertext with MSEH magic prefix. Verify GCM authentication failure throws `FileStoreException`.
- `FileStoreSelectorTest`: Encryption wrapping applied only when enabled. Startup fails on S3 + direct-download conflict.

## Tasks

- [ ] Define `EncryptionHeader` proto message in `quarkus-data-encryption` (or hand-code the protobuf encoding for the two fields)
- [ ] Implement `EncryptionHeader` Java class with `read(InputStream)` and `write(OutputStream)`
- [ ] Delete `EncryptionEnvelope.java`
- [ ] Add `encryptingStream(OutputStream)` and `decryptingStream(InputStream, EncryptionHeader)` default methods to `DataEncryptionProvider`
- [ ] Update `DekDataEncryptionProvider.encrypt(byte[])` and `decrypt(byte[])` to use the new header format (via `encryptingStream`/`decryptingStream`)
- [ ] Implement `DekDataEncryptionProvider.encryptingStream` and `decryptingStream`
- [ ] Update `PlainDataEncryptionProvider.encrypt(byte[])` and `decrypt(byte[])` similarly
- [ ] Implement `PlainDataEncryptionProvider.encryptingStream` and `decryptingStream` (passthrough)
- [ ] Remove `EncryptionEnvelope` usage from `DataEncryptionService`; add `encryptingStream(OutputStream)` and `decryptingStream(InputStream)`
- [ ] Update `DataEncryptionService.decrypt(byte[])` to dispatch via `EncryptionHeader` provider ID
- [ ] Implement `EncryptingFileStore` decorator with virtual-thread pipe `store()` and streaming `retrieve()`
- [ ] Add `memory-service.attachments.encryption.enabled` to `AttachmentConfig`
- [ ] Update `FileStoreSelector` to wrap delegate with `EncryptingFileStore` when enabled; add startup validation
- [ ] Add unit tests: `EncryptionHeaderTest`, `DekDataEncryptionProviderTest` additions, `EncryptingFileStoreTest`, `FileStoreSelectorTest`
- [ ] Add Cucumber integration tests for encrypted upload/download round-trip

## Files to Modify

| File | Change |
|------|--------|
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../EncryptionHeader.java` | **New** — magic + 2-byte length + protobuf header |
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../EncryptionEnvelope.java` | **Delete** |
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../DataEncryptionProvider.java` | Add default `encryptingStream` / `decryptingStream` methods |
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../DataEncryptionService.java` | Remove `EncryptionEnvelope` usage; add `encryptingStream` / `decryptingStream` |
| `quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/src/main/java/.../DekDataEncryptionProvider.java` | Update `encrypt`/`decrypt`; implement streaming methods |
| `quarkus/quarkus-data-encryption/runtime/src/main/java/.../PlainDataEncryptionProvider.java` | Update `encrypt`/`decrypt`; implement streaming passthrough |
| `quarkus/quarkus-data-encryption/runtime/src/test/java/.../EncryptionHeaderTest.java` | **New** |
| `quarkus/quarkus-data-encryption/quarkus-data-encryption-dek/src/test/java/.../DekDataEncryptionProviderTest.java` | Add streaming and new-format tests |
| `memory-service/src/main/java/.../attachment/EncryptingFileStore.java` | **New** — streaming decorator |
| `memory-service/src/main/java/.../attachment/FileStoreSelector.java` | Wrap delegate; add startup validation |
| `memory-service/src/main/java/.../attachment/AttachmentConfig.java` | Add `memory-service.attachments.encryption.enabled` |
| `memory-service/src/main/resources/application.properties` | Add `memory-service.attachments.encryption.enabled=false` |
| `memory-service/src/test/resources/features/attachments*.feature` | Add encrypted upload/download scenarios |
| `memory-service/src/test/java/.../attachment/EncryptingFileStoreTest.java` | **New** |
| `memory-service/src/test/java/.../attachment/FileStoreSelectorTest.java` | Add encryption wiring and validation tests |

## Verification

```bash
# Compile
./mvnw compile

# Run tests
./mvnw test -pl memory-service > test.log 2>&1
# Search for failures using Grep tool on test.log
```

## Design Decisions

1. **`EncryptionHeader` replaces `EncryptionEnvelope`**: The envelope embedded the full ciphertext payload as a protobuf `bytes` field, requiring the entire plaintext in memory. The header is only a prefix — magic bytes, 2-byte length, and a small protobuf message — so the ciphertext flows as a plain stream after it. Both byte[] and streaming paths use the same wire format.
2. **`[magic][varint length][proto]` framing**: The 4-byte magic (`MSEH`) provides fast identification and catches accidental decryption of non-encrypted data. The protobuf `uint32` varint length prefix lets `read()` consume exactly the right bytes before handing the stream to the cipher, without a full protobuf streaming parse. Using the same varint encoding as protobuf itself keeps the implementation consistent — `CodedInputStream.readRawVarint32()` reads it directly.
3. **IV in the protobuf header**: Keeping the IV in the proto (field 3) means all decryption context is in one place. Adding future fields (e.g., key version ID for Vault) is a non-breaking proto change.
4. **Provider owns header writing**: Each provider writes its own `EncryptionHeader` (with its own IV, provider ID, version). The service just routes. This is consistent with how `encrypt(byte[])` works today and keeps provider-specific algorithm details (IV length, nonce format) inside the provider.
5. **Streaming via virtual-thread pipe in `store()`**: `FileStore.store()` takes an `InputStream`, not an `OutputStream`, so we can't directly wrap the output side. A `PipedInputStream`/`PipedOutputStream` pair with a virtual thread encrypts on one end while the delegate reads from the other — truly streaming with a small buffer, no full-file copy. Virtual threads make the overhead negligible.
6. **`retrieve()` returns `CipherInputStream`**: Streaming decryption on the read path. GCM authentication tag is verified when the stream is fully consumed; partial reads are allowed but the tag check happens at end-of-stream.
7. **Disable S3 presigned URLs when encrypted**: Stored bytes are ciphertext + header; a presigned URL serves garbage. Startup validation prevents silent misconfiguration.
8. **Key rotation inherits from DEK provider**: `DekDataEncryptionProvider` already iterates `decryptionKeys` on decrypt. The streaming `decryptingStream` does the same — tries each key with the IV from the header until one succeeds.
