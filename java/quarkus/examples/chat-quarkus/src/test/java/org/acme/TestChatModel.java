package org.acme;

import dev.langchain4j.data.message.AiMessage;
import dev.langchain4j.data.message.ChatMessage;
import dev.langchain4j.model.ModelProvider;
import dev.langchain4j.model.chat.ChatModel;
import dev.langchain4j.model.chat.request.ChatRequest;
import dev.langchain4j.model.chat.request.ChatRequestParameters;
import dev.langchain4j.model.chat.response.ChatResponse;
import dev.langchain4j.model.chat.response.ChatResponseMetadata;
import dev.langchain4j.model.output.FinishReason;
import dev.langchain4j.model.output.TokenUsage;
import jakarta.annotation.Priority;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Alternative;
import java.util.Collections;
import java.util.List;
import java.util.Set;

@ApplicationScoped
@Alternative
@Priority(1)
public class TestChatModel implements ChatModel {

    @Override
    public ChatResponse chat(ChatRequest chatRequest) {
        AiMessage aiMessage = AiMessage.from("test-response");
        ChatResponseMetadata metadata = ChatResponseMetadata.builder().build();
        TokenUsage tokenUsage = null;
        return ChatResponse.builder()
                .aiMessage(aiMessage)
                .metadata(metadata)
                .id("test-id")
                .modelName("test-model")
                .tokenUsage(tokenUsage)
                .finishReason(FinishReason.STOP)
                .build();
    }

    @Override
    public ChatResponse doChat(ChatRequest chatRequest) {
        return chat(chatRequest);
    }

    @Override
    public ChatRequestParameters defaultRequestParameters() {
        return null;
    }

    @Override
    public List<dev.langchain4j.model.chat.listener.ChatModelListener> listeners() {
        return Collections.emptyList();
    }

    @Override
    public ModelProvider provider() {
        return ModelProvider.OTHER;
    }

    @Override
    public String chat(String userMessage) {
        return "test-response";
    }

    @Override
    public ChatResponse chat(ChatMessage... messages) {
        return chat((ChatRequest) null);
    }

    @Override
    public ChatResponse chat(List<ChatMessage> messages) {
        return chat((ChatRequest) null);
    }

    @Override
    public Set<dev.langchain4j.model.chat.Capability> supportedCapabilities() {
        return Collections.emptySet();
    }
}
