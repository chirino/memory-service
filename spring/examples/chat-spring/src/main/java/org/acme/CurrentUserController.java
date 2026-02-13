package org.acme;

import java.util.HashMap;
import java.util.Map;
import org.springframework.security.core.annotation.AuthenticationPrincipal;
import org.springframework.security.oauth2.jwt.Jwt;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * Returns information about the current authenticated user.
 * With bearer token authentication, user info comes from the JWT claims.
 */
@RestController
@RequestMapping("/v1/me")
class CurrentUserController {

    @GetMapping
    public Map<String, String> getCurrentUser(@AuthenticationPrincipal Jwt jwt) {
        if (jwt == null) {
            return Map.of("userId", "anonymous");
        }
        // Try preferred_username first (standard OIDC claim), then fall back to name, then sub
        String userId = jwt.getClaimAsString("preferred_username");
        if (userId == null) {
            userId = jwt.getClaimAsString("name");
        }
        if (userId == null) {
            userId = jwt.getSubject();
        }

        // Get name and email from JWT claims
        String name = jwt.getClaimAsString("name");
        String email = jwt.getClaimAsString("email");

        Map<String, String> result = new HashMap<>();
        result.put("userId", userId != null ? userId : "anonymous");
        if (name != null) {
            result.put("name", name);
        }
        if (email != null) {
            result.put("email", email);
        }
        return result;
    }
}
