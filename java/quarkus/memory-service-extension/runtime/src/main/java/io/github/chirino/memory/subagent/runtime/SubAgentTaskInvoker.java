package io.github.chirino.memory.subagent.runtime;

@FunctionalInterface
public interface SubAgentTaskInvoker {

    SubAgentTaskExecution handle(SubAgentTaskRequest request);
}
