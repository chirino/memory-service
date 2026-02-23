package org.acme;

import dev.langchain4j.service.SystemMessage;
import dev.langchain4j.service.UserMessage;
import io.quarkiverse.langchain4j.RegisterAiService;

@RegisterAiService(
        chatMemoryProviderSupplier = RegisterAiService.NoChatMemoryProviderSupplier.class)
public interface SummarizingAssistant {

    @SystemMessage(
            """
            You are a conversation summarizer. Create a concise summary of the conversation \
            history that preserves all key facts, context, and decisions. \
            The summary will replace older messages as context for an ongoing conversation.\
            """)
    @UserMessage(
            """
            Summarize the following conversation history:

            {transcript}
            """)
    String summarize(String transcript);
}
