package org.acme;

import dev.langchain4j.data.message.Content;
import dev.langchain4j.service.MemoryId;
import dev.langchain4j.service.UserMessage;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.test.Mock;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;

@Mock
@ApplicationScoped
public class MockCustomerSupportAgent implements Agent {

    @Override
    public Multi<ChatEvent> chat(
            @MemoryId String memoryId,
            @UserMessage String userMessage,
            @UserMessage List<Content> attachments) {
        return Multi.createFrom()
                .items(
                        new ChatEvent.PartialResponseEvent("Hello"),
                        new ChatEvent.PartialResponseEvent("from"),
                        new ChatEvent.PartialResponseEvent("mock"));
    }
}
