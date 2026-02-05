package example;

import dev.langchain4j.service.MemoryId;
import io.quarkiverse.langchain4j.RegisterAiService;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
@RegisterAiService()
public interface Agent {

    Multi<String> chat(@MemoryId String memoryId, String userMessage);

    Multi<ChatEvent> chatDetailed(@MemoryId String memoryId, String userMessage);
}
