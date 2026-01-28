package io.github.chirino.memory.api.dto;

public class SearchResultDto {

    private EntryDto entry;
    private double score;
    private String highlights;

    public EntryDto getEntry() {
        return entry;
    }

    public void setEntry(EntryDto entry) {
        this.entry = entry;
    }

    public double getScore() {
        return score;
    }

    public void setScore(double score) {
        this.score = score;
    }

    public String getHighlights() {
        return highlights;
    }

    public void setHighlights(String highlights) {
        this.highlights = highlights;
    }
}
