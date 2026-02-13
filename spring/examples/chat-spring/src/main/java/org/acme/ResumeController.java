package org.acme;

import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import io.github.chirino.memoryservice.history.ResponseResumer;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.io.IOException;
import java.util.List;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.StringUtils;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;
import reactor.core.Disposable;

@RestController
@RequestMapping("/v1/conversations")
class ResumeController {

    private final ResponseResumer responseResumer;
    private final MemoryServiceProxy proxy;
    private final OAuth2AuthorizedClientService authorizedClientService;

    ResumeController(
            ResponseResumer responseResumer,
            MemoryServiceProxy proxy,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        this.responseResumer = responseResumer;
        this.proxy = proxy;
        this.authorizedClientService = authorizedClientServiceProvider.getIfAvailable();
    }

    @PostMapping("/resume-check")
    public List<String> resumeCheck(@RequestBody(required = false) List<String> conversationIds) {
        if (conversationIds == null || conversationIds.isEmpty()) {
            return List.of();
        }
        return responseResumer.check(
                conversationIds, SecurityHelper.bearerToken(authorizedClientService));
    }

    @GetMapping(path = "/{conversationId}/resume", produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    public SseEmitter resume(@PathVariable String conversationId) {
        if (!StringUtils.hasText(conversationId)) {
            throw new ResponseStatusException(
                    HttpStatus.BAD_REQUEST, "Conversation ID is required");
        }

        SseEmitter emitter = new SseEmitter(0L);
        String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
        Disposable subscription =
                responseResumer
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
            emitter.send(SseEmitter.event().name("token").data(frame));
        } catch (IOException | IllegalStateException ignored) {
            // Client disconnected or emitter already completed
        }
    }

    private void safeComplete(SseEmitter emitter) {
        try {
            emitter.complete();
        } catch (IllegalStateException ignored) {
            // Emitter already completed
        }
    }

    private void safeCompleteWithError(SseEmitter emitter, Throwable failure) {
        try {
            emitter.completeWithError(failure);
        } catch (IllegalStateException ignored) {
            // Emitter already completed
        }
    }
}
