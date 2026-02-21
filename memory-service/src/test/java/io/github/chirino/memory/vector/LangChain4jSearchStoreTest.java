package io.github.chirino.memory.vector;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.mockito.ArgumentMatchers.anyList;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import dev.langchain4j.data.segment.TextSegment;
import dev.langchain4j.store.embedding.EmbeddingStore;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

class LangChain4jSearchStoreTest {

    @Test
    void upsertTranscriptEmbedding_uses_non_blank_segment_text() {
        @SuppressWarnings("unchecked")
        EmbeddingStore<TextSegment> embeddingStore = org.mockito.Mockito.mock(EmbeddingStore.class);

        EmbeddingService embeddingService = org.mockito.Mockito.mock(EmbeddingService.class);
        when(embeddingService.modelId()).thenReturn("openai/text-embedding-3-small");

        LangChain4jSearchStore store = new LangChain4jSearchStore();
        store.embeddingStore = embeddingStore;
        store.embeddingService = embeddingService;

        store.upsertTranscriptEmbedding(
                "group-1", "conversation-1", "entry-1", new float[] {1f, 2f});

        @SuppressWarnings("unchecked")
        ArgumentCaptor<List<TextSegment>> segmentsCaptor = ArgumentCaptor.forClass(List.class);
        verify(embeddingStore).addAll(anyList(), anyList(), segmentsCaptor.capture());

        TextSegment segment = segmentsCaptor.getValue().getFirst();
        assertEquals("[indexed-content]", segment.text());
        assertFalse(segment.text().isBlank());
    }
}
