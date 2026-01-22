package io.github.chirino.memory.history.runtime;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Default;
import jakarta.enterprise.inject.Instance;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Inject;
import org.jboss.logging.Logger;

/**
 * Producer for {@link ResponseResumer} that selects the appropriate implementation.
 * Prefers an enabled non-default resumer (e.g., RedisResponseResumer) over the default
 * NoopResponseResumer.
 */
@ApplicationScoped
public class ResponseResumerProducer {

    private static final Logger LOG = Logger.getLogger(ResponseResumerProducer.class);

    @Inject @Any Instance<ResponseResumer> allResumers;

    @Produces
    @ApplicationScoped
    @Default
    public ResponseResumer produceResponseResumer() {
        // First, try to find a non-default enabled resumer
        for (Instance.Handle<ResponseResumer> handle : allResumers.handles()) {
            ResponseResumer resumer = handle.get();
            // Skip the default bean (NoopResponseResumer)
            if (resumer instanceof NoopResponseResumer) {
                continue;
            }
            // If it's enabled, use it
            if (resumer.enabled()) {
                LOG.debugf("Using enabled ResponseResumer: %s", resumer.getClass().getName());
                return resumer;
            }
        }
        // Fall back to the default (NoopResponseResumer)
        // Use the static instance to avoid CDI lookup issues
        LOG.debugf("Using default ResponseResumer: NoopResponseResumer");
        return ResponseResumer.noop();
    }
}
