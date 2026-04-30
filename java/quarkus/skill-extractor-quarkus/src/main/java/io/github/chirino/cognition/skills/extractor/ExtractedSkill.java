package io.github.chirino.cognition.skills.extractor;

import java.util.List;

public record ExtractedSkill(
        String type,
        String title,
        String description,
        List<String> steps,
        String conditions,
        String confidence) {}
