package io.github.chirino.memoryservice.security;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.lang.Nullable;
import org.springframework.security.core.Authentication;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClient;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.security.oauth2.client.authentication.OAuth2AuthenticationToken;
import org.springframework.security.oauth2.core.AbstractOAuth2Token;
import org.springframework.util.StringUtils;

public final class SecurityHelper {

    private static final Logger LOG = LoggerFactory.getLogger(SecurityHelper.class);

    private SecurityHelper() {}

    @Nullable
    public static String bearerToken(
            @Nullable OAuth2AuthorizedClientService authorizedClientService) {
        Authentication authentication = SecurityContextHolder.getContext().getAuthentication();
        if (authentication == null) {
            LOG.info(
                    "bearerToken: No authentication in SecurityContext, thread={}",
                    Thread.currentThread().getName());
            return null;
        }
        LOG.info(
                "bearerToken: Found authentication type={} principal={} thread={}",
                authentication.getClass().getSimpleName(),
                authentication.getName(),
                Thread.currentThread().getName());
        if (authentication instanceof OAuth2AuthenticationToken oauth2
                && authorizedClientService != null) {
            OAuth2AuthorizedClient client =
                    authorizedClientService.loadAuthorizedClient(
                            oauth2.getAuthorizedClientRegistrationId(), oauth2.getName());
            if (client != null && client.getAccessToken() != null) {
                LOG.info("bearerToken: Retrieved token from OAuth2AuthorizedClientService");
                return client.getAccessToken().getTokenValue();
            }
        }
        Object credentials = authentication.getCredentials();
        if (credentials instanceof AbstractOAuth2Token token) {
            LOG.info("bearerToken: Retrieved token from AbstractOAuth2Token credentials");
            return token.getTokenValue();
        }
        if (credentials instanceof String text && StringUtils.hasText(text)) {
            LOG.info("bearerToken: Retrieved token from String credentials");
            return text;
        }
        LOG.info(
                "bearerToken: No token found, credentialsType={}",
                credentials == null ? "null" : credentials.getClass().getSimpleName());
        return null;
    }

    @Nullable
    public static String principalName() {
        Authentication authentication = SecurityContextHolder.getContext().getAuthentication();
        return (authentication != null) ? authentication.getName() : null;
    }
}
