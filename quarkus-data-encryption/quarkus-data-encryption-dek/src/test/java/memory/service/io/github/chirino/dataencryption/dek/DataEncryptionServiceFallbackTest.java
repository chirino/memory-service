package memory.service.io.github.chirino.dataencryption.dek;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;

import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.security.SecureRandom;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import memory.service.io.github.chirino.dataencryption.DataEncryptionConfig;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import memory.service.io.github.chirino.dataencryption.PlainDataEncryptionProvider;
import org.junit.jupiter.api.Test;

public class DataEncryptionServiceFallbackTest {

    @Test
    void encryptsWithFirstProviderAndDecryptsWithEnvelope() throws Exception {
        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider dek = createProviderWithKey(key);
        PlainDataEncryptionProvider plain = new PlainDataEncryptionProvider();

        TestConfig config = new TestConfig(List.of("dek", "plain"));
        DataEncryptionService service = new DataEncryptionService(config, List.of(dek, plain));
        invokeInit(service);

        byte[] plaintext = "envelope-test".getBytes();
        byte[] ciphertext = service.encrypt(plaintext);

        // Envelope should change the bytes
        assertNotEquals(new String(plaintext), new String(ciphertext));

        byte[] decrypted = service.decrypt(ciphertext);
        assertArrayEquals(plaintext, decrypted);
    }

    @Test
    void fallsBackToPlainForLegacyData() throws Exception {
        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider dek = createProviderWithKey(key);
        PlainDataEncryptionProvider plain = new PlainDataEncryptionProvider();

        TestConfig config = new TestConfig(List.of("dek", "plain"));
        DataEncryptionService service = new DataEncryptionService(config, List.of(dek, plain));
        invokeInit(service);

        byte[] legacyData = "legacy-plain-data".getBytes();

        // Not wrapped in an envelope, so service should try providers in order.
        byte[] decrypted = service.decrypt(legacyData);

        // DEK provider should fail fast, plain provider should act as identity.
        assertArrayEquals(legacyData, decrypted);
    }

    private DekDataEncryptionProvider createProviderWithKey(byte[] key) throws Exception {
        DekDataEncryptionProvider provider = new DekDataEncryptionProvider();
        Field field = DekDataEncryptionProvider.class.getDeclaredField("dekKey");
        field.setAccessible(true);
        field.set(provider, key);
        provider.init();
        return provider;
    }

    private void invokeInit(DataEncryptionService service) throws Exception {
        Method m = DataEncryptionService.class.getDeclaredMethod("init");
        m.setAccessible(true);
        m.invoke(service);
    }

    private static final class TestConfig implements DataEncryptionConfig {

        private final List<String> providers;
        private final Map<String, ProviderConfig> providerConfigs = new HashMap<>();

        private TestConfig(List<String> providers) {
            this.providers = providers;
            for (String id : providers) {
                providerConfigs.put(id, new SimpleProviderConfig(id));
            }
        }

        @Override
        public List<String> providers() {
            return providers;
        }

        @Override
        public Map<String, ProviderConfig> provider() {
            return providerConfigs;
        }

        private static final class SimpleProviderConfig implements ProviderConfig {

            private final String type;

            private SimpleProviderConfig(String type) {
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
