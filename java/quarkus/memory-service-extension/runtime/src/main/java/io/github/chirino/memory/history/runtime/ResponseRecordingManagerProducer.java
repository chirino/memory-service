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
 * Prefers an enabled non-default recording manager (e.g., RedisResponseRecordingManager) over the
 * default NoopResponseRecordingManager.
 */
@ApplicationScoped
public class ResponseRecordingManagerProducer {

    private static final Logger LOG = Logger.getLogger(ResponseRecordingManagerProducer.class);

    @Inject @Any Instance<ResponseRecordingManager> allRecordingManagers;

    @Produces
    @ApplicationScoped
    @Default
    public ResponseRecordingManager produceResponseRecordingManager() {
        // First, try to find a non-default enabled recording manager.
        for (Instance.Handle<ResponseRecordingManager> handle : allRecordingManagers.handles()) {
            ResponseRecordingManager recordingManager = handle.get();
            // Skip the default bean (NoopResponseRecordingManager)
            if (recordingManager instanceof NoopResponseRecordingManager) {
                continue;
            }
            // If it's enabled, use it
            if (recordingManager.enabled()) {
                LOG.debugf(
                        "Using enabled ResponseRecordingManager: %s",
                        recordingManager.getClass().getName());
                return recordingManager;
            }
        }
        // Fall back to the default (NoopResponseRecordingManager)
        // Use the static instance to avoid CDI lookup issues
        LOG.debugf("Using default ResponseRecordingManager: NoopResponseRecordingManager");
        return ResponseRecordingManager.noop();
    }
}
