package example.agent;

import java.util.Map;
import org.springframework.security.core.annotation.AuthenticationPrincipal;
import org.springframework.security.oauth2.core.user.OAuth2User;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * Returns information about the current authenticated user.
 */
@RestController
@RequestMapping("/v1/me")
class CurrentUserController {

    @GetMapping
    public Map<String, String> getCurrentUser(@AuthenticationPrincipal OAuth2User oauth2User) {
        if (oauth2User == null) {
            return Map.of("userId", "anonymous");
        }
        // Try preferred_username first (standard OIDC claim), then fall back to name, then sub
        String userId = oauth2User.getAttribute("preferred_username");
        if (userId == null) {
            userId = oauth2User.getAttribute("name");
        }
        if (userId == null) {
            userId = oauth2User.getName();
        }
        return Map.of("userId", userId != null ? userId : "anonymous");
    }
}
