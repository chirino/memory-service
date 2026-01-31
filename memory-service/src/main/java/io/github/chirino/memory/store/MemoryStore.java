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
import io.github.chirino.memory.api.dto.ForkFromEntryRequest;
import io.github.chirino.memory.api.dto.IndexTranscriptRequest;
import io.github.chirino.memory.api.dto.OwnershipTransferDto;
import io.github.chirino.memory.api.dto.PagedEntries;
import io.github.chirino.memory.api.dto.SearchEntriesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.ShareConversationRequest;
import io.github.chirino.memory.api.dto.SyncResult;
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

    EntryDto appendUserEntry(String userId, String conversationId, CreateUserEntryRequest request);

    List<ConversationMembershipDto> listMemberships(String userId, String conversationId);

    ConversationMembershipDto shareConversation(
            String userId, String conversationId, ShareConversationRequest request);

    ConversationMembershipDto updateMembership(
            String userId,
            String conversationId,
            String memberUserId,
            ShareConversationRequest request);

    void deleteMembership(String userId, String conversationId, String memberUserId);

    ConversationDto forkConversationAtEntry(
            String userId, String conversationId, String entryId, ForkFromEntryRequest request);

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
            String clientId);

    List<EntryDto> appendAgentEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId);

    SyncResult syncAgentEntry(
            String userId, String conversationId, CreateEntryRequest entry, String clientId);

    EntryDto indexTranscript(IndexTranscriptRequest request, String clientId);

    List<SearchResultDto> searchEntries(String userId, SearchEntriesRequest request);

    // Admin methods — no userId scoping, configurable deleted-resource visibility
    List<ConversationSummaryDto> adminListConversations(AdminConversationQuery query);

    Optional<ConversationDto> adminGetConversation(String conversationId);

    void adminDeleteConversation(String conversationId);

    void adminRestoreConversation(String conversationId);

    PagedEntries adminGetEntries(String conversationId, AdminMessageQuery query);

    List<ConversationMembershipDto> adminListMemberships(String conversationId);

    List<ConversationForkSummaryDto> adminListForks(String conversationId);

    List<SearchResultDto> adminSearchEntries(AdminSearchQuery query);

    // Eviction support
    List<String> findEvictableGroupIds(OffsetDateTime cutoff, int limit);

    long countEvictableGroups(OffsetDateTime cutoff);

    void hardDeleteConversationGroups(List<String> groupIds);

    // Memory epoch eviction support

    /**
     * Find epochs eligible for eviction (not latest, past retention).
     *
     * @param cutoff epochs with max(created_at) before this are eligible
     * @param limit maximum epochs to return
     * @return list of (conversationId, clientId, epoch) tuples
     */
    List<EpochKey> findEvictableEpochs(OffsetDateTime cutoff, int limit);

    /** Count entries in evictable epochs for progress estimation. */
    long countEvictableEpochEntries(OffsetDateTime cutoff);

    /**
     * Delete entries for the specified epochs. Also queues vector store cleanup tasks for affected
     * entries.
     *
     * @return number of entries deleted
     */
    int deleteEntriesForEpochs(List<EpochKey> epochs);
}
