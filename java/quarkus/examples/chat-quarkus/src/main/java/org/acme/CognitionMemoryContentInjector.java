package org.acme;

import dev.langchain4j.data.message.ChatMessage;
import dev.langchain4j.data.message.TextContent;
import dev.langchain4j.data.message.UserMessage;
import dev.langchain4j.rag.content.Content;
import dev.langchain4j.rag.content.injector.ContentInjector;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.ArrayList;
import java.util.List;

@ApplicationScoped
public class CognitionMemoryContentInjector implements ContentInjector {

    @Override
    public ChatMessage inject(List<Content> contents, ChatMessage chatMessage) {
        if (contents == null || contents.isEmpty() || !(chatMessage instanceof UserMessage user)) {
            return chatMessage;
        }

        List<dev.langchain4j.data.message.Content> messageContents = new ArrayList<>();
        messageContents.add(TextContent.from(injectedText(contents)));
        messageContents.addAll(user.contents());

        return UserMessage.builder()
                .name(user.name())
                .contents(messageContents)
                .attributes(user.attributes())
                .build();
    }

    private static String injectedText(List<Content> contents) {
        StringBuilder builder = new StringBuilder();
        builder.append("Durable context from previous conversations is provided below.\n");
        builder.append("Use the profile context as baseline background for this conversation.\n");
        builder.append(
                "Use ad hoc memories only when they are clearly relevant to the current"
                        + " request.\n");
        builder.append(
                "If durable context conflicts with the user's current message, follow the current"
                        + " message and mention the conflict when useful.\n\n");

        for (Content content : contents) {
            if (content != null && content.textSegment() != null) {
                String text = content.textSegment().text();
                if (text != null && !text.isBlank()) {
                    builder.append(text.strip()).append("\n\n");
                }
            }
        }
        return builder.toString().strip();
    }
}
