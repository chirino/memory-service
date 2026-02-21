package io.github.chirino.memory.vector;

import static org.junit.jupiter.api.Assertions.assertSame;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.store.MemoryStore;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

class SearchResultDtoBuilderTest {

    @Test
    void delegatesVectorResultHydrationToMemoryStore() {
        MemoryStoreSelector selector = Mockito.mock(MemoryStoreSelector.class);
        MemoryStore store = Mockito.mock(MemoryStore.class);
        when(selector.getStore()).thenReturn(store);

        SearchResultDto expected = new SearchResultDto();
        when(store.buildFromVectorResult("entry-1", "conversation-1", 0.7d, true))
                .thenReturn(expected);

        SearchResultDtoBuilder builder = new SearchResultDtoBuilder();
        builder.storeSelector = selector;

        SearchResultDto actual =
                builder.buildFromVectorResult("entry-1", "conversation-1", 0.7d, true);

        assertSame(expected, actual);
        verify(store).buildFromVectorResult("entry-1", "conversation-1", 0.7d, true);
    }

    @Test
    void delegatesFullTextResultHydrationToMemoryStore() {
        MemoryStoreSelector selector = Mockito.mock(MemoryStoreSelector.class);
        MemoryStore store = Mockito.mock(MemoryStore.class);
        when(selector.getStore()).thenReturn(store);

        SearchResultDto expected = new SearchResultDto();
        when(store.buildFromFullTextResult("entry-2", "conversation-2", 0.9d, "highlight", false))
                .thenReturn(expected);

        SearchResultDtoBuilder builder = new SearchResultDtoBuilder();
        builder.storeSelector = selector;

        SearchResultDto actual =
                builder.buildFromFullTextResult(
                        "entry-2", "conversation-2", 0.9d, "highlight", false);

        assertSame(expected, actual);
        verify(store)
                .buildFromFullTextResult("entry-2", "conversation-2", 0.9d, "highlight", false);
    }
}
