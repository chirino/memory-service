package io.github.chirino.cognition.skills.verifier;

import java.util.List;

public record VerifiedSkill(
        String type,
        String title,
        String description,
        List<String> steps,
        String conditions,
        String confidence,
        List<String> sourceEntryIds) {}
