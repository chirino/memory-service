package org.acme;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import dev.langchain4j.data.message.TextContent;
import dev.langchain4j.data.message.UserMessage;
import dev.langchain4j.rag.content.Content;
import java.util.List;
import org.junit.jupiter.api.Test;

class CognitionMemoryContentInjectorTest {

    @Test
    void injectsDurableContextBeforeOriginalUserContent() {
        CognitionMemoryContentInjector injector = new CognitionMemoryContentInjector();
        UserMessage original = UserMessage.from("Write Java code");

        UserMessage injected =
                (UserMessage)
                        injector.inject(
                                List.of(Content.from("Durable user memory\nmemory: Prefer Java")),
                                original);

        assertEquals(2, injected.contents().size());
        String context = ((TextContent) injected.contents().get(0)).text();
        assertTrue(context.contains("Durable context from previous conversations"));
        assertTrue(context.contains("Prefer Java"));
        assertEquals("Write Java code", ((TextContent) injected.contents().get(1)).text());
    }
}
