package io.github.chirino.memory.langchain4j;

import dev.langchain4j.memory.ChatMemory;
import dev.langchain4j.memory.chat.ChatMemoryProvider;
import dev.langchain4j.memory.chat.MessageWindowChatMemory;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.function.Supplier;
import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * A {@link Supplier} of {@link ChatMemoryProvider} backed by the Memory Service.
 *
 * <p>Use this as the {@code chatMemoryProviderSupplier} on a specific {@code @RegisterAiService}
 * when the global chat memory store is disabled ({@code memory-service.chat-memory.enabled=false})
 * but you want selected agents to use durable Memory Service-backed chat memory:
 *
 * <pre>{@code
 * @RegisterAiService(chatMemoryProviderSupplier = MemoryService.class)
 * public interface DurableAgent {
 *     String chat(@MemoryId String conversationId, String message);
 * }
 * }</pre>
 */
@ApplicationScoped
public class MemoryService implements Supplier<ChatMemoryProvider> {

    private final MemoryServiceChatMemoryStoreFactory storeFactory;
    private final int maxMessages;

    @Inject
    public MemoryService(
            MemoryServiceChatMemoryStoreFactory storeFactory,
            @ConfigProperty(
                            name = "quarkus.langchain4j.chat-memory.memory-window.max-messages",
                            defaultValue = "10")
                    int maxMessages) {
        this.storeFactory = storeFactory;
        this.maxMessages = maxMessages;
    }

    @Override
    public ChatMemoryProvider get() {
        MemoryServiceChatMemoryStore store = storeFactory.get();
        return new ChatMemoryProvider() {
            @Override
            public ChatMemory get(Object memoryId) {
                return MessageWindowChatMemory.builder()
                        .maxMessages(maxMessages)
                        .id(memoryId)
                        .chatMemoryStore(store)
                        .build();
            }
        };
    }
}
