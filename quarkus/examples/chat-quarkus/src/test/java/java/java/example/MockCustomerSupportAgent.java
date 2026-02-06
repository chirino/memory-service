package example;

import dev.langchain4j.service.MemoryId;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.test.Mock;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;

@Mock
@ApplicationScoped
public class MockCustomerSupportAgent implements Agent {

    @Override
    public Multi<String> chat(@MemoryId String memoryId, String userMessage) {
        return Multi.createFrom().items("Hello ", "from ", "mock ");
    }

    @Override
    public Multi<ChatEvent> chatDetailed(String memoryId, String userMessage) {
        return Multi.createFrom()
                .items(
                        new ChatEvent.PartialResponseEvent("Hello"),
                        new ChatEvent.PartialResponseEvent("from"),
                        new ChatEvent.PartialResponseEvent("mock"));
    }
}
