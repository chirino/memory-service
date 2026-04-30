package io.github.chirino.cognition.skills.cluster;

import java.time.OffsetDateTime;
import java.util.List;
import java.util.Map;

/**
 * Intermediate representation of a knowledge cluster, ready for LLM extraction.
 */
public record ClusterIR(
        String id,
        String userId,
        String label,
        List<String> keywords,
        int memberCount,
        String trend,
        Map<String, String> representativeTexts,
        OffsetDateTime updatedAt) {}
