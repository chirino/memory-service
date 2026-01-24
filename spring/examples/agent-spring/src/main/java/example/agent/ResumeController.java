package example.agent;

import io.github.chirino.memoryservice.history.ResponseResumer;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.util.List;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/v1/conversations")
class ResumeController {

    private final ResponseResumer responseResumer;
    private final OAuth2AuthorizedClientService authorizedClientService;

    ResumeController(
            ResponseResumer responseResumer,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        this.responseResumer = responseResumer;
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
    }

    @PostMapping("/resume-check")
    public List<String> resumeCheck(@RequestBody(required = false) List<String> conversationIds) {
        if (conversationIds == null || conversationIds.isEmpty()) {
            return List.of();
        }
        return responseResumer.check(
                conversationIds, SecurityHelper.bearerToken(authorizedClientService));
    }
}
