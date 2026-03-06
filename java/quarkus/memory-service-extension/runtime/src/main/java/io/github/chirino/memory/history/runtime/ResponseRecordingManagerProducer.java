package io.github.chirino.memory.history.runtime;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Default;
import jakarta.enterprise.inject.Instance;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Inject;
import org.jboss.logging.Logger;

/**
 * Producer for {@link ResponseRecordingManager} that selects the appropriate implementation.
 * Prefers an enabled non-default resumer (e.g., RedisResponseRecordingManager) over the default
 * NoopResponseRecordingManager.
 */
@ApplicationScoped
public class ResponseRecordingManagerProducer {

    private static final Logger LOG = Logger.getLogger(ResponseRecordingManagerProducer.class);

    @Inject @Any Instance<ResponseRecordingManager> allResumers;

    @Produces
    @ApplicationScoped
    @Default
    public ResponseRecordingManager produceResponseRecordingManager() {
        // First, try to find a non-default enabled resumer
        for (Instance.Handle<ResponseRecordingManager> handle : allResumers.handles()) {
            ResponseRecordingManager resumer = handle.get();
            // Skip the default bean (NoopResponseRecordingManager)
            if (resumer instanceof NoopResponseRecordingManager) {
                continue;
            }
            // If it's enabled, use it
            if (resumer.enabled()) {
                LOG.debugf(
                        "Using enabled ResponseRecordingManager: %s", resumer.getClass().getName());
                return resumer;
            }
        }
        // Fall back to the default (NoopResponseRecordingManager)
        // Use the static instance to avoid CDI lookup issues
        LOG.debugf("Using default ResponseRecordingManager: NoopResponseRecordingManager");
        return ResponseRecordingManager.noop();
    }
}
