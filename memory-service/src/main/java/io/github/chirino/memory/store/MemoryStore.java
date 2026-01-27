package io.github.chirino.memory.store;

import io.github.chirino.memory.api.ConversationListMode;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.CreateConversationRequest;
import io.github.chirino.memory.api.dto.CreateSummaryRequest;
import io.github.chirino.memory.api.dto.CreateUserMessageRequest;
import io.github.chirino.memory.api.dto.ForkFromMessageRequest;
import io.github.chirino.memory.api.dto.MessageDto;
import io.github.chirino.memory.api.dto.PagedMessages;
import io.github.chirino.memory.api.dto.SearchMessagesRequest;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.ShareConversationRequest;
import io.github.chirino.memory.api.dto.SyncResult;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.model.AdminConversationQuery;
import io.github.chirino.memory.model.AdminMessageQuery;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.model.MessageChannel;
import java.util.List;
import java.util.Optional;

public interface MemoryStore {

    ConversationDto createConversation(String userId, CreateConversationRequest request);

    List<ConversationSummaryDto> listConversations(
            String userId, String query, String after, int limit, ConversationListMode mode);

    ConversationDto getConversation(String userId, String conversationId);

    void deleteConversation(String userId, String conversationId);

    MessageDto appendUserMessage(
            String userId, String conversationId, CreateUserMessageRequest request);

    List<ConversationMembershipDto> listMemberships(String userId, String conversationId);

    ConversationMembershipDto shareConversation(
            String userId, String conversationId, ShareConversationRequest request);

    ConversationMembershipDto updateMembership(
            String userId,
            String conversationId,
            String memberUserId,
            ShareConversationRequest request);

    void deleteMembership(String userId, String conversationId, String memberUserId);

    ConversationDto forkConversationAtMessage(
            String userId, String conversationId, String messageId, ForkFromMessageRequest request);

    List<ConversationForkSummaryDto> listForks(String userId, String conversationId);

    void requestOwnershipTransfer(String userId, String conversationId, String newOwnerUserId);

    PagedMessages getMessages(
            String userId,
            String conversationId,
            String afterMessageId,
            int limit,
            MessageChannel channel,
            MemoryEpochFilter epochFilter,
            String clientId);

    List<MessageDto> appendAgentMessages(
            String userId,
            String conversationId,
            List<CreateMessageRequest> messages,
            String clientId);

    SyncResult syncAgentMessages(
            String userId,
            String conversationId,
            List<CreateMessageRequest> messages,
            String clientId);

    MessageDto createSummary(String conversationId, CreateSummaryRequest request, String clientId);

    List<SearchResultDto> searchMessages(String userId, SearchMessagesRequest request);

    // Admin methods — no userId scoping, configurable deleted-resource visibility
    List<ConversationSummaryDto> adminListConversations(AdminConversationQuery query);

    Optional<ConversationDto> adminGetConversation(String conversationId, boolean includeDeleted);

    void adminDeleteConversation(String conversationId);

    void adminRestoreConversation(String conversationId);

    PagedMessages adminGetMessages(String conversationId, AdminMessageQuery query);

    List<ConversationMembershipDto> adminListMemberships(
            String conversationId, boolean includeDeleted);

    List<SearchResultDto> adminSearchMessages(AdminSearchQuery query);
}
