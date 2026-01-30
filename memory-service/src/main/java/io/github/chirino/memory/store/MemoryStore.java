package io.github.chirino.memory.store;

import io.github.chirino.memory.api.ConversationListMode;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.CreateConversationRequest;
import io.github.chirino.memory.api.dto.CreateUserEntryRequest;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.ForkFromEntryRequest;
import io.github.chirino.memory.api.dto.IndexTranscriptRequest;
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

    void requestOwnershipTransfer(String userId, String conversationId, String newOwnerUserId);

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

    SyncResult syncAgentEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId);

    EntryDto indexTranscript(IndexTranscriptRequest request, String clientId);

    List<SearchResultDto> searchEntries(String userId, SearchEntriesRequest request);

    // Admin methods — no userId scoping, configurable deleted-resource visibility
    List<ConversationSummaryDto> adminListConversations(AdminConversationQuery query);

    Optional<ConversationDto> adminGetConversation(String conversationId);

    void adminDeleteConversation(String conversationId);

    void adminRestoreConversation(String conversationId);

    PagedEntries adminGetEntries(String conversationId, AdminMessageQuery query);

    List<ConversationMembershipDto> adminListMemberships(String conversationId);

    List<SearchResultDto> adminSearchEntries(AdminSearchQuery query);

    // Eviction support
    List<String> findEvictableGroupIds(OffsetDateTime cutoff, int limit);

    long countEvictableGroups(OffsetDateTime cutoff);

    void hardDeleteConversationGroups(List<String> groupIds);

    long countEvictableMemberships(OffsetDateTime cutoff);

    int hardDeleteMembershipsBatch(OffsetDateTime cutoff, int limit);
}
