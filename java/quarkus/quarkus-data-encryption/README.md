# quarkus-data-encryption Extension

Configurable, provider-based data encryption for Quarkus applications, using a Protobuf envelope and pluggable key management (local DEK, Vault, AWS KMS-ready).

This module is a Quarkus extension meant to be added to other Quarkus apps (including this repo’s services) to encrypt arbitrary byte payloads (e.g. columns, blobs, documents) with support for rotation and gradual migration.

## Modules

The extension is split into several Maven modules:

- `quarkus-data-encryption` (runtime)
  - Core SPI (`DataEncryptionProvider`, `KeyEncryptionService`).
  - Protobuf envelope (`EncryptionEnvelope`).
  - Provider registry and `DataEncryptionService`.
  - Config mapping (`DataEncryptionConfig`).
- `quarkus-data-encryption-dek`
  - Local AES-256-GCM provider using a configured DEK.
  - Supports multiple decryption keys for key rotation.
- `quarkus-data-encryption-vault`
  - Vault Transit-based `KeyEncryptionService` using Quarkiverse Vault.
- `quarkus-data-encryption-aws-kms`
  - Placeholder for AWS KMS-based `KeyEncryptionService` (no implementation yet).

## Dependency Setup

In a consuming Quarkus app’s `pom.xml`:

```xml
<dependencies>
  <!-- Core extension -->
  <dependency>
    <groupId>io.github.chirino.memory-service</groupId>
    <artifactId>quarkus-data-encryption</artifactId>
    <version>${project.version}</version>
  </dependency>

  <!-- Optional providers -->
  <dependency>
    <groupId>io.github.chirino.memory-service</groupId>
    <artifactId>quarkus-data-encryption-dek</artifactId>
    <version>${project.version}</version>
  </dependency>

  <dependency>
    <groupId>io.github.chirino.memory-service</groupId>
    <artifactId>quarkus-data-encryption-vault</artifactId>
    <version>${project.version}</version>
  </dependency>
</dependencies>
```

> Note: until this extension is published to a public Maven repository, it is intended to be used from the same multi-module build (as in this repo).

## Configuration Model

The extension is configured via `memory-service.encryption.*` properties (used as the config prefix when consumed by Memory Service).

### Provider Ordering and Types

```properties
# Logical provider IDs in order of preference
memory-service.encryption.providers=a,b

# Logical provider "a" uses the local DEK provider
memory-service.encryption.provider.a.type=dek

# Logical provider "b" is a pass-through provider
memory-service.encryption.provider.b.type=plain
```

Semantics:

- `memory-service.encryption.providers` is an ordered list of **logical provider IDs** (for example: `a`, `b`, `primary`, `fallback`).
- For each logical ID, `memory-service.encryption.provider.<id>.type` selects the concrete provider implementation (`dek`, `plain`, etc.).
- The first logical provider that is enabled and has a matching implementation is used for **new encrypt operations**.
- For ciphertext:
  - If it is a Protobuf envelope, the `provider_id` in the envelope is the logical provider ID used at encrypt time and is used to select the provider for decrypt.
  - If it is not an envelope (legacy/plain data), `DataEncryptionService` tries providers in configured order until one succeeds or falls back to returning the raw bytes.

Notes:

- The provider `type` must match the `id()` of a `DataEncryptionProvider` implementation (for example, `PlainDataEncryptionProvider.id() == "plain"`, `DekDataEncryptionProvider.id() == "dek"`).
- For backwards compatibility, providers are also registered by their implementation IDs (`plain`, `dek`, …), so envelopes written with those IDs before this change continue to decrypt correctly.

### Local DEK Provider Configuration

Module: `quarkus-data-encryption-dek`

Provider ID: `dek`

```properties
# Primary 32-byte AES-256 DEK (Base64-encoded)
memory-service.encryption.dek.key=BASE64_PRIMARY_KEY

# Optional additional decryption keys (Base64-encoded),
# used only for decrypt; useful during rotation.
memory-service.encryption.dek.decryption-keys=BASE64_OLD_KEY_1,BASE64_OLD_KEY_2
```

Behaviour:

- All new data encrypted by the `dek` provider uses `memory-service.encryption.dek.key` (decoded from Base64).
- On decrypt, the provider tries:
  - Primary key, then
  - Each key from `memory-service.encryption.dek.decryption-keys` (decoded from Base64).
- If none can decrypt, it throws `DecryptionFailedException`, allowing higher-level fallback (e.g. to `plain`).

### Plain Provider

The `plain` provider is built into the runtime module (`PlainDataEncryptionProvider`) and simply returns the input unchanged.

Configure it by mapping a logical provider ID to the `plain` type:

```properties
memory-service.encryption.providers=b
memory-service.encryption.provider.b.type=plain
```

This is useful both for:

- Legacy/plain data during migration.
- Testing or development environments where encryption is disabled.

### Vault Transit Provider (Key Encryption)

Module: `quarkus-data-encryption-vault`

This module provides a `KeyEncryptionService` implementation (`VaultKeyEncryptionService`) that uses the Quarkiverse Vault extension (`io.quarkiverse.vault:quarkus-vault`) and the Vault Transit secrets engine.

It is meant to be combined with a DEK-based data provider (e.g. `dek`), where:

- Vault manages the KEK.
- Local code only handles encrypted DEKs (from Vault) and ephemeral plaintext DEKs in memory.

Key properties:

```properties
# Name of the Transit key used to wrap/unwrap DEKs
memory-service.encryption.vault.transit-key=app-data

# Standard Quarkus Vault configuration (examples)
quarkus.vault.url=http://localhost:8200
quarkus.vault.authentication.token=dev-root-token
quarkus.vault.transit.enabled=true
```

Behaviour (`VaultKeyEncryptionService`):

- `generateDek()`:
  - Calls `VaultTransitSecretEngine.generateDataKey(VaultTransitDataKeyType.wrapped, transitKey, bits=256)`.
  - Returns a `GeneratedDek` whose:
    - `plaintextDek` is the decoded plaintext from Vault.
    - `encryptedDek` is the Vault ciphertext as UTF-8 bytes.
- `decryptDek(byte[] encryptedDek)`:
  - Calls `VaultTransitSecretEngine.decrypt(transitKey, ciphertext)` to get the plaintext DEK bytes.

How you combine this with an actual data provider (e.g. storing `encryptedDek` alongside ciphertext in a table) is up to the consuming application.

## Using the DataEncryptionService

The main entry point for applications is `DataEncryptionService` in the runtime module.

Inject it into your Quarkus application code:

```java
import jakarta.inject.Inject;
import jakarta.enterprise.context.ApplicationScoped;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;

@ApplicationScoped
public class EncryptedStore {

    @Inject
    DataEncryptionService encryptionService;

    public byte[] toEncryptedBytes(byte[] plain) {
        return encryptionService.encrypt(plain);
    }

    public byte[] fromEncryptedBytes(byte[] stored) {
        return encryptionService.decrypt(stored);
    }
}
```

`encrypt` always wraps the ciphertext in the Protobuf `EncryptionEnvelope` with:

- `version` (currently `1`).
- `provider_id` (the provider used for encryption).
- `payload` (provider-specific ciphertext).

`decrypt`:

- Detects and parses an envelope when present.
- Selects the provider based on `provider_id`.
- Falls back for legacy/plain payloads as described earlier.

## Rotation and Migration

Typical rotation scenario:

1. Start with a plain provider:

   ```properties
   memory-service.encryption.providers=a
   memory-service.encryption.provider.a.type=plain
   ```

2. Introduce DEK encryption for new data:

   ```properties
   # Logical provider "enc" uses DEK, "plain" remains as fallback
   memory-service.encryption.providers=enc,plain
   memory-service.encryption.provider.enc.type=dek
   memory-service.encryption.provider.plain.type=plain

   memory-service.encryption.dek.key=<current-dek>
   ```

3. Rotate DEK without changing provider IDs:

   - Update `memory-service.encryption.dek.key` to the new DEK.
   - Add the old DEK to `memory-service.encryption.dek.decryption-keys`.
   - Existing ciphertext remains decryptable; new ciphertext uses the new key.

4. Optional background job:

   - Walk stored data, decrypt using `DataEncryptionService`, re-encrypt with the current configuration, and persist.

Because the envelope contains the provider ID, you can also introduce entirely new providers (e.g. a future `kms` provider) and re-order `memory-service.encryption.providers` for new data while continuing to decrypt existing envelopes using the recorded provider ID.

## Testing Notes

- Unit tests exist for:
  - Plain provider behaviour.
  - DEK provider encryption/decryption, failure behaviour, and multiple-decryption-key support.
  - Service-level fallback behaviour with DEK + plain providers.
- The Vault module uses the Quarkiverse Vault API and is designed to be exercised against a real Vault instance (for example, via Testcontainers) in an integration-test module. No integration tests are included here yet.
