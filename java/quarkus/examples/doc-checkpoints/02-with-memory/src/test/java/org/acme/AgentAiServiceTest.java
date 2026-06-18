package org.acme;

import static org.junit.jupiter.api.Assertions.assertEquals;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import jakarta.inject.Inject;
import java.util.UUID;
import org.junit.jupiter.api.Test;

@QuarkusTest
@QuarkusTestResource(MockOpenAiTestResource.class)
class AgentAiServiceTest {

    @Inject Agent agent;

    @Test
    void testAgentRequest() {
        String result = agent.chat(UUID.randomUUID().toString(), "hi");

        assertEquals("test-response", result);
    }
}
