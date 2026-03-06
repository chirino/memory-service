package com.example.demo;

import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import io.github.chirino.memoryservice.client.model.Channel;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/v1/conversations")
class MemoryServiceProxyController {
    private final MemoryServiceProxy proxy;

    MemoryServiceProxyController(MemoryServiceProxy proxy) {
        this.proxy = proxy;
    }

    @GetMapping("/{conversationId}")
    public ResponseEntity<?> getConversation(@PathVariable String conversationId) {
        return proxy.getConversation(conversationId);
    }

    @GetMapping("/{conversationId}/entries")
    public ResponseEntity<?> listConversationEntries(
            @PathVariable String conversationId,
            @RequestParam(required = false) String afterCursor,
            @RequestParam(required = false) Integer limit,
            @RequestParam(required = false) String channel,
            @RequestParam(required = false) String epoch,
            @RequestParam(required = false) String forks) {
        Channel channelEnum = channel != null ? Channel.fromValue(channel) : Channel.HISTORY;
        return proxy.listConversationEntries(
                conversationId, afterCursor, limit, channelEnum, epoch, forks);
    }

    @GetMapping
    public ResponseEntity<?> listConversations(
            @RequestParam(value = "mode", required = false) String mode,
            @RequestParam(value = "afterCursor", required = false) String afterCursor,
            @RequestParam(value = "limit", required = false) Integer limit,
            @RequestParam(value = "query", required = false) String query) {
        return proxy.listConversations(mode, afterCursor, limit, query);
    }
}
