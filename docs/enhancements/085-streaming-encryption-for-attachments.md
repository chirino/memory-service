---
status: proposed
---

# Enhancement 085: Streaming Encryption for Attachments

> **Status**: Proposed.

## Summary

Replace the in-memory `bytes.Buffer` buffering in the encrypted attachment store with streaming AES-CTR encryption so that large attachments do not require holding two full copies (plaintext + ciphertext) in RAM simultaneously.

## Motivation

The current `EncryptStore.Store()` in `internal/plugin/attach/encrypt/encrypt.go` reads the entire plaintext into a `bytes.Buffer`, computes SHA-256, then encrypts into a second `bytes.Buffer` before passing it to the inner store. For a 100 MB attachment, this means ~200 MB of heap allocation per concurrent upload.

This is a scalability concern:
- **Memory pressure**: Multiple concurrent uploads of large files can cause OOM or excessive GC pauses.
- **Artificial limits**: The `maxSize` config is effectively capped by available RAM rather than by disk/object-store capacity.
- **Production risk**: Under load, a burst of large uploads could degrade the entire service.

The root cause is the choice of AES-GCM as the cipher mode (see background below).

### Background: AES-GCM vs AES-CTR

Both AES-GCM and AES-CTR use the same underlying AES block cipher, but they differ in how they handle data and what guarantees they provide:

**AES-GCM** (Galois/Counter Mode) is an _authenticated encryption_ (AEAD) mode. It encrypts data using a counter-based stream (like CTR) but also computes an authentication tag — a cryptographic checksum over the entire ciphertext. This tag lets the decryptor verify that the data has not been tampered with. The catch is that the tag can only be produced after processing _all_ of the plaintext, which means the entire input must be buffered before the final encrypted output (with tag appended) can be written. This is what forces the current implementation to hold two full copies in memory.

**AES-CTR** (Counter Mode) is a pure _stream cipher_ mode. It generates a keystream from the AES key and a nonce/counter, then XORs it with the plaintext byte-by-byte. Each byte of output depends only on the corresponding byte of input and the keystream position — there is no final tag or summary over the whole message. This means encryption and decryption can both operate in a true streaming fashion: bytes in, bytes out, with constant memory usage regardless of file size.

The tradeoff is that AES-CTR provides **confidentiality only** — it does not detect tampering. AES-GCM provides both confidentiality and integrity. For this use case (at-rest encryption of attachments in a backing store), integrity verification is not needed: an attacker with write access to the store could just as easily delete files as modify ciphertext, so the authentication tag provides no practical benefit.

## Design

Switch from AES-GCM to AES-CTR for attachment encryption, enabling true single-pass streaming for both `Store` and `Retrieve` with constant memory usage.

**Wire format (MSEH v2):**

```
[MSEH header: Version=2, ProviderID, Nonce (16 bytes)]
[AES-CTR ciphertext bytes... streamed]
```

No trailing HMAC or authentication tag. The ciphertext length equals the plaintext length (no padding).

**Encryption (Store):**

```go
func (s *EncryptStore) Store(ctx context.Context, data io.Reader, maxSize int64, contentType string) (*registryattach.FileStoreResult, error) {
    limited := io.LimitReader(data, maxSize+1)
    hasher := sha256.New()

    // Set up the MSEH v2 header + AES-CTR stream via the provider.
    // The provider writes the header to pr, then wraps it in CTR encryption.
    pr, pw := io.Pipe()

    var encErr error
    go func() {
        defer pw.Close()
        enc, err := s.svc.EncryptStream(pw) // writes MSEH header, returns CTR writer
        if err != nil {
            pw.CloseWithError(err)
            return
        }
        // Tee plaintext through SHA-256 hasher while encrypting.
        n, err := io.Copy(enc, io.TeeReader(limited, hasher))
        if err != nil {
            pw.CloseWithError(err)
            return
        }
        if n > maxSize {
            pw.CloseWithError(fmt.Errorf("file exceeds maximum size of %d bytes", maxSize))
            return
        }
        if err := enc.Close(); err != nil {
            pw.CloseWithError(err)
        }
    }()

    result, err := s.inner.Store(ctx, pr, -1, contentType) // size unknown, stream
    if err != nil {
        return nil, err
    }
    result.SHA256 = fmt.Sprintf("%x", hasher.Sum(nil))
    return result, nil
}
```

**Decryption (Retrieve):** Already streaming — `DecryptStream` reads the MSEH header, then returns an `io.Reader` that decrypts AES-CTR on the fly. No buffering needed.

**Dual-version decryption:** The MSEH header `Version` field distinguishes v1 (AES-GCM, existing) from v2 (AES-CTR). Decryption reads the version and routes accordingly, so existing encrypted attachments continue to work.

**Provider changes:** Each encryption provider (`dek`, `vault`, `awskms`) needs an `EncryptStreamV2` / updated `EncryptStream` that uses AES-CTR instead of AES-GCM, and `DecryptStream` needs to handle both versions based on the header.

## Testing

### Unit Tests

```gherkin
Feature: Streaming encrypted attachment store (AES-CTR v2)

  Scenario: Encrypt and decrypt round-trip with v2 format
    Given encryption is configured with the dek provider
    When I store an attachment of 5MB
    Then the attachment is stored successfully
    And the stored data starts with an MSEH v2 header
    And the attachment can be retrieved and decrypted to the original content

  Scenario: Decrypt v1 (AES-GCM) attachment after upgrade
    Given an attachment was previously encrypted with MSEH v1
    When I retrieve the attachment
    Then the attachment is decrypted successfully using the v1 code path

  Scenario: Attachment exceeding max size is rejected
    Given the max attachment size is 10MB
    When I store an attachment of 15MB
    Then the store returns a size exceeded error

  Scenario: Large attachment does not spike memory
    Given encryption is configured with the dek provider
    When I store an attachment of 50MB
    Then heap allocations during store do not exceed 5MB
```

### Existing Tests

The existing encrypted attachment BDD tests (`cucumber_pg_encrypted_test.go`, `cucumber_sqlite_encrypted_test.go`) should continue to pass without modification, validating v1 backward compatibility.

## Tasks

- [ ] Add MSEH v2 (AES-CTR) support to `encrypt.Provider` interface
- [ ] Implement AES-CTR `EncryptStream`/`DecryptStream` in `dek` provider
- [ ] Implement AES-CTR `EncryptStream`/`DecryptStream` in `vault` provider
- [ ] Implement AES-CTR `EncryptStream`/`DecryptStream` in `awskms` provider
- [ ] Update `dataencryption.Service` to route v1 vs v2 on decryption
- [ ] Rewrite `EncryptStore.Store()` to use streaming (no buffering)
- [ ] Add unit tests for v2 encrypt/decrypt round-trip
- [ ] Verify existing v1 encrypted attachments still decrypt (backward compat)
- [ ] Verify existing encrypted attachment BDD tests still pass

## Files to Modify

| File | Change |
|------|--------|
| `internal/plugin/attach/encrypt/encrypt.go` | Replace buffered Store with streaming AES-CTR via pipe |
| `internal/registry/encrypt/plugin.go` | Update `Provider` interface if needed for v2 stream methods |
| `internal/plugin/encrypt/dek/dek.go` | Add AES-CTR encrypt/decrypt stream support |
| `internal/plugin/encrypt/vault/vault.go` | Add AES-CTR encrypt/decrypt stream support |
| `internal/plugin/encrypt/awskms/awskms.go` | Add AES-CTR encrypt/decrypt stream support |
| `internal/dataencryption/service.go` | Route v1/v2 in `DecryptStream` based on header version |
| `internal/dataencryption/mseh.go` | Document v2 format constants if needed |
| `internal/plugin/attach/encrypt/encrypt_test.go` | Add v2 round-trip and backward compat tests |

## Verification

```bash
# Compile
go build ./...

# Run encrypted attachment tests
go test ./internal/plugin/attach/encrypt/ -v -count=1

# Run BDD tests with encryption
go test ./internal/bdd -run TestFeaturesPgEncrypted -count=1
go test ./internal/bdd -run TestFeaturesSqliteEncrypted -count=1
```

## Design Decisions

- **AES-CTR over AES-GCM**: AES-CTR eliminates buffering entirely — no temp files, no memory spikes, true streaming on both encrypt and decrypt paths.
- **No authentication tag (no HMAC)**: The threat model is at-rest protection. An attacker with write access to the backing store can delete data just as easily as tampering with ciphertext. AEAD adds complexity (chunking or deferred verification) without meaningful security benefit in this context.
- **Dual-version support**: The MSEH header already has a `Version` field. V1 (AES-GCM) continues to work for existing data. New attachments use v2 (AES-CTR). No migration required.
