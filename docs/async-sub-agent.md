# Async Sub-Agent Design

This document describes how async sub-agent workflows should behave across all Memory Service client frameworks.

The goal is to let a parent agent delegate work to one or more child agents without forcing the model to manage scheduling, polling, or conversation wiring itself.

## Goals

- Let a parent agent start child tasks as normal tool calls.
- Run child agents asynchronously in their own execution threads or tasks.
- Keep the parent agent free to continue reasoning and making more tool calls in the same turn.
- Prevent the parent turn from being finalized to the end user until all joined child tasks have completed.
- Feed child results back into the parent conversation so the parent model can incorporate them before responding.
- Keep the framework-specific implementation mostly inside the framework integration layer instead of the application.

## Non-Goals

- Exposing generic polling tools such as `wait_for_task`, `wait_for_any_task`, or `wait_for_all_tasks` to the model.
- Replaying the full parent transcript into the child conversation.
- Making child-task orchestration depend on a specific LLM provider or tool-calling format.

## Core Concepts

### Parent Conversation

The conversation currently handling the end-user turn.

### Child Conversation

A new conversation started from the parent conversation by setting `startedByConversationId` and creating the first child entry atomically. The first child entry is the explicit delegating message for the sub-agent.

### Joined Child Task

A child task that must complete before the parent agent is allowed to produce its final user-visible AI response.

This is the default mode. Detached child tasks are not part of this design.

### Framework Runtime

The language/framework integration layer that:

- exposes model-facing tools,
- creates child conversations,
- schedules child execution,
- tracks pending joined tasks for the parent turn,
- injects completed child results back into the parent conversation, and
- re-invokes the parent agent until no joined tasks remain.

## Model-Facing Interface

The model-facing tool surface should stay small and concrete.

Recommended tools:

- `messageSubAgent`
- `getSubAgentStatus`

These tools represent real actions the model may want to take. Waiting is not a model tool. Waiting is a runtime concern handled by the framework.

The minimum useful response from `messageSubAgent` is:

- `childConversationId`
- task status metadata if available

When `childConversationId` is omitted, `messageSubAgent` creates a new child conversation and sends the first delegated task. When `childConversationId` is present, it appends a follow-up message to that existing child conversation.

Returning the child conversation ID is important because the parent agent may want to send follow-up messages to that child later in the same turn or in a later turn.

## Conversation Model

Async sub-agent workflows depend on the conversation-lineage model:

- child conversations are separate conversations, not forks,
- each child conversation records `startedByConversationId`,
- optionally `startedByEntryId` points at the parent entry that triggered the delegation,
- the child conversation begins with the explicit delegated task,
- the child conversation keeps its own history and agent attribution.

This separation is important because the child transcript should remain inspectable as an independent unit of work.

## Execution Model

The runtime should treat child execution as asynchronous work owned by the framework.

### Start

When the parent model calls `messageSubAgent` without a `childConversationId`:

1. The framework creates a new child conversation.
2. The framework appends the first child entry atomically with lineage metadata.
3. The framework schedules child execution on a background thread, task, coroutine, or equivalent framework-native async primitive.
4. The tool returns promptly to the parent agent loop with the `childConversationId`.
5. The runtime registers that child task as pending for the parent conversation.

When the parent model calls `messageSubAgent` with an existing `childConversationId`, the framework appends a new child entry, schedules or resumes child execution, and keeps that child task joined to the current parent turn.

The important constraint is that the tool call should not block until the child finishes.

### Continue Parent Reasoning

After starting one child task, the parent agent must still be able to:

- start more child tasks,
- call other tools,
- continue reasoning within the same parent turn.

That means joined waiting must not happen inside the `messageSubAgent` tool implementation.

### Join Before Final Response

When the parent agent reaches a candidate final answer, the runtime must check whether any joined child tasks are still pending for that parent conversation.

If no joined tasks remain:

- the candidate final answer may be returned to the end user.

If joined tasks are still pending:

1. the runtime holds the candidate final answer,
2. waits for all pending joined tasks to complete,
3. summarizes or formats the completed child results,
4. appends that result material into the parent conversation as a synthetic follow-up message,
5. invokes the parent agent again, and
6. repeats this process until there are no joined tasks left.

The parent response should only become user-visible after this join loop settles.

## Parent Loop Contract

Abstractly, the parent turn works like this:

1. Run the parent agent.
2. Execute any tool calls the parent requests.
3. Record any child tasks started during that work as pending joined tasks.
4. Let the parent agent continue until it appears ready to answer the user.
5. Before emitting the final AI response, inspect pending joined tasks.
6. If none remain, emit the final AI response.
7. If some remain, wait for completion, append child results to the parent context, and run the parent agent again.
8. Repeat until no joined tasks remain.

This is the key design rule for all frameworks.

## Child Completion Handling

A completed child task should produce enough information for the parent runtime to make it useful in the next parent invocation.

Typical completion data includes:

- `childConversationId`
- child agent identifier if available
- task status
- streamed response text so far, for streaming child tasks
- last child response
- last child error, if any

The exact serialization can vary by framework, but the parent runtime should append a stable, explicit summary to the parent conversation so the model can reason over it.

## Failure Semantics

Child failures should not silently disappear.

If a child task fails:

- mark the child task as failed,
- preserve any error detail that is safe to expose to the parent model,
- include that failure in the joined result material fed back to the parent,
- let the parent model decide whether to retry, continue with partial information, or explain the failure to the user.

Frameworks may also enforce upper bounds such as:

- max joined rounds per parent turn,
- max child runtime,
- max fan-out child tasks per parent turn.

Those safeguards are runtime concerns, not model tools.

## Streaming Considerations

Joined child tasks complicate streaming responses.

If a framework has already started emitting the parent AI response to the user, it is too late to hold that response pending child completion without additional buffering or resumable streaming semantics.

Because of that, frameworks should do one of the following:

- buffer the candidate parent response until joined child tasks have settled, or
- only allow joined orchestration before committing any user-visible output.

Frameworks should not emit partial user-visible output and then later retroactively join child results into the same parent answer unless they have an explicit resumable streaming model.

## Why Waiting Is Not a Tool

Generic wait or poll tools push orchestration responsibility onto the model. That has several downsides:

- wasted model turns,
- unnecessary token usage,
- less reliable retry and deduplication behavior,
- weaker auditability,
- provider-specific prompting patterns instead of a stable framework contract.

The framework already knows which child tasks belong to the current parent turn. It is the right place to wait, gather results, and resume the parent loop.

## Framework Responsibilities

Each framework integration should provide:

- model-facing tools for child-task creation and messaging,
- async execution infrastructure for child agents,
- optional streaming child-task support when the framework can surface child event streams,
- parent-turn task tracking,
- join-before-final-response orchestration,
- child result injection back into the parent conversation before the next parent invocation,
- a simple application extension point for the actual child-agent implementation.

The application should usually only need to provide:

- the parent AI service,
- the child AI service and a small app-specific tool or adapter that binds it into the framework runtime,
- optional formatting or policy hooks.

## Quarkus Mapping

The current Quarkus implementation is one example of this design:

- `SubAgentTaskTool` is a reusable base class for model-facing actions.
- `StreamingSubAgentTaskTool` is the companion base class for child handlers that return `Multi<ChatEvent>`.
- an app-specific tool such as `FactFindingSubAgentTool` or `FeedbackSubAgentTool` extends that base class and binds the concrete child AI service.
- the base tool derives `childAgentId` from the subclass name by default, so callers do not pass `childAgentId` as a tool parameter.
- `SubAgentTaskManager` tracks pending joined tasks and completion state.
- `SubAgentTurnRunner` keeps the parent turn open, waits at the final-response boundary, appends child results as a synthetic user message in the parent conversation history, and re-invokes the parent agent.

Other frameworks should match the behavior, even if the class names and async primitives differ.

## Summary

Async sub-agent workflows should feel simple to the parent model:

- start child work,
- continue reasoning,
- receive child results before the final answer is committed,
- produce one final parent response after all joined child work is complete.

The complexity belongs in the framework runtime, not in model prompts or user application glue code.
