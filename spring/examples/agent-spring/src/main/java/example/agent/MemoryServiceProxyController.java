package example.agent;

import io.github.chirino.memoryservice.client.MemoryServiceProxy;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
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

    @GetMapping
    public ResponseEntity<?> listConversations(
            @RequestParam(value = "mode", required = false) String mode,
            @RequestParam(value = "after", required = false) String after,
            @RequestParam(value = "limit", required = false) Integer limit,
            @RequestParam(value = "query", required = false) String query) {
        return proxy.listConversations(mode, after, limit, query);
    }

    @GetMapping("/{conversationId}")
    public ResponseEntity<?> getConversation(@PathVariable String conversationId) {
        return proxy.getConversation(conversationId);
    }

    @DeleteMapping("/{conversationId}")
    public ResponseEntity<?> deleteConversation(@PathVariable String conversationId) {
        return proxy.deleteConversation(conversationId);
    }

    @GetMapping("/{conversationId}/entries")
    public ResponseEntity<?> listConversationEntries(
            @PathVariable String conversationId,
            @RequestParam(value = "after", required = false) String after,
            @RequestParam(value = "limit", required = false) Integer limit) {
        return proxy.listConversationEntries(conversationId, after, limit);
    }

    @PostMapping("/{conversationId}/entries/{entryId}/fork")
    public ResponseEntity<?> forkConversationAtEntry(
            @PathVariable String conversationId,
            @PathVariable String entryId,
            @RequestBody(required = false) String body) {
        return proxy.forkConversationAtEntry(conversationId, entryId, body);
    }

    @GetMapping("/{conversationId}/forks")
    public ResponseEntity<?> listConversationForks(@PathVariable String conversationId) {
        return proxy.listConversationForks(conversationId);
    }

    @PostMapping("/{conversationId}/memberships")
    public ResponseEntity<?> shareConversation(
            @PathVariable String conversationId, @RequestBody String body) {
        return proxy.shareConversation(conversationId, body);
    }

    @DeleteMapping("/{conversationId}/response")
    public ResponseEntity<?> cancelResponse(@PathVariable String conversationId) {
        return proxy.cancelResponse(conversationId);
    }
}
