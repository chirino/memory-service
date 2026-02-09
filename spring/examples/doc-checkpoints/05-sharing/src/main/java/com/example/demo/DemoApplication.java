package com.example.demo;

import java.net.http.HttpClient;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;
import org.springframework.http.client.JdkClientHttpRequestFactory;
import org.springframework.web.client.RestClient;

@SpringBootApplication
public class DemoApplication {

    public static void main(String[] args) {
        SpringApplication.run(DemoApplication.class, args);
    }

    @Bean
    public RestClient.Builder restClientBuilder() {
        // Force HTTP/1.1 to avoid HTTP/2 issues with WireMock
        HttpClient httpClient =
                HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1).build();

        JdkClientHttpRequestFactory requestFactory = new JdkClientHttpRequestFactory(httpClient);

        return RestClient.builder().requestFactory(requestFactory);
    }
}
