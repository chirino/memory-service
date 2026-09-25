package org.acme;

import dev.langchain4j.web.search.WebSearchEngine;
import dev.langchain4j.web.search.WebSearchTool;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Singleton;

/**
 * Produces the {@link WebSearchTool} used by {@link SubAgent}.
 *
 * <p>Gotcha: with {@code quarkus-langchain4j-tavily}, a second unqualified default {@code
 * WebSearchTool} bean next to the one Quarkus LangChain4j already provides makes AI-service tool
 * injection ambiguous. {@code chat-quarkus} dropped its producer for that reason; qualify this one
 * or remove it if injection becomes ambiguous.
 */
@ApplicationScoped
public class TavilyWebSearchToolProducer {

    @Produces
    @Singleton
    WebSearchTool webSearchTool(WebSearchEngine webSearchEngine) {
        return WebSearchTool.from(webSearchEngine);
    }
}
