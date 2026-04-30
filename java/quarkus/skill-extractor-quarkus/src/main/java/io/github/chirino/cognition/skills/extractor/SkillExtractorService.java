package io.github.chirino.cognition.skills.extractor;

import dev.langchain4j.service.SystemMessage;
import dev.langchain4j.service.UserMessage;
import io.quarkiverse.langchain4j.RegisterAiService;

@RegisterAiService(
        chatMemoryProviderSupplier = RegisterAiService.NoChatMemoryProviderSupplier.class)
public interface SkillExtractorService {

    @SystemMessage(fromResource = "prompts/skill-extractor-system.md")
    @UserMessage(fromResource = "prompts/skill-extractor-user.md")
    ExtractionResponse extractSkills(
            String clusterLabel,
            String keywords,
            String trend,
            int memberCount,
            String representativeTexts);
}
