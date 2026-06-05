package org.acme;

import dev.langchain4j.data.message.ChatMessage;
import dev.langchain4j.data.message.UserMessage;
import dev.langchain4j.rag.AugmentationRequest;
import dev.langchain4j.rag.AugmentationResult;
import dev.langchain4j.rag.RetrievalAugmentor;
import dev.langchain4j.rag.content.Content;
import dev.langchain4j.rag.query.Query;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Supplier;

@ApplicationScoped
public class CognitionMemoryRetrievalAugmentor implements Supplier<RetrievalAugmentor> {

    @Inject CognitionMemoryRagConfig config;
    @Inject CognitionMemoryProfileContext profileContext;
    @Inject CognitionMemoryContentRetriever retriever;
    @Inject CognitionMemoryContentInjector injector;

    @Override
    public RetrievalAugmentor get() {
        return this::augment;
    }

    private AugmentationResult augment(AugmentationRequest request) {
        if (!config.enabled()) {
            return new AugmentationResult(request.chatMessage(), List.of());
        }

        List<Content> contents = new ArrayList<>();
        if (isFirstTurn(request)) {
            contents.addAll(profileContext.retrieve());
        }
        contents.addAll(
                retriever.retrieve(
                        Query.from(userText(request.chatMessage()), request.metadata())));

        ChatMessage augmented = injector.inject(contents, request.chatMessage());
        return new AugmentationResult(augmented, contents);
    }

    static boolean isFirstTurn(AugmentationRequest request) {
        if (request == null
                || request.metadata() == null
                || request.metadata().chatMemory() == null) {
            return true;
        }
        long userTurns =
                request.metadata().chatMemory().stream()
                        .filter(UserMessage.class::isInstance)
                        .count();
        return userTurns <= 1;
    }

    static String userText(ChatMessage chatMessage) {
        if (chatMessage instanceof UserMessage userMessage) {
            if (userMessage.hasSingleText()) {
                return userMessage.singleText();
            }
            StringBuilder builder = new StringBuilder();
            for (dev.langchain4j.data.message.Content content : userMessage.contents()) {
                if (content instanceof dev.langchain4j.data.message.TextContent textContent) {
                    builder.append(textContent.text()).append('\n');
                }
            }
            return builder.toString().strip();
        }
        return "";
    }
}
