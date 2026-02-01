package io.github.chirino.memory.api.dto;

import java.util.List;

public class SearchResultsDto {

    private List<SearchResultDto> results;
    private String nextCursor;

    public List<SearchResultDto> getResults() {
        return results;
    }

    public void setResults(List<SearchResultDto> results) {
        this.results = results;
    }

    public String getNextCursor() {
        return nextCursor;
    }

    public void setNextCursor(String nextCursor) {
        this.nextCursor = nextCursor;
    }
}
