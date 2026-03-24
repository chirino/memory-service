# Async Sub-Agent Design

This document describes how async sub-agent workflows should behave across all Memory Service client frameworks.

The goal is to let a parent agent delegate work to one or more child agents while keeping the conversation wiring framework-managed and the waiting/cancellation model explicit and portable.

## Goals

- Let a parent agent start child tasks as normal tool calls.
- Run child agents asynchronously in their own execution threads or tasks.
- Keep the parent agent free to continue reasoning and making more tool calls in the same turn.
- Let the parent agent explicitly decide whether to keep working, wait for a child, poll status, or stop a child task.
- Make the tool contract simple enough to implement consistently across frameworks.
- Keep the framework-specific implementation mostly inside the framework integration layer instead of the application.

## Non-Goals

- Replaying the full parent transcript into the child conversation.
- Making child-task orchestration depend on a specific LLM provider or tool-calling format.

## Core Concepts

### Parent Conversation

The conversation currently handling the end-user turn.

### Child Conversation

A new conversation started from the parent conversation by setting `startedByConversationId` and creating the first child entry atomically. The first child entry is the explicit delegating message for the sub-agent.

### Framework Runtime

The language/framework integration layer that:

- exposes model-facing tools,
- creates child conversations,
- schedules child execution,
- tracks child task state,
- exposes bounded waiting and cancellation primitives, and
- records child conversation history and results.

## Model-Facing Interface

The model-facing tool surface should stay small and concrete.

Recommended tools:

- `agentSend`
- `agentPoll`
- `waitTask`
- `agentStop`

These tools represent concrete actions the model may need:

- start or continue a child agent conversation,
- inspect current progress,
- wait up to a bounded amount of time for completion,
- stop a child agent conversation that is no longer needed.

The minimum useful response from `agentSend` is:

- `taskId`
- `status`

When `taskId` is omitted, `agentSend` creates a new child conversation and sends the first delegated message. When `taskId` is present, it appends a follow-up message to that existing child conversation, and the caller must provide an explicit mode such as `queue` or `interrupt`. If an existing child conversation already has the right context, prefer reusing it instead of starting a new one.
`agentSend` may also accept an optional `agentId` override when the framework wants the model to choose among multiple child agent identities.

Returning the child conversation ID is important because the parent agent may want to send follow-up messages to that child later in the same turn or in a later turn.

The minimum useful response from `waitTask` and `agentPoll` is:

- `taskId`
- `status`
- `response` if available
- `lastError` if available
- optionally streaming progress such as accumulated partial output

The minimum useful response from `agentStop` is:

- `taskId`
- final status, typically `STOPPED`
- any last known output or error state

## Conversation Model

Async sub-agent workflows depend on the conversation-lineage model:

- child conversations are separate conversations, not forks,
- each child conversation records `startedByConversationId`,
- optionally `startedByEntryId` points at the parent entry that triggered the delegation,
- the child conversation begins with the explicit delegated task,
- the child conversation keeps its own history and agent attribution.

This separation is important because the child transcript should remain inspectable as an independent unit of work.

## Execution Model

The runtime should treat child execution as asynchronous work owned by the framework, but waiting should be explicit.

### Start

When the parent model calls `agentSend` without a `taskId`:

1. The framework creates a new child conversation.
2. The framework appends the first child entry atomically with lineage metadata.
3. The framework schedules child execution on a background thread, task, coroutine, or equivalent framework-native async primitive.
4. The tool returns promptly to the parent agent loop with the `taskId`.
5. If the framework configures a maximum concurrency, only tasks currently in `RUNNING` state count toward that limit. Starting another `RUNNING` task beyond that limit should return an error.

When the parent model calls `agentSend` with an existing `taskId`, the framework appends a new child entry and schedules or resumes child execution for that same child conversation.

The important constraint is that the tool call should not block until the child finishes.

### Continue Parent Reasoning

After starting one child task, the parent agent must still be able to:

- start more child tasks,
- call other tools,
- continue reasoning within the same parent turn.

That means waiting must not happen inside the `agentSend` tool implementation.

### Poll Or Wait Explicitly

If the parent wants child results before answering, it should explicitly call `waitTask(taskIds, secs)`.
If the parent needs every outstanding child result for the current parent conversation, it should omit `taskIds` or pass an empty list.

`waitTask` should:

- wait for up to `secs`,
- treat `secs=0` or omitted `secs` as a default 5-second bounded wait,
- return immediately if the selected child task or tasks complete sooner,
- return current status if the timeout expires first,
- preserve the child task so the parent can wait again later.
- when `taskIds` is omitted or empty, wait across all current child tasks for the parent conversation and return an aggregate result.

If the parent only wants a non-blocking check, it should call `agentPoll`.

This keeps the control flow simple and observable:

- start child work,
- continue reasoning,
- optionally wait,
- optionally poll,
- optionally stop,
- then answer the user.

### Stop Explicitly

If a delegated child conversation is no longer useful, the parent can call `agentStop`.

`agentStop` should:

- mark the task as stopped,
- cancel background work if the framework can do so,
- be best-effort for in-flight model calls or streams,
- return an updated task status.

Frameworks should not promise hard cancellation if the underlying LLM/tool runtime does not support it cleanly.

## Child Completion Handling

A completed child task should produce enough information for the parent runtime to make it useful in the next parent invocation.

Typical completion data includes:

- `taskId`
- child agent identifier if available
- task status
- streamed response text so far, for streaming child tasks
- last child response
- last child error, if any

The exact serialization can vary by framework, but tool responses should stay stable enough that the parent model can reason over them directly.

## Failure Semantics

Child failures should not silently disappear.

If a child task fails:

- mark the child task as failed,
- preserve any error detail that is safe to expose to the parent model,
- return that error detail from status and wait operations,
- let the parent model decide whether to retry, continue with partial information, or explain the failure to the user.

Frameworks may also enforce upper bounds such as:

- max child runtime,
- max fan-out child tasks per parent turn.

Those safeguards are runtime concerns, not model tools.

## Streaming Considerations

Streaming child tasks are still useful, but frameworks should expose their progress through `agentPoll` and `waitTask` rather than implicitly merging child completion into the parent response.

## Framework Responsibilities

Each framework integration should provide:

- model-facing tools for child-task creation and messaging,
- bounded wait and stop operations,
- async execution infrastructure for child agents,
- optional streaming child-task support when the framework can surface child event streams,
- child-task status tracking,
- a simple application extension point for the actual child-agent implementation.

The application should usually only need to provide:

- the parent AI service,
- the child AI service and a small app-specific provider or adapter that binds it into the framework runtime,
- optional formatting or policy hooks.

## Quarkus Mapping

The current Quarkus implementation is one example of this design:

- the parent AI service can register a `toolProviderSupplier`,
- the sub-agent runtime factory builds low-level `ToolSpecification` and `ToolExecutor` pairs for `agentSend`, `agentPoll`, `waitTask`, and `agentStop`,
- the app only supplies the concrete child AI service invocation and optional tool names/descriptions,
- `SubAgentTaskManager` tracks task state, queued follow-ups, bounded waits, and best-effort stop requests,
- the parent agent uses explicit tool calls to decide when to wait, poll, stop, queue, or interrupt.

Other frameworks should match the behavior, even if the class names and async primitives differ.

## Summary

Async sub-agent workflows should feel simple to the parent model:

- start child work,
- continue reasoning,
- explicitly wait when needed,
- explicitly stop work that is no longer useful,
- answer the user when it has enough information.

The complexity belongs in the framework runtime, not in model prompts or user application glue code.
