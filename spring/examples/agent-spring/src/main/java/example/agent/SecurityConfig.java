package example.agent;

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
                                auth.requestMatchers("/actuator/**")
                                        .permitAll()
                                        .anyRequest()
                                        .authenticated())
                // Use saved request so users return to the URL they originally requested after
                // login.
                .oauth2Login(oauth2 -> oauth2.defaultSuccessUrl("/", false))
                .logout(logout -> logout.logoutSuccessUrl("/"));

        return http.build();
    }
}
