package org.acme;

import dev.langchain4j.web.search.WebSearchEngine;
import dev.langchain4j.web.search.WebSearchTool;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Singleton;

@ApplicationScoped
public class TavilyWebSearchToolProducer {

    @Produces
    @Singleton
    WebSearchTool webSearchTool(WebSearchEngine webSearchEngine) {
        return WebSearchTool.from(webSearchEngine);
    }
}
