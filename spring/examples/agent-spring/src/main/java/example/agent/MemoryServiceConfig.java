package example.agent;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection.MemoryServiceConnectionDetails;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.WebClient;

@Configuration
@EnableConfigurationProperties(MemoryServiceClientProperties.class)
class MemoryServiceConfig {

    private static final Logger LOG = LoggerFactory.getLogger(MemoryServiceConfig.class);

    @Bean
    MemoryServiceProxy memoryServiceProxy(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientService,
            ObjectProvider<MemoryServiceConnectionDetails> connectionDetailsProvider) {

        MemoryServiceConnectionDetails connectionDetails =
                connectionDetailsProvider.getIfAvailable();
        if (connectionDetails != null) {
            if (!StringUtils.hasText(properties.getApiKey())
                    && StringUtils.hasText(connectionDetails.getApiKey())) {
                properties.setApiKey(connectionDetails.getApiKey());
            }
            if (!StringUtils.hasText(properties.getBaseUrl())
                    && StringUtils.hasText(connectionDetails.getBaseUrl())) {
                properties.setBaseUrl(connectionDetails.getBaseUrl());
            }

            LOG.info(
                    "MemoryService client configured from service connection: baseUrl={},"
                            + " apiKeyPresent={}",
                    connectionDetails.getBaseUrl(),
                    StringUtils.hasText(connectionDetails.getApiKey()));
        } else {
            LOG.info(
                    "MemoryService client configured from properties: baseUrl={}, apiKeyPresent={}",
                    properties.getBaseUrl(),
                    StringUtils.hasText(properties.getApiKey()));
        }

        return new MemoryServiceProxy(
                properties, webClientBuilder, authorizedClientService.getIfAvailable());
    }
}
