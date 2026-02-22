package io.github.chirino.memory.attachment;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.lang.reflect.Field;
import java.net.URI;
import java.nio.ByteBuffer;
import java.security.SecureRandom;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import memory.service.io.github.chirino.dataencryption.DataEncryptionConfig;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import memory.service.io.github.chirino.dataencryption.EncryptionHeader;
import memory.service.io.github.chirino.dataencryption.PlainDataEncryptionProvider;
import memory.service.io.github.chirino.dataencryption.dek.DekDataEncryptionProvider;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

public class EncryptingFileStoreTest {

    private CaptureFileStore captureStore;
    private DataEncryptionService encryptionService;

    @BeforeEach
    void setUp() throws Exception {
        captureStore = new CaptureFileStore();

        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider dek = createDekProvider(key);
        PlainDataEncryptionProvider plain = new PlainDataEncryptionProvider();

        DataEncryptionConfig config = new SimpleConfig(List.of("dek", "plain"));
        encryptionService = new DataEncryptionService(config, List.of(dek, plain));
        invokeInit(encryptionService);
    }

    @Test
    void storeAndRetrieveRoundTrip() throws Exception {
        EncryptingFileStore store = new EncryptingFileStore(captureStore, encryptionService);

        byte[] plaintext = "hello encrypted world".getBytes();
        FileStoreResult result =
                store.store(new ByteArrayInputStream(plaintext), plaintext.length, "text/plain");

        assertNotNull(result.storageKey());
        // FileStoreResult.size() must reflect plaintext size, not ciphertext size
        assertEquals(plaintext.length, result.size());

        // Retrieve decrypts transparently
        byte[] retrieved;
        try (InputStream is = store.retrieve(result.storageKey())) {
            retrieved = is.readAllBytes();
        }
        assertArrayEquals(plaintext, retrieved);
    }

    @Test
    void delegateReceivesCiphertextWithMsehMagic() throws Exception {
        EncryptingFileStore store = new EncryptingFileStore(captureStore, encryptionService);

        byte[] plaintext = "secret data".getBytes();
        store.store(new ByteArrayInputStream(plaintext), plaintext.length, "text/plain");

        // Raw bytes in the delegate should start with MSEH magic
        byte[] storedBytes = captureStore.getLastStoredBytes();
        assertNotNull(storedBytes);
        int magic = ByteBuffer.wrap(storedBytes, 0, 4).getInt();
        assertEquals(EncryptionHeader.MAGIC, magic);
    }

    @Test
    void delegateDoesNotContainPlaintext() throws Exception {
        EncryptingFileStore store = new EncryptingFileStore(captureStore, encryptionService);

        byte[] plaintext = "secret data".getBytes();
        store.store(new ByteArrayInputStream(plaintext), plaintext.length, "text/plain");

        byte[] storedBytes = captureStore.getLastStoredBytes();
        // The stored bytes should not contain the plaintext in raw form
        assertFalse(
                containsSubsequence(storedBytes, plaintext),
                "Stored bytes must not contain plaintext");
    }

    @Test
    void getSignedUrlAlwaysReturnsEmpty() throws Exception {
        EncryptingFileStore store = new EncryptingFileStore(captureStore, encryptionService);
        Optional<URI> url = store.getSignedUrl("any-key", Duration.ofHours(1));
        assertTrue(url.isEmpty(), "EncryptingFileStore must not return presigned URLs");
    }

    @Test
    void deleteForwardsToDelegatee() throws Exception {
        EncryptingFileStore store = new EncryptingFileStore(captureStore, encryptionService);
        store.delete("some-key");
        assertEquals("some-key", captureStore.getLastDeletedKey());
    }

    // ── Helpers ──────────────────────────────────────────────────────────────

    private static boolean containsSubsequence(byte[] haystack, byte[] needle) {
        outer:
        for (int i = 0; i <= haystack.length - needle.length; i++) {
            for (int j = 0; j < needle.length; j++) {
                if (haystack[i + j] != needle[j]) continue outer;
            }
            return true;
        }
        return false;
    }

    /** In-memory FileStore that captures stored bytes for inspection. */
    private static final class CaptureFileStore implements FileStore {

        private final List<byte[]> stored = new ArrayList<>();
        private String lastDeletedKey;

        @Override
        public FileStoreResult store(InputStream data, long maxSize, String contentType)
                throws FileStoreException {
            try {
                byte[] bytes = data.readAllBytes();
                stored.add(bytes);
                return new FileStoreResult("key-" + stored.size(), bytes.length);
            } catch (Exception e) {
                throw FileStoreException.storageError("capture failed", e);
            }
        }

        @Override
        public InputStream retrieve(String storageKey) throws FileStoreException {
            int idx = Integer.parseInt(storageKey.substring("key-".length())) - 1;
            return new ByteArrayInputStream(stored.get(idx));
        }

        @Override
        public void delete(String storageKey) {
            lastDeletedKey = storageKey;
        }

        @Override
        public Optional<URI> getSignedUrl(String storageKey, Duration expiry) {
            return Optional.empty();
        }

        byte[] getLastStoredBytes() {
            return stored.isEmpty() ? null : stored.get(stored.size() - 1);
        }

        String getLastDeletedKey() {
            return lastDeletedKey;
        }
    }

    private static DekDataEncryptionProvider createDekProvider(byte[] key) throws Exception {
        DekDataEncryptionProvider provider = new DekDataEncryptionProvider();
        Field field = DekDataEncryptionProvider.class.getDeclaredField("dekKey");
        field.setAccessible(true);
        field.set(provider, key);
        java.lang.reflect.Method init = DekDataEncryptionProvider.class.getDeclaredMethod("init");
        init.setAccessible(true);
        init.invoke(provider);
        return provider;
    }

    private static void invokeInit(DataEncryptionService service) throws Exception {
        java.lang.reflect.Method m = DataEncryptionService.class.getDeclaredMethod("init");
        m.setAccessible(true);
        m.invoke(service);
    }

    private static final class SimpleConfig implements DataEncryptionConfig {

        private final List<String> providers;

        SimpleConfig(List<String> providers) {
            this.providers = providers;
        }

        @Override
        public List<String> providers() {
            return providers;
        }

        @Override
        public java.util.Map<String, ProviderConfig> provider() {
            java.util.Map<String, ProviderConfig> map = new java.util.HashMap<>();
            for (String id : providers) {
                map.put(id, new SimpleProviderConfig(id));
            }
            return map;
        }

        private static final class SimpleProviderConfig implements ProviderConfig {

            private final String type;

            SimpleProviderConfig(String type) {
                this.type = type;
            }

            @Override
            public String type() {
                return type;
            }

            @Override
            public boolean enabled() {
                return true;
            }
        }
    }
}
