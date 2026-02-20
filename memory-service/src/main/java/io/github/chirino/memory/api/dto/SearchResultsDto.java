package io.github.chirino.memory.api.dto;

import java.util.List;

public class SearchResultsDto {

    private List<SearchResultDto> results;
    private String afterCursor;

    public List<SearchResultDto> getResults() {
        return results;
    }

    public void setResults(List<SearchResultDto> results) {
        this.results = results;
    }

    public String getAfterCursor() {
        return afterCursor;
    }

    public void setAfterCursor(String afterCursor) {
        this.afterCursor = afterCursor;
    }
}
