package io.github.chirino.memory.history.annotations;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Marks a parameter as containing attachment metadata for history recording. The parameter should be
 * a {@code List<Map<String, Object>>} where each map contains keys like {@code attachmentId},
 * {@code contentType}, and {@code name}.
 *
 * <p>When present on a method parameter, the {@link
 * io.github.chirino.memory.history.runtime.ConversationInterceptor} will use these values for
 * storing attachment references instead of extracting from {@code @ImageUrl}.
 */
@Retention(RetentionPolicy.RUNTIME)
@Target(ElementType.PARAMETER)
public @interface Attachments {}
