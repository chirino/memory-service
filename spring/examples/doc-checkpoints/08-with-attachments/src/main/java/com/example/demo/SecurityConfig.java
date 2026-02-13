package com.example.demo;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.web.SecurityFilterChain;

@Configuration
class SecurityConfig {

    @Bean
    SecurityFilterChain securityFilterChain(HttpSecurity http) throws Exception {
        http.csrf(csrf -> csrf.disable())
                .authorizeHttpRequests(
                        auth ->
                                auth
                                        // Signed download URLs are unauthenticated
                                        // (token in URL provides authorization)
                                        .requestMatchers("/v1/attachments/download/**")
                                        .permitAll()
                                        .anyRequest()
                                        .authenticated())
                .oauth2Login(oauth2 -> oauth2.defaultSuccessUrl("/", false))
                .oauth2ResourceServer(oauth2 -> oauth2.jwt(jwt -> {}))
                .logout(logout -> logout.logoutSuccessUrl("/"));
        return http.build();
    }
}
