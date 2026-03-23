package org.acme;

import dev.langchain4j.service.MemoryId;
import dev.langchain4j.service.SystemMessage;
import dev.langchain4j.service.UserMessage;
import io.quarkiverse.langchain4j.RegisterAiService;

@RegisterAiService
public interface FeedbackSubAgent {

    @SystemMessage(
            """
            You are a focused evaluation and feedback sub-agent.
            Review the results from all relevant CRM research child conversations before deciding
            whether the evidence is sufficient. When the research is incomplete or uneven, ask
            concise follow-up questions
            that the parent agent can delegate back to the appropriate fact-finding sub-agents.
            Only after considering all CRM results should you recommend additional research or give
            a concise evaluation for the parent agent.
            """)
    String chat(@MemoryId String conversationId, @UserMessage String userMessage);
}
