package com.example.demo;

import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import io.github.chirino.memoryservice.history.ResponseRecordingManager;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.io.IOException;
import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;
import reactor.core.Disposable;

@RestController
@RequestMapping("/v1/conversations")
class ResumeController {
    private static final Logger LOG = LoggerFactory.getLogger(ResumeController.class);

    private final ResponseRecordingManager recordingManager;
    private final MemoryServiceProxy proxy;
    private final OAuth2AuthorizedClientService authorizedClientService;

    ResumeController(
            ResponseRecordingManager recordingManager,
            MemoryServiceProxy proxy,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        this.recordingManager = recordingManager;
        this.proxy = proxy;
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
    }

    @PostMapping("/resume-check")
    public List<String> check(@RequestBody List<String> conversationIds) {
        return recordingManager.check(
                conversationIds, SecurityHelper.bearerToken(authorizedClientService));
    }

    @GetMapping(path = "/{conversationId}/resume", produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    public SseEmitter resume(@PathVariable String conversationId) {
        String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
        SseEmitter emitter = new SseEmitter(0L);

        Disposable subscription =
                recordingManager
                        .replay(conversationId, bearerToken)
                        .subscribe(
                                chunk ->
                                        safeSendChunk(
                                                emitter, new ChatController.TokenFrame(chunk)),
                                failure -> safeCompleteWithError(emitter, failure),
                                () -> safeComplete(emitter));

        emitter.onCompletion(subscription::dispose);
        emitter.onTimeout(
                () -> {
                    subscription.dispose();
                    safeComplete(emitter);
                });
        return emitter;
    }

    @PostMapping("/{conversationId}/cancel")
    public ResponseEntity<?> cancelResponse(@PathVariable String conversationId) {
        return proxy.cancelResponse(conversationId);
    }

    private void safeSendChunk(SseEmitter emitter, ChatController.TokenFrame frame) {
        try {
            emitter.send(SseEmitter.event().data(frame));
        } catch (IOException | IllegalStateException ignored) {
            // Client disconnected or emitter already completed
        }
    }

    private void safeComplete(SseEmitter emitter) {
        try {
            emitter.complete();
        } catch (IllegalStateException ignored) {
            // Emitter already completed.
        }
    }

    private void safeCompleteWithError(SseEmitter emitter, Throwable failure) {
        LOG.warn("Replay stream failed", failure);
        try {
            emitter.completeWithError(failure);
        } catch (IllegalStateException ignored) {
            // Emitter already completed.
        }
    }
}
