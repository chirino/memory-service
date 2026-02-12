package io.github.chirino.memory.store;

import io.github.chirino.memory.api.ConversationListMode;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.CreateConversationRequest;
import io.github.chirino.memory.api.dto.CreateOwnershipTransferRequest;
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
import java.time.OffsetDateTime;
import java.util.List;
import java.util.Optional;

public interface MemoryStore {

    ConversationDto createConversation(String userId, CreateConversationRequest request);

    List<ConversationSummaryDto> listConversations(
            String userId, String query, String after, int limit, ConversationListMode mode);

    ConversationDto getConversation(String userId, String conversationId);

    void deleteConversation(String userId, String conversationId);

    List<ConversationMembershipDto> listMemberships(String userId, String conversationId);

    ConversationMembershipDto shareConversation(
            String userId, String conversationId, ShareConversationRequest request);

    ConversationMembershipDto updateMembership(
            String userId,
            String conversationId,
            String memberUserId,
            ShareConversationRequest request);

    void deleteMembership(String userId, String conversationId, String memberUserId);

    List<ConversationForkSummaryDto> listForks(String userId, String conversationId);

    // Ownership transfer methods (transfers are always "pending" while they exist)
    List<OwnershipTransferDto> listPendingTransfers(String userId, String role);

    Optional<OwnershipTransferDto> getTransfer(String userId, String transferId);

    OwnershipTransferDto createOwnershipTransfer(
            String userId, CreateOwnershipTransferRequest request);

    /** Accept transfer - performs ownership swap and deletes the transfer record. */
    void acceptTransfer(String userId, String transferId);

    void deleteTransfer(String userId, String transferId);

    PagedEntries getEntries(
            String userId,
            String conversationId,
            String afterEntryId,
            int limit,
            Channel channel,
            MemoryEpochFilter epochFilter,
            String clientId,
            boolean allForks);

    /**
     * Appends entries to a conversation. For MEMORY channel entries, an epoch is required.
     *
     * @param userId the user making the request
     * @param conversationId the conversation to append to
     * @param entries the entries to append
     * @param clientId the client ID (required for MEMORY channel)
     * @param epoch the epoch for MEMORY channel entries; if null, the latest epoch is used
     *              (or 1 if no entries exist yet)
     * @return the created entries
     */
    List<EntryDto> appendMemoryEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId,
            Long epoch);

    SyncResult syncAgentEntry(
            String userId, String conversationId, CreateEntryRequest entry, String clientId);

    /**
     * Index entries for search. Updates indexedContent for each entry and attempts
     * vector store indexing. On vector store failure, creates a singleton retry task.
     *
     * @param entries list of entries with conversationId, entryId, and text
     * @return response with count of entries processed
     */
    IndexConversationsResponse indexEntries(List<IndexEntryRequest> entries);

    /**
     * List entries that need indexing (history channel, indexed_content IS NULL).
     * Results are sorted by createdAt for consistent pagination.
     *
     * @param limit maximum number of entries to return
     * @param cursor pagination cursor from previous response
     * @return paginated response with entries and cursor
     */
    UnindexedEntriesResponse listUnindexedEntries(int limit, String cursor);

    /**
     * Find entries pending vector store indexing (indexed_content IS NOT NULL AND indexed_at IS NULL).
     * Used by the retry task to find entries that failed vector store indexing.
     *
     * @param limit maximum number of entries to return
     * @return list of entries pending vector indexing
     */
    List<EntryDto> findEntriesPendingVectorIndexing(int limit);

    /**
     * Update indexed_at timestamp after successful vector store indexing.
     *
     * @param entryId the entry ID to update
     * @param indexedAt the timestamp when the entry was indexed
     */
    void setIndexedAt(String entryId, OffsetDateTime indexedAt);

    // Admin methods — no userId scoping, configurable deleted-resource visibility
    List<ConversationSummaryDto> adminListConversations(AdminConversationQuery query);

    Optional<ConversationDto> adminGetConversation(String conversationId);

    void adminDeleteConversation(String conversationId);

    void adminRestoreConversation(String conversationId);

    PagedEntries adminGetEntries(String conversationId, AdminMessageQuery query);

    List<ConversationMembershipDto> adminListMemberships(String conversationId);

    List<ConversationForkSummaryDto> adminListForks(String conversationId);

    SearchResultsDto adminSearchEntries(AdminSearchQuery query);

    // Eviction support
    List<String> findEvictableGroupIds(OffsetDateTime cutoff, int limit);

    long countEvictableGroups(OffsetDateTime cutoff);

    void hardDeleteConversationGroups(List<String> groupIds);
}
