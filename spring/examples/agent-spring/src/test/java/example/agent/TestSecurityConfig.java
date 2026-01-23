package example.agent;

import org.springframework.boot.test.context.TestConfiguration;
import org.springframework.context.annotation.Bean;
import org.springframework.security.oauth2.client.InMemoryOAuth2AuthorizedClientService;
import org.springframework.security.oauth2.client.registration.ClientRegistration;
import org.springframework.security.oauth2.client.registration.ClientRegistrationRepository;
import org.springframework.security.oauth2.client.registration.InMemoryClientRegistrationRepository;
import org.springframework.security.oauth2.core.AuthorizationGrantType;

@TestConfiguration
class TestSecurityConfig {

    @Bean
    ClientRegistrationRepository clientRegistrationRepository() {
        ClientRegistration registration =
                ClientRegistration.withRegistrationId("memory-service-client")
                        .clientId("test-client")
                        .clientSecret("test-secret")
                        .authorizationGrantType(AuthorizationGrantType.AUTHORIZATION_CODE)
                        .redirectUri("{baseUrl}/login/oauth2/code/{registrationId}")
                        .scope("openid")
                        .authorizationUri("http://localhost/authorize")
                        .tokenUri("http://localhost/token")
                        .jwkSetUri("http://localhost/jwks")
                        .build();
        return new InMemoryClientRegistrationRepository(registration);
    }

    @Bean
    InMemoryOAuth2AuthorizedClientService authorizedClientService(
            ClientRegistrationRepository clientRegistrationRepository) {
        return new InMemoryOAuth2AuthorizedClientService(clientRegistrationRepository);
    }
}
