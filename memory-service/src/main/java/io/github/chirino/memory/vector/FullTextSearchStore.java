package io.github.chirino.memory.vector;

import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.model.AdminSearchQuery;

/** Full-text/keyword search contract. */
public interface FullTextSearchStore {

    boolean isFullTextSearchAvailable();

    SearchResultsDto search(String userId, SearchEntriesRequest request);

    SearchResultsDto adminSearch(AdminSearchQuery query);
}
