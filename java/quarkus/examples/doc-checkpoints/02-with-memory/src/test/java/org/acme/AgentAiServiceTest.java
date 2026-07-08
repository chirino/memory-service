package org.acme;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNotNull;

import dev.langchain4j.memory.chat.ChatMemoryProvider;
import dev.langchain4j.store.memory.chat.ChatMemoryStore;
import dev.langchain4j.store.memory.chat.InMemoryChatMemoryStore;
import io.github.chirino.memory.langchain4j.MemoryService;
import io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryStore;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.QuarkusTestProfile;
import io.quarkus.test.junit.TestProfile;
import jakarta.inject.Inject;
import java.util.Map;
import java.util.UUID;
import org.junit.jupiter.api.Test;

/**
 * Tests for the two chat-memory modes supported by the memory-service extension:
 *
 * <ul>
 *   <li>Global mode ({@code memory-service.chat-memory.enabled=true}): every
 *       {@code @RegisterAiService} uses Memory Service-backed chat memory by default.
 *   <li>Selective mode ({@code memory-service.chat-memory.enabled=false}): Quarkiverse keeps its
 *       default in-memory store; individual agents opt in via {@code chatMemoryProviderSupplier =
 *       MemoryService.class}.
 * </ul>
 */
@QuarkusTest
@QuarkusTestResource(MockOpenAiTestResource.class)
// Default test profile uses memory-service.chat-memory.enabled=false (see
// test/resources/application.properties)
class AgentAiServiceTest {

    @Inject Agent agent;
    @Inject SelectiveAgent selectiveAgent;
    @Inject NoMemoryAgent noMemoryAgent;
    @Inject ChatMemoryStore chatMemoryStore;
    @Inject MemoryService memoryService;

    /**
     * When {@code memory-service.chat-memory.enabled=false} (selective mode), the global CDI
     * {@link ChatMemoryStore} bean should be Quarkiverse's default {@link InMemoryChatMemoryStore}.
     */
    @Test
    void testDisabledMode_globalStoreIsInMemory() {
        assertInstanceOf(
                InMemoryChatMemoryStore.class,
                chatMemoryStore,
                "With enabled=false, global ChatMemoryStore should be InMemoryChatMemoryStore");
    }

    /**
     * Plain {@code @RegisterAiService} (no explicit {@code chatMemoryProviderSupplier}) uses the
     * global store, which is in-memory when {@code enabled=false}.
     */
    @Test
    void testDisabledMode_plainAgentUsesInMemoryStore() {
        String result = agent.chat(UUID.randomUUID().toString(), "hi");
        assertEquals("test-response", result);
    }

    /**
     * {@code @RegisterAiService(chatMemoryProviderSupplier = MemoryService.class)} opts into
     * Memory Service-backed memory even when the global mode is disabled. The {@link MemoryService}
     * bean must be injectable and its {@link MemoryService#get()} must return a non-null provider.
     */
    @Test
    void testSelectiveMode_memoryServiceSupplierIsInjectable() {
        assertNotNull(memoryService, "MemoryService CDI bean must be injectable");
        ChatMemoryProvider provider = memoryService.get();
        assertNotNull(provider, "MemoryService.get() must return a non-null ChatMemoryProvider");
    }

    /**
     * {@code @RegisterAiService(chatMemoryProviderSupplier = NoChatMemoryProviderSupplier.class)}
     * agents must still respond without any memory.
     */
    @Test
    void testNoMemoryAgent_respondsWithoutMemory() {
        String result = noMemoryAgent.chat("hi");
        assertEquals("test-response", result);
    }
}

/**
 * Separate test class that enables global mode ({@code memory-service.chat-memory.enabled=true})
 * and verifies the {@link MemoryServiceChatMemoryStore} becomes the active CDI
 * {@link ChatMemoryStore} bean.
 */
@QuarkusTest
@TestProfile(GlobalMemoryModeTest.GlobalMemoryProfile.class)
@QuarkusTestResource(MockOpenAiTestResource.class)
class GlobalMemoryModeTest {

    public static class GlobalMemoryProfile implements QuarkusTestProfile {
        @Override
        public Map<String, String> getConfigOverrides() {
            return Map.of(
                    "memory-service.chat-memory.enabled", "true",
                    // Point to a non-existent URL so startup succeeds but actual calls would fail
                    // fast
                    "memory-service.client.url", "http://localhost:19999");
        }
    }

    @Inject ChatMemoryStore chatMemoryStore;

    /**
     * When {@code memory-service.chat-memory.enabled=true}, the global {@link ChatMemoryStore}
     * bean should be the {@link MemoryServiceChatMemoryStore}.
     */
    @Test
    void testEnabledMode_globalStoreIsMemoryService() {
        assertInstanceOf(
                MemoryServiceChatMemoryStore.class,
                chatMemoryStore,
                "With enabled=true, global ChatMemoryStore should be MemoryServiceChatMemoryStore");
    }
}
