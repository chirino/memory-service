package io.github.chirino.cognition.skills.verifier;

import dev.langchain4j.service.SystemMessage;
import dev.langchain4j.service.UserMessage;
import io.quarkiverse.langchain4j.RegisterAiService;

@RegisterAiService(
        chatMemoryProviderSupplier = RegisterAiService.NoChatMemoryProviderSupplier.class)
public interface SkillVerifierService {

    @SystemMessage(fromResource = "prompts/skill-verifier-system.md")
    VerificationResponse verify(@UserMessage String verificationRequest);
}
