package io.github.chirino.memory.subagent.runtime;

public record SubAgentToolDefinition(
        String childAgentId,
        Integer maxConcurrency,
        String messageToolName,
        String messageToolDescription,
        String statusToolName,
        String statusToolDescription,
        String waitToolName,
        String waitToolDescription,
        String stopToolName,
        String stopToolDescription) {

    public SubAgentToolDefinition {
        childAgentId = normalize(childAgentId);
        maxConcurrency = normalize(maxConcurrency);
        messageToolName = defaultIfBlank(messageToolName, "agentSend");
        messageToolDescription =
                defaultIfBlank(
                        messageToolDescription,
                        """
                        Send work to a delegated child agent conversation. Prefer this when work can be
                        split into independent subtasks, when the user asks for work to be done in
                        parallel, or when focused delegated work will help before answering. Reuse an
                        existing agent conversation when it already has the right context for the next
                        step instead of starting a new one. When taskId is omitted, the runtime creates a
                        new child agent conversation.
                        When taskId is present, mode is required and must be either "queue" or
                        "interrupt". If a max concurrency limit is configured, trying to start more than
                        that many RUNNING tasks fails.
                        """);
        statusToolName = defaultIfBlank(statusToolName, "agentPoll");
        statusToolDescription =
                defaultIfBlank(
                        statusToolDescription,
                        """
                        Poll delegated agent state without waiting. Use this to inspect current progress,
                        retrieve the latest result, or decide whether to reuse, wait for, or stop an
                        agent conversation.
                        """);
        waitToolName = defaultIfBlank(waitToolName, "waitTask");
        waitToolDescription =
                defaultIfBlank(
                        waitToolDescription,
                        """
                        Wait for one or more delegated agent conversations to complete for up to secs
                        seconds. If taskIds is omitted or empty, this waits across all current child
                        agent conversations for the
                        parent conversation. If secs is 0 or omitted, the runtime waits for 5 seconds by
                        default. Use agentPoll to poll without waiting.
                        """);
        stopToolName = defaultIfBlank(stopToolName, "agentStop");
        stopToolDescription =
                defaultIfBlank(
                        stopToolDescription,
                        """
                        Stop an in-progress delegated agent conversation when it is no longer needed.
                        This clears any queued follow-up message.
                        """);
    }

    public static Builder builder() {
        return new Builder();
    }

    public static final class Builder {
        private String childAgentId;
        private Integer maxConcurrency;
        private String messageToolName;
        private String messageToolDescription;
        private String statusToolName;
        private String statusToolDescription;
        private String waitToolName;
        private String waitToolDescription;
        private String stopToolName;
        private String stopToolDescription;

        public Builder childAgentId(String childAgentId) {
            this.childAgentId = childAgentId;
            return this;
        }

        public Builder maxConcurrency(Integer maxConcurrency) {
            this.maxConcurrency = maxConcurrency;
            return this;
        }

        public Builder messageToolName(String messageToolName) {
            this.messageToolName = messageToolName;
            return this;
        }

        public Builder messageToolDescription(String messageToolDescription) {
            this.messageToolDescription = messageToolDescription;
            return this;
        }

        public Builder statusToolName(String statusToolName) {
            this.statusToolName = statusToolName;
            return this;
        }

        public Builder statusToolDescription(String statusToolDescription) {
            this.statusToolDescription = statusToolDescription;
            return this;
        }

        public Builder waitToolName(String waitToolName) {
            this.waitToolName = waitToolName;
            return this;
        }

        public Builder waitToolDescription(String waitToolDescription) {
            this.waitToolDescription = waitToolDescription;
            return this;
        }

        public Builder stopToolName(String stopToolName) {
            this.stopToolName = stopToolName;
            return this;
        }

        public Builder stopToolDescription(String stopToolDescription) {
            this.stopToolDescription = stopToolDescription;
            return this;
        }

        public SubAgentToolDefinition build() {
            return new SubAgentToolDefinition(
                    childAgentId,
                    maxConcurrency,
                    messageToolName,
                    messageToolDescription,
                    statusToolName,
                    statusToolDescription,
                    waitToolName,
                    waitToolDescription,
                    stopToolName,
                    stopToolDescription);
        }
    }

    private static String defaultIfBlank(String value, String defaultValue) {
        return normalize(value) == null ? defaultValue : value.trim();
    }

    private static String normalize(String value) {
        if (value == null) {
            return null;
        }
        String trimmed = value.trim();
        return trimmed.isEmpty() ? null : trimmed;
    }

    private static Integer normalize(Integer value) {
        if (value == null) {
            return null;
        }
        if (value <= 0) {
            throw new IllegalArgumentException("maxConcurrency must be greater than 0");
        }
        return value;
    }
}
