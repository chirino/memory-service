package io.github.chirino.memory.history.annotations;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Marks the {@code @RecordConversation} method parameter that supplies the entry agent ID.
 *
 * <p>One value applies to the whole invocation: the intercepted user entry and, for
 * non-streaming results, the recorded AI entry. Streaming ({@code Multi}) results are recorded
 * without it. When the delegating message and the response need different agent IDs, use
 * {@code ConversationStore} manual appends around the call instead.
 */
@Retention(RetentionPolicy.RUNTIME)
@Target(ElementType.PARAMETER)
public @interface AgentId {}
