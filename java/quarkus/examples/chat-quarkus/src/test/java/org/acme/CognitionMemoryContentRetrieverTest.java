package org.acme;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.github.chirino.memory.client.model.MemoryItem;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class CognitionMemoryContentRetrieverTest {

    @Test
    void adHocCandidateRequiresCloseScore() {
        MemoryItem item = memory("preference", "preference", 0.81);

        assertFalse(CognitionMemoryContentRetriever.adHocCandidate(item, 0.82));

        item.setScore(0.82);
        assertTrue(CognitionMemoryContentRetriever.adHocCandidate(item, 0.82));
    }

    @Test
    void adHocCandidateSkipsProfileNamespaces() {
        assertFalse(
                CognitionMemoryContentRetriever.adHocCandidate(
                        memory("profile_context", "profile_context_snapshot", 0.99), 0.82));
        assertFalse(
                CognitionMemoryContentRetriever.adHocCandidate(
                        memory("profile_input", "profile_context_inputs", 0.99), 0.82));
    }

    private static MemoryItem memory(String namespaceTail, String kind, double score) {
        MemoryItem item = new MemoryItem();
        item.setNamespace(List.of("user", "bob", "cognition.v1", namespaceTail));
        item.setKey("k1");
        item.setValue(Map.of("kind", kind, "content", "memory content"));
        item.setScore(score);
        return item;
    }
}
