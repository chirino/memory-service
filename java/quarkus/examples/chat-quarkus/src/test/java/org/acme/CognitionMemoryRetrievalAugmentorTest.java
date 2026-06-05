package org.acme;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import dev.langchain4j.data.message.AiMessage;
import dev.langchain4j.data.message.UserMessage;
import dev.langchain4j.rag.AugmentationRequest;
import dev.langchain4j.rag.query.Metadata;
import java.util.List;
import org.junit.jupiter.api.Test;

class CognitionMemoryRetrievalAugmentorTest {

    @Test
    void treatsOneUserMessageAsFirstTurnCandidate() {
        UserMessage message = UserMessage.from("hello");
        AugmentationRequest request =
                new AugmentationRequest(
                        message, Metadata.from(message, "conversation-1", List.of(message)));

        assertTrue(CognitionMemoryRetrievalAugmentor.isFirstTurn(request));
    }

    @Test
    void treatsMultipleUserMessagesAsLaterTurn() {
        UserMessage current = UserMessage.from("second");
        AugmentationRequest request =
                new AugmentationRequest(
                        current,
                        Metadata.from(
                                current,
                                "conversation-1",
                                List.of(UserMessage.from("first"), AiMessage.from("ok"), current)));

        assertFalse(CognitionMemoryRetrievalAugmentor.isFirstTurn(request));
    }
}
