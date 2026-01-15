package io.github.chirino.memory.langchain4j;

import jakarta.enterprise.context.ApplicationScoped;
import java.util.function.Supplier;

/**
 * Utility that ensures the CDI request context is active while executing the provided work.
 * Using @ActivateRequestContext here allows Quarkus to manage the context lifecycle and
 * SmallRye Context Propagation to capture it for asynchronous continuations.
 */
@ApplicationScoped
public class RequestContextExecutor {

    //    @ActivateRequestContext
    public void run(Runnable runnable) {
        runnable.run();
    }

    //    @ActivateRequestContext
    public <T> T call(Supplier<T> supplier) {
        return supplier.get();
    }
}
