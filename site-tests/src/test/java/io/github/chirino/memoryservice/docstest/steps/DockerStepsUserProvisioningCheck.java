package io.github.chirino.memoryservice.docstest.steps;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;

import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

class DockerStepsUserProvisioningCheck {

    @Test
    void shouldProvisionScenarioUsersAndIssueTokens() {
        DockerSteps dockerSteps = new DockerSteps();
        dockerSteps.setup();

        int scenarioPort = 19191;
        Map<String, String> env = new HashMap<>();
        DockerSteps.injectScenarioAuthTokens(env, scenarioPort);

        assertNotNull(env.get("CMD_get-token"));
        assertNotNull(env.get("CMD_get-token_bob_bob"));
        assertNotNull(env.get("CMD_get-token_alice_alice"));
        assertNotNull(env.get("CMD_get-token_charlie_charlie"));

        assertFalse(env.get("CMD_get-token").isBlank());
        assertFalse(env.get("CMD_get-token_bob_bob").isBlank());
        assertFalse(env.get("CMD_get-token_alice_alice").isBlank());
        assertFalse(env.get("CMD_get-token_charlie_charlie").isBlank());

        dockerSteps.cleanup();
    }
}
