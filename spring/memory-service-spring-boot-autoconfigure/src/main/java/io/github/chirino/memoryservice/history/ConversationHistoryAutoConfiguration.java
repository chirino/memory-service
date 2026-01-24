package io.github.chirino.memoryservice.history;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcClients;
import io.grpc.ManagedChannel;
import org.springframework.ai.chat.client.advisor.api.StreamAdvisor;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.security.oauth2.client.ReactiveOAuth2AuthorizedClientManager;
import org.springframework.web.reactive.function.client.WebClient;

@Configuration(proxyBeanMethods = false)
@ConditionalOnClass(StreamAdvisor.class)
public class ConversationHistoryAutoConfiguration {

    @Bean
    public ConversationsApiFactory conversationsApiFactory(
            MemoryServiceClientProperties properties,
            ObjectProvider<WebClient.Builder> webClientBuilderProvider,
            ObjectProvider<ReactiveOAuth2AuthorizedClientManager> managerProvider) {
        WebClient.Builder builder = webClientBuilderProvider.getIfAvailable(WebClient::builder);
        if (builder == null) {
            builder = WebClient.builder();
        }
        return new ConversationsApiFactory(properties, builder, managerProvider.getIfAvailable());
    }

    @Bean
    public ConversationStore conversationStore(
            ConversationsApiFactory conversationsApiFactory,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        return new ConversationStore(
                conversationsApiFactory, authorizedClientServiceProvider.getIfAvailable());
    }

    @Bean
    public ConversationHistoryStreamAdvisorBuilder conversationHistoryStreamAdvisorBuilder(
            ConversationStore conversationStore, ResponseResumer responseResumer) {
        return new ConversationHistoryStreamAdvisorBuilder(conversationStore, responseResumer);
    }

    @Bean
    @ConditionalOnBean({MemoryServiceGrpcClients.MemoryServiceStubs.class, ManagedChannel.class})
    public ResponseResumer grpcResponseResumer(
            MemoryServiceClientProperties clientProperties,
            MemoryServiceGrpcClients.MemoryServiceStubs stubs,
            ManagedChannel channel,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        return new SpringGrpcResponseResumer(
                stubs, channel, clientProperties, authorizedClientServiceProvider.getIfAvailable());
    }

    @Bean
    @ConditionalOnMissingBean(ResponseResumer.class)
    public ResponseResumer noopResponseResumer() {
        return ResponseResumer.noop();
    }
}
