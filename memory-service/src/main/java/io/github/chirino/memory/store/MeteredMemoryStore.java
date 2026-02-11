package io.github.chirino.memory.store;

import io.github.chirino.memory.api.ConversationListMode;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.CreateConversationRequest;
import io.github.chirino.memory.api.dto.CreateOwnershipTransferRequest;
import io.github.chirino.memory.api.dto.CreateUserEntryRequest;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.IndexConversationsResponse;
import io.github.chirino.memory.api.dto.IndexEntryRequest;
import io.github.chirino.memory.api.dto.OwnershipTransferDto;
import io.github.chirino.memory.api.dto.PagedEntries;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.api.dto.ShareConversationRequest;
import io.github.chirino.memory.api.dto.SyncResult;
import io.github.chirino.memory.api.dto.UnindexedEntriesResponse;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.model.AdminConversationQuery;
import io.github.chirino.memory.model.AdminMessageQuery;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.model.Channel;
import io.micrometer.core.instrument.MeterRegistry;
import java.time.OffsetDateTime;
import java.util.List;
import java.util.Optional;

/**
 * Decorator that wraps a MemoryStore implementation with timing metrics.
 * All operations are recorded using Micrometer timers with the metric name
 * "memory.store.operation" and an "operation" tag identifying the method.
 */
public class MeteredMemoryStore implements MemoryStore {

    private final MeterRegistry registry;
    private final MemoryStore delegate;

    public MeteredMemoryStore(MeterRegistry registry, MemoryStore delegate) {
        this.registry = registry;
        this.delegate = delegate;
    }

    @Override
    public ConversationDto createConversation(String userId, CreateConversationRequest request) {
        return registry.timer("memory.store.operation", "operation", "createConversation")
                .record(() -> delegate.createConversation(userId, request));
    }

    @Override
    public List<ConversationSummaryDto> listConversations(
            String userId, String query, String after, int limit, ConversationListMode mode) {
        return registry.timer("memory.store.operation", "operation", "listConversations")
                .record(() -> delegate.listConversations(userId, query, after, limit, mode));
    }

    @Override
    public ConversationDto getConversation(String userId, String conversationId) {
        return registry.timer("memory.store.operation", "operation", "getConversation")
                .record(() -> delegate.getConversation(userId, conversationId));
    }

    @Override
    public void deleteConversation(String userId, String conversationId) {
        registry.timer("memory.store.operation", "operation", "deleteConversation")
                .record(() -> delegate.deleteConversation(userId, conversationId));
    }

    @Override
    public EntryDto appendUserEntry(
            String userId, String conversationId, CreateUserEntryRequest request) {
        return registry.timer("memory.store.operation", "operation", "appendUserEntry")
                .record(() -> delegate.appendUserEntry(userId, conversationId, request));
    }

    @Override
    public List<ConversationMembershipDto> listMemberships(String userId, String conversationId) {
        return registry.timer("memory.store.operation", "operation", "listMemberships")
                .record(() -> delegate.listMemberships(userId, conversationId));
    }

    @Override
    public ConversationMembershipDto shareConversation(
            String userId, String conversationId, ShareConversationRequest request) {
        return registry.timer("memory.store.operation", "operation", "shareConversation")
                .record(() -> delegate.shareConversation(userId, conversationId, request));
    }

    @Override
    public ConversationMembershipDto updateMembership(
            String userId,
            String conversationId,
            String memberUserId,
            ShareConversationRequest request) {
        return registry.timer("memory.store.operation", "operation", "updateMembership")
                .record(
                        () ->
                                delegate.updateMembership(
                                        userId, conversationId, memberUserId, request));
    }

    @Override
    public void deleteMembership(String userId, String conversationId, String memberUserId) {
        registry.timer("memory.store.operation", "operation", "deleteMembership")
                .record(() -> delegate.deleteMembership(userId, conversationId, memberUserId));
    }

    @Override
    public List<ConversationForkSummaryDto> listForks(String userId, String conversationId) {
        return registry.timer("memory.store.operation", "operation", "listForks")
                .record(() -> delegate.listForks(userId, conversationId));
    }

    @Override
    public List<OwnershipTransferDto> listPendingTransfers(String userId, String role) {
        return registry.timer("memory.store.operation", "operation", "listPendingTransfers")
                .record(() -> delegate.listPendingTransfers(userId, role));
    }

    @Override
    public Optional<OwnershipTransferDto> getTransfer(String userId, String transferId) {
        return registry.timer("memory.store.operation", "operation", "getTransfer")
                .record(() -> delegate.getTransfer(userId, transferId));
    }

    @Override
    public OwnershipTransferDto createOwnershipTransfer(
            String userId, CreateOwnershipTransferRequest request) {
        return registry.timer("memory.store.operation", "operation", "createOwnershipTransfer")
                .record(() -> delegate.createOwnershipTransfer(userId, request));
    }

    @Override
    public void acceptTransfer(String userId, String transferId) {
        registry.timer("memory.store.operation", "operation", "acceptTransfer")
                .record(() -> delegate.acceptTransfer(userId, transferId));
    }

    @Override
    public void deleteTransfer(String userId, String transferId) {
        registry.timer("memory.store.operation", "operation", "deleteTransfer")
                .record(() -> delegate.deleteTransfer(userId, transferId));
    }

    @Override
    public PagedEntries getEntries(
            String userId,
            String conversationId,
            String afterEntryId,
            int limit,
            Channel channel,
            MemoryEpochFilter epochFilter,
            String clientId,
            boolean allForks) {
        return registry.timer("memory.store.operation", "operation", "getEntries")
                .record(
                        () ->
                                delegate.getEntries(
                                        userId,
                                        conversationId,
                                        afterEntryId,
                                        limit,
                                        channel,
                                        epochFilter,
                                        clientId,
                                        allForks));
    }

    @Override
    public List<EntryDto> appendAgentEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId,
            Long epoch) {
        return registry.timer("memory.store.operation", "operation", "appendAgentEntries")
                .record(
                        () ->
                                delegate.appendAgentEntries(
                                        userId, conversationId, entries, clientId, epoch));
    }

    @Override
    public SyncResult syncAgentEntry(
            String userId, String conversationId, CreateEntryRequest entry, String clientId) {
        return registry.timer("memory.store.operation", "operation", "syncAgentEntry")
                .record(() -> delegate.syncAgentEntry(userId, conversationId, entry, clientId));
    }

    @Override
    public IndexConversationsResponse indexEntries(List<IndexEntryRequest> entries) {
        return registry.timer("memory.store.operation", "operation", "indexEntries")
                .record(() -> delegate.indexEntries(entries));
    }

    @Override
    public UnindexedEntriesResponse listUnindexedEntries(int limit, String cursor) {
        return registry.timer("memory.store.operation", "operation", "listUnindexedEntries")
                .record(() -> delegate.listUnindexedEntries(limit, cursor));
    }

    @Override
    public List<EntryDto> findEntriesPendingVectorIndexing(int limit) {
        return registry.timer(
                        "memory.store.operation", "operation", "findEntriesPendingVectorIndexing")
                .record(() -> delegate.findEntriesPendingVectorIndexing(limit));
    }

    @Override
    public void setIndexedAt(String entryId, OffsetDateTime indexedAt) {
        registry.timer("memory.store.operation", "operation", "setIndexedAt")
                .record(() -> delegate.setIndexedAt(entryId, indexedAt));
    }

    @Override
    public List<ConversationSummaryDto> adminListConversations(AdminConversationQuery query) {
        return registry.timer("memory.store.operation", "operation", "adminListConversations")
                .record(() -> delegate.adminListConversations(query));
    }

    @Override
    public Optional<ConversationDto> adminGetConversation(String conversationId) {
        return registry.timer("memory.store.operation", "operation", "adminGetConversation")
                .record(() -> delegate.adminGetConversation(conversationId));
    }

    @Override
    public void adminDeleteConversation(String conversationId) {
        registry.timer("memory.store.operation", "operation", "adminDeleteConversation")
                .record(() -> delegate.adminDeleteConversation(conversationId));
    }

    @Override
    public void adminRestoreConversation(String conversationId) {
        registry.timer("memory.store.operation", "operation", "adminRestoreConversation")
                .record(() -> delegate.adminRestoreConversation(conversationId));
    }

    @Override
    public PagedEntries adminGetEntries(String conversationId, AdminMessageQuery query) {
        return registry.timer("memory.store.operation", "operation", "adminGetEntries")
                .record(() -> delegate.adminGetEntries(conversationId, query));
    }

    @Override
    public List<ConversationMembershipDto> adminListMemberships(String conversationId) {
        return registry.timer("memory.store.operation", "operation", "adminListMemberships")
                .record(() -> delegate.adminListMemberships(conversationId));
    }

    @Override
    public List<ConversationForkSummaryDto> adminListForks(String conversationId) {
        return registry.timer("memory.store.operation", "operation", "adminListForks")
                .record(() -> delegate.adminListForks(conversationId));
    }

    @Override
    public SearchResultsDto adminSearchEntries(AdminSearchQuery query) {
        return registry.timer("memory.store.operation", "operation", "adminSearchEntries")
                .record(() -> delegate.adminSearchEntries(query));
    }

    @Override
    public List<String> findEvictableGroupIds(OffsetDateTime cutoff, int limit) {
        return registry.timer("memory.store.operation", "operation", "findEvictableGroupIds")
                .record(() -> delegate.findEvictableGroupIds(cutoff, limit));
    }

    @Override
    public long countEvictableGroups(OffsetDateTime cutoff) {
        return registry.timer("memory.store.operation", "operation", "countEvictableGroups")
                .record(() -> delegate.countEvictableGroups(cutoff));
    }

    @Override
    public void hardDeleteConversationGroups(List<String> groupIds) {
        registry.timer("memory.store.operation", "operation", "hardDeleteConversationGroups")
                .record(() -> delegate.hardDeleteConversationGroups(groupIds));
    }
}
