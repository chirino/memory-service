package org.acme;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

import io.github.chirino.memory.client.model.Channel;
import io.github.chirino.memory.runtime.MemoryServiceProxy;
import jakarta.ws.rs.core.Response;
import org.junit.jupiter.api.Test;

class ConversationsResourceTest {

    @Test
    void forwardsReversePaginationAndExplicitForkMode() {
        CapturingProxy proxy = new CapturingProxy();
        ConversationsResource resource = new ConversationsResource();
        resource.proxy = proxy;

        resource.listConversationEntries("conversation-1", null, "older-entry", true, 50, "none");

        assertEquals("conversation-1", proxy.conversationId);
        assertEquals(
                new MemoryServiceProxy.EntryListOptions(
                        null, "older-entry", true, 50, Channel.HISTORY, null, "none"),
                proxy.options);
    }

    @Test
    void defaultsOmittedForkModeToAncestryPath() {
        CapturingProxy proxy = new CapturingProxy();
        ConversationsResource resource = new ConversationsResource();
        resource.proxy = proxy;

        Response response =
                resource.listConversationEntries(
                        "conversation-1", "newer-entry", null, null, 25, null);

        assertNull(response);
        assertEquals(
                new MemoryServiceProxy.EntryListOptions(
                        "newer-entry", null, null, 25, Channel.HISTORY, null, "none"),
                proxy.options);
    }

    private static final class CapturingProxy extends MemoryServiceProxy {
        private String conversationId;
        private EntryListOptions options;

        @Override
        public Response listConversationEntries(String conversationId, EntryListOptions options) {
            this.conversationId = conversationId;
            this.options = options;
            return null;
        }
    }
}
