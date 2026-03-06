package org.acme;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configuration.EnableWebSecurity;
import org.springframework.security.config.http.SessionCreationPolicy;
import org.springframework.security.web.SecurityFilterChain;

@Configuration
@EnableWebSecurity
class SecurityConfig {

    @Bean
    SecurityFilterChain securityFilterChain(HttpSecurity http) throws Exception {
        http.csrf(csrf -> csrf.disable())
                .sessionManagement(
                        session -> session.sessionCreationPolicy(SessionCreationPolicy.STATELESS))
                .authorizeHttpRequests(
                        auth ->
                                auth
                                        // Allow static resources and actuator
                                        .requestMatchers(
                                                "/actuator/**",
                                                "/",
                                                "/index.html",
                                                "/assets/**",
                                                "/*.js",
                                                "/*.css",
                                                "/*.ico")
                                        .permitAll()
                                        // Signed download URLs are unauthenticated
                                        .requestMatchers("/v1/attachments/download/**")
                                        .permitAll()
                                        // All API endpoints require authentication
                                        .requestMatchers("/v1/**", "/chat/**")
                                        .authenticated()
                                        .anyRequest()
                                        .permitAll())
                // Use OAuth2 Resource Server with JWT validation
                .oauth2ResourceServer(oauth2 -> oauth2.jwt(jwt -> {}));

        return http.build();
    }
}
