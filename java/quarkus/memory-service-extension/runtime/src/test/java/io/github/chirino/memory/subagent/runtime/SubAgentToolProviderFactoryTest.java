package io.github.chirino.memory.subagent.runtime;

import static org.assertj.core.api.Assertions.assertThat;

import java.time.Instant;
import java.util.List;
import org.junit.jupiter.api.Test;

class SubAgentToolProviderFactoryTest {

    @Test
    void normalizeWaitSecondsUsesDefaultForZeroOrNegativeValues() {
        assertThat(SubAgentToolProviderFactory.normalizeWaitSeconds(0)).isEqualTo(5);
        assertThat(SubAgentToolProviderFactory.normalizeWaitSeconds(-1)).isEqualTo(5);
        assertThat(SubAgentToolProviderFactory.normalizeWaitSeconds(7)).isEqualTo(7);
    }

    @Test
    void messageToolDescriptionIncludesConfiguredMaxConcurrency() {
        String description =
                SubAgentToolProviderFactory.messageToolDescription(
                        SubAgentToolDefinition.builder().maxConcurrency(3).build());

        assertThat(description).contains("At most 3 tasks may be RUNNING");
    }

    @Test
    void simplifyWaitResultReturnsMinimalTaskViews() {
        SubAgentTaskResult completed =
                new SubAgentTaskResult(
                        "parent-1",
                        "child-1",
                        null,
                        SubAgentTaskStatus.COMPLETED,
                        "task one",
                        "streamed one",
                        "final one",
                        null,
                        null,
                        null,
                        1,
                        Instant.parse("2026-03-23T22:27:35Z"),
                        Instant.parse("2026-03-23T22:27:37Z"));
        SubAgentTaskResult running =
                new SubAgentTaskResult(
                        "parent-1",
                        "child-2",
                        null,
                        SubAgentTaskStatus.RUNNING,
                        "task two",
                        "partial two",
                        null,
                        null,
                        null,
                        null,
                        2,
                        Instant.parse("2026-03-23T22:27:35Z"),
                        Instant.parse("2026-03-23T22:27:36Z"));

        List<SubAgentWaitTaskView> simplified =
                SubAgentToolProviderFactory.simplify(
                        new SubAgentWaitResult("parent-1", false, List.of(completed, running)));

        assertThat(simplified)
                .containsExactly(
                        new SubAgentWaitTaskView(
                                "child-1", SubAgentTaskStatus.COMPLETED, "final one", null),
                        new SubAgentWaitTaskView(
                                "child-2", SubAgentTaskStatus.RUNNING, "partial two", null));
    }
}
