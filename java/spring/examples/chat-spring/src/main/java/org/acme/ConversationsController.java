package org.acme;

import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import io.github.chirino.memoryservice.client.model.Channel;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PatchMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/v1/conversations")
class ConversationsController {

    private final MemoryServiceProxy proxy;

    ConversationsController(MemoryServiceProxy proxy) {
        this.proxy = proxy;
    }

    @GetMapping
    public ResponseEntity<?> listConversations(
            @RequestParam(value = "mode", required = false) String mode,
            @RequestParam(value = "afterCursor", required = false) String afterCursor,
            @RequestParam(value = "limit", required = false) Integer limit,
            @RequestParam(value = "query", required = false) String query) {
        return proxy.listConversations(mode, afterCursor, limit, query);
    }

    @GetMapping("/{conversationId}")
    public ResponseEntity<?> getConversation(@PathVariable String conversationId) {
        return proxy.getConversation(conversationId);
    }

    @PatchMapping("/{conversationId}")
    public ResponseEntity<?> updateConversation(
            @PathVariable String conversationId, @RequestBody String body) {
        return proxy.updateConversation(conversationId, body);
    }

    @DeleteMapping("/{conversationId}")
    public ResponseEntity<?> deleteConversation(@PathVariable String conversationId) {
        return proxy.deleteConversation(conversationId);
    }

    @GetMapping("/{conversationId}/entries")
    public ResponseEntity<?> listConversationEntries(
            @PathVariable String conversationId,
            @RequestParam(value = "afterCursor", required = false) String afterCursor,
            @RequestParam(value = "limit", required = false) Integer limit,
            @RequestParam(value = "channel", required = false) String channel,
            @RequestParam(value = "epoch", required = false) String epoch,
            @RequestParam(value = "forks", required = false) String forks) {
        Channel channelEnum = channel != null ? Channel.fromValue(channel) : Channel.HISTORY;
        return proxy.listConversationEntries(
                conversationId, afterCursor, limit, channelEnum, epoch, forks);
    }

    @GetMapping("/{conversationId}/forks")
    public ResponseEntity<?> listConversationForks(@PathVariable String conversationId) {
        return proxy.listConversationForks(conversationId, null, null);
    }

    @GetMapping("/{conversationId}/memberships")
    public ResponseEntity<?> listConversationMemberships(@PathVariable String conversationId) {
        return proxy.listConversationMemberships(conversationId, null, null);
    }

    @PostMapping("/{conversationId}/memberships")
    public ResponseEntity<?> shareConversation(
            @PathVariable String conversationId, @RequestBody String body) {
        return proxy.shareConversation(conversationId, body);
    }

    @PatchMapping("/{conversationId}/memberships/{userId}")
    public ResponseEntity<?> updateConversationMembership(
            @PathVariable String conversationId,
            @PathVariable String userId,
            @RequestBody String body) {
        return proxy.updateConversationMembership(conversationId, userId, body);
    }

    @DeleteMapping("/{conversationId}/memberships/{userId}")
    public ResponseEntity<?> deleteConversationMembership(
            @PathVariable String conversationId, @PathVariable String userId) {
        return proxy.deleteConversationMembership(conversationId, userId);
    }

    @PostMapping("/search")
    public ResponseEntity<?> searchConversations(@RequestBody String body) {
        return proxy.searchConversations(body);
    }
}
