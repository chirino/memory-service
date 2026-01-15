package io.github.chirino.memory.store.impl;

import com.fasterxml.jackson.databind.ObjectMapper;
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
import io.github.chirino.memory.config.VectorStoreSelector;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.model.MessageChannel;
import io.github.chirino.memory.mongo.model.MongoConversation;
import io.github.chirino.memory.mongo.model.MongoConversationGroup;
import io.github.chirino.memory.mongo.model.MongoConversationMembership;
import io.github.chirino.memory.mongo.model.MongoConversationOwnershipTransfer;
import io.github.chirino.memory.mongo.model.MongoMessage;
import io.github.chirino.memory.mongo.repo.MongoConversationGroupRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationOwnershipTransferRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationRepository;
import io.github.chirino.memory.mongo.repo.MongoMessageRepository;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.MemoryEpochFilter;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.github.chirino.memory.vector.EmbeddingService;
import io.github.chirino.memory.vector.VectorStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.stream.Collectors;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import org.jboss.logging.Logger;

@ApplicationScoped
public class MongoMemoryStore implements MemoryStore {

    private static final Logger LOG = Logger.getLogger(MongoMemoryStore.class);
    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @Inject MongoConversationRepository conversationRepository;

    @Inject MongoConversationGroupRepository conversationGroupRepository;

    @Inject MongoConversationMembershipRepository membershipRepository;

    @Inject MongoMessageRepository messageRepository;

    @Inject MongoConversationOwnershipTransferRepository ownershipTransferRepository;

    @Inject ObjectMapper objectMapper;

    @Inject DataEncryptionService dataEncryptionService;

    @Inject VectorStoreSelector vectorStoreSelector;

    @Inject EmbeddingService embeddingService;

    @Override
    @Transactional
    public ConversationDto createConversation(String userId, CreateConversationRequest request) {
        MongoConversation c = new MongoConversation();
        c.id = UUID.randomUUID().toString();
        MongoConversationGroup group = new MongoConversationGroup();
        group.id = c.id;
        group.createdAt = Instant.now();
        conversationGroupRepository.persist(group);
        c.conversationGroupId = group.id;
        c.ownerUserId = userId;
        c.title = encryptTitle(request.getTitle());
        c.metadata = request.getMetadata() != null ? request.getMetadata() : Collections.emptyMap();
        Instant now = Instant.now();
        c.createdAt = now;
        c.updatedAt = now;
        conversationRepository.persist(c);

        membershipRepository.createMembership(c.conversationGroupId, userId, AccessLevel.OWNER);

        return toConversationDto(c, AccessLevel.OWNER, null);
    }

    @Override
    public List<ConversationSummaryDto> listConversations(
            String userId, String query, String after, int limit, ConversationListMode mode) {
        List<MongoConversationMembership> memberships =
                membershipRepository.listForUser(userId, limit);
        if (mode == ConversationListMode.ROOTS) {
            Map<String, AccessLevel> accessByGroup =
                    memberships.stream()
                            .collect(
                                    Collectors.toMap(
                                            membership -> membership.conversationGroupId,
                                            membership -> membership.accessLevel,
                                            (left, right) -> left));
            if (accessByGroup.isEmpty()) {
                return List.of();
            }
            List<MongoConversation> roots =
                    conversationRepository
                            .find(
                                    "conversationGroupId in ?1 and forkedAtMessageId is null and"
                                            + " forkedAtConversationId is null",
                                    accessByGroup.keySet())
                            .list();
            return roots.stream()
                    .map(
                            c ->
                                    toConversationSummaryDto(
                                            c, accessByGroup.get(c.conversationGroupId), null))
                    .sorted(Comparator.comparing(ConversationSummaryDto::getUpdatedAt).reversed())
                    .limit(limit)
                    .collect(Collectors.toList());
        }

        Map<String, AccessLevel> accessByGroup =
                memberships.stream()
                        .collect(
                                Collectors.toMap(
                                        membership -> membership.conversationGroupId,
                                        membership -> membership.accessLevel));
        if (accessByGroup.isEmpty()) {
            return List.of();
        }
        Set<String> groupIds = accessByGroup.keySet();
        List<MongoConversation> candidates =
                conversationRepository.find("conversationGroupId in ?1", groupIds).list();

        if (mode == ConversationListMode.LATEST_FORK) {
            Map<String, MongoConversation> latestByGroup = new HashMap<>();
            for (MongoConversation candidate : candidates) {
                String groupId = candidate.conversationGroupId;
                if (!accessByGroup.containsKey(groupId)) {
                    continue;
                }
                MongoConversation current = latestByGroup.get(groupId);
                if (current == null || candidate.updatedAt.isAfter(current.updatedAt)) {
                    latestByGroup.put(groupId, candidate);
                }
            }
            return latestByGroup.values().stream()
                    .map(
                            entity ->
                                    toConversationSummaryDto(
                                            entity,
                                            accessByGroup.get(entity.conversationGroupId),
                                            null))
                    .sorted(Comparator.comparing(ConversationSummaryDto::getUpdatedAt).reversed())
                    .limit(limit)
                    .collect(Collectors.toList());
        }

        return candidates.stream()
                .map(
                        entity ->
                                toConversationSummaryDto(
                                        entity,
                                        accessByGroup.get(entity.conversationGroupId),
                                        null))
                .filter(dto -> dto.getAccessLevel() != null)
                .sorted(Comparator.comparing(ConversationSummaryDto::getUpdatedAt).reversed())
                .limit(limit)
                .collect(Collectors.toList());
    }

    @Override
    public ConversationDto getConversation(String userId, String conversationId) {
        MongoConversation c = conversationRepository.findById(conversationId);
        if (c == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = c.conversationGroupId;
        MongoConversationMembership membership =
                membershipRepository
                        .findMembership(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        return toConversationDto(c, membership.accessLevel, null);
    }

    @Override
    @Transactional
    public void deleteConversation(String userId, String conversationId) {
        String groupId = resolveGroupId(conversationId);
        MongoConversationMembership membership =
                membershipRepository
                        .findMembership(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        if (membership.accessLevel != AccessLevel.OWNER
                && membership.accessLevel != AccessLevel.MANAGER) {
            throw new AccessDeniedException("Only owner or manager can delete conversation");
        }
        messageRepository.delete("conversationGroupId", groupId);
        conversationRepository.delete("conversationGroupId", groupId);
        membershipRepository.delete("conversationGroupId", groupId);
        ownershipTransferRepository.delete("conversationGroupId", groupId);
        conversationGroupRepository.deleteById(groupId);
    }

    @Override
    @Transactional
    public MessageDto appendUserMessage(
            String userId, String conversationId, CreateUserMessageRequest request) {
        MongoConversation c = conversationRepository.findById(conversationId);

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (c == null) {
            c = new MongoConversation();
            c.id = conversationId;
            c.conversationGroupId = conversationId;
            if (conversationGroupRepository.findById(conversationId) == null) {
                MongoConversationGroup group = new MongoConversationGroup();
                group.id = conversationId;
                group.createdAt = Instant.now();
                conversationGroupRepository.persist(group);
            }
            c.ownerUserId = userId;
            String inferredTitle = inferTitleFromUserMessage(request);
            c.title = encryptTitle(inferredTitle);
            c.metadata = Collections.emptyMap();
            Instant now = Instant.now();
            c.createdAt = now;
            c.updatedAt = now;
            conversationRepository.persist(c);
            membershipRepository.createMembership(c.conversationGroupId, userId, AccessLevel.OWNER);
        } else {
            String groupId = c.conversationGroupId;
            ensureHasAccess(groupId, userId, AccessLevel.WRITER);
        }

        MongoMessage m = new MongoMessage();
        m.id = UUID.randomUUID().toString();
        m.conversationId = conversationId;
        m.userId = userId;
        m.channel = MessageChannel.HISTORY;
        m.memoryEpoch = null;
        m.conversationGroupId = c.conversationGroupId;
        m.decodedContent = List.of(Map.of("type", "text", "text", request.getContent()));
        m.content = encryptContent(m.decodedContent);
        Instant createdAt = Instant.now();
        m.createdAt = createdAt;
        messageRepository.persist(m);
        c.updatedAt = createdAt;
        conversationRepository.persistOrUpdate(c);
        return toMessageDto(m);
    }

    @Override
    public List<ConversationMembershipDto> listMemberships(String userId, String conversationId) {
        String groupId = resolveGroupId(conversationId);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);
        return membershipRepository.listForConversationGroup(groupId).stream()
                .map(this::toMembershipDto)
                .collect(Collectors.toList());
    }

    @Override
    @Transactional
    public ConversationMembershipDto shareConversation(
            String userId, String conversationId, ShareConversationRequest request) {
        String groupId = resolveGroupId(conversationId);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);
        MongoConversation c = conversationRepository.findById(conversationId);
        if (c == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        MongoConversationMembership m =
                membershipRepository.createMembership(
                        groupId, request.getUserId(), request.getAccessLevel());
        return toMembershipDto(m);
    }

    @Override
    @Transactional
    public ConversationMembershipDto updateMembership(
            String userId,
            String conversationId,
            String memberUserId,
            ShareConversationRequest request) {
        String groupId = resolveGroupId(conversationId);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);
        MongoConversationMembership m =
                membershipRepository
                        .findMembership(groupId, memberUserId)
                        .orElseThrow(
                                () -> new ResourceNotFoundException("membership", memberUserId));
        if (request.getAccessLevel() != null) {
            m.accessLevel = request.getAccessLevel();
            membershipRepository.update(m);
        }
        return toMembershipDto(m);
    }

    @Override
    @Transactional
    public void deleteMembership(String userId, String conversationId, String memberUserId) {
        String groupId = resolveGroupId(conversationId);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);
        membershipRepository
                .findMembership(groupId, memberUserId)
                .ifPresent(membershipRepository::delete);
    }

    @Override
    @Transactional
    public ConversationDto forkConversationAtMessage(
            String userId,
            String conversationId,
            String messageId,
            ForkFromMessageRequest request) {
        MongoConversation original = conversationRepository.findById(conversationId);
        if (original == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = original.conversationGroupId;
        ensureHasAccess(groupId, userId, AccessLevel.WRITER);

        MongoMessage target =
                messageRepository
                        .findByIdOptional(messageId)
                        .orElseThrow(() -> new ResourceNotFoundException("message", messageId));
        if (!conversationId.equals(target.conversationId)) {
            throw new ResourceNotFoundException("message", messageId);
        }
        if (target.channel != MessageChannel.HISTORY) {
            throw new AccessDeniedException("Forking is only allowed for history messages");
        }

        MongoMessage previous =
                messageRepository
                        .find(
                                "conversationId = ?1 and channel = ?2 and (createdAt < ?3 or"
                                        + " (createdAt = ?3 and id < ?4))",
                                io.quarkus.panache.common.Sort.by("createdAt")
                                        .descending()
                                        .and("id")
                                        .descending(),
                                conversationId,
                                MessageChannel.HISTORY,
                                target.createdAt,
                                target.id)
                        .firstResult();

        MongoConversation fork = new MongoConversation();
        fork.id = UUID.randomUUID().toString();
        fork.ownerUserId = original.ownerUserId;
        fork.title =
                encryptTitle(
                        request.getTitle() != null
                                ? request.getTitle()
                                : decryptTitle(original.title));
        fork.metadata = Collections.emptyMap();
        fork.conversationGroupId = original.conversationGroupId;
        fork.forkedAtConversationId = original.id;
        fork.forkedAtMessageId = previous != null ? previous.id : null;
        Instant now = Instant.now();
        fork.createdAt = now;
        fork.updatedAt = now;
        conversationRepository.persist(fork);

        return toConversationDto(
                fork,
                membershipRepository
                        .findMembership(groupId, userId)
                        .map(m -> m.accessLevel)
                        .orElse(AccessLevel.READER),
                null);
    }

    @Override
    public List<ConversationForkSummaryDto> listForks(String userId, String conversationId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;
        ensureHasAccess(groupId, userId, AccessLevel.READER);

        List<MongoConversation> candidates =
                conversationRepository.find("conversationGroupId", groupId).list();
        List<ConversationForkSummaryDto> results = new ArrayList<>();
        for (MongoConversation candidate : candidates) {
            ConversationForkSummaryDto dto = new ConversationForkSummaryDto();
            dto.setConversationId(candidate.id);
            dto.setConversationGroupId(groupId);
            dto.setForkedAtMessageId(candidate.forkedAtMessageId);
            dto.setForkedAtConversationId(candidate.forkedAtConversationId);
            dto.setTitle(decryptTitle(candidate.title));
            dto.setCreatedAt(formatInstant(candidate.createdAt));
            results.add(dto);
        }
        return results;
    }

    @Override
    @Transactional
    public void requestOwnershipTransfer(
            String userId, String conversationId, String newOwnerUserId) {
        String groupId = resolveGroupId(conversationId);
        MongoConversationMembership membership =
                membershipRepository
                        .findMembership(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        if (membership.accessLevel != AccessLevel.OWNER) {
            throw new AccessDeniedException("Only owner may transfer ownership");
        }
        MongoConversationOwnershipTransfer transfer = new MongoConversationOwnershipTransfer();
        transfer.id = UUID.randomUUID().toString();
        transfer.conversationGroupId = groupId;
        transfer.fromUserId = userId;
        transfer.toUserId = newOwnerUserId;
        transfer.status =
                io.github.chirino.memory.persistence.entity.ConversationOwnershipTransferEntity
                        .TransferStatus.PENDING;
        Instant now = Instant.now();
        transfer.createdAt = now;
        transfer.updatedAt = now;
        ownershipTransferRepository.persist(transfer);
    }

    @Override
    public PagedMessages getMessages(
            String userId,
            String conversationId,
            String afterMessageId,
            int limit,
            MessageChannel channel,
            MemoryEpochFilter epochFilter,
            String clientId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;
        ensureHasAccess(groupId, userId, AccessLevel.READER);
        List<MongoMessage> messages;
        if (channel == MessageChannel.MEMORY) {
            messages =
                    fetchMemoryMessages(
                            conversationId, afterMessageId, limit, epochFilter, clientId);
        } else {
            messages =
                    messageRepository.listByChannel(
                            conversationId, afterMessageId, limit, channel, clientId);
        }
        PagedMessages page = new PagedMessages();
        page.setConversationId(conversationId);
        List<MessageDto> dtos = messages.stream().map(this::toMessageDto).toList();
        page.setMessages(dtos);
        String nextCursor =
                dtos.size() == limit && !dtos.isEmpty() ? dtos.get(dtos.size() - 1).getId() : null;
        page.setNextCursor(nextCursor);
        return page;
    }

    private List<MongoMessage> fetchMemoryMessages(
            String conversationId,
            String afterMessageId,
            int limit,
            MemoryEpochFilter epochFilter,
            String clientId) {
        if (clientId == null || clientId.isBlank()) {
            return Collections.emptyList();
        }
        MemoryEpochFilter filter = epochFilter != null ? epochFilter : MemoryEpochFilter.latest();
        return switch (filter.getMode()) {
            case ALL ->
                    messageRepository.listByChannel(
                            conversationId, afterMessageId, limit, MessageChannel.MEMORY, clientId);
            case LATEST -> {
                Long latestEpoch =
                        messageRepository.findLatestMemoryEpoch(conversationId, clientId);
                // If no messages with epochs exist, list all memory messages
                if (latestEpoch == null) {
                    yield messageRepository.listByChannel(
                            conversationId, afterMessageId, limit, MessageChannel.MEMORY, clientId);
                }
                yield messageRepository.listMemoryMessagesByEpoch(
                        conversationId, afterMessageId, limit, latestEpoch, clientId);
            }
            case EPOCH ->
                    messageRepository.listMemoryMessagesByEpoch(
                            conversationId, afterMessageId, limit, filter.getEpoch(), clientId);
        };
    }

    @Override
    @Transactional
    public List<MessageDto> appendAgentMessages(
            String userId,
            String conversationId,
            List<CreateMessageRequest> messages,
            String clientId) {
        MongoConversation c = conversationRepository.findById(conversationId);

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (c == null) {
            c = new MongoConversation();
            c.id = conversationId;
            c.conversationGroupId = conversationId;
            if (conversationGroupRepository.findById(conversationId) == null) {
                MongoConversationGroup group = new MongoConversationGroup();
                group.id = conversationId;
                group.createdAt = Instant.now();
                conversationGroupRepository.persist(group);
            }
            c.ownerUserId = userId;
            String inferredTitle = inferTitleFromMessages(messages);
            c.title = encryptTitle(inferredTitle);
            c.metadata = Collections.emptyMap();
            Instant now = Instant.now();
            c.createdAt = now;
            c.updatedAt = now;
            conversationRepository.persist(c);
            membershipRepository.createMembership(c.conversationGroupId, userId, AccessLevel.OWNER);
        } else {
            String groupId = c.conversationGroupId;
            ensureHasAccess(groupId, userId, AccessLevel.WRITER);
        }

        Instant latestHistoryTimestamp = null;
        List<MessageDto> created = new ArrayList<>(messages.size());
        for (CreateMessageRequest req : messages) {
            MongoMessage m = new MongoMessage();
            m.id = UUID.randomUUID().toString();
            m.conversationId = conversationId;
            m.userId = req.getUserId();
            m.clientId = clientId;
            m.channel =
                    req.getChannel() != null
                            ? MessageChannel.fromString(req.getChannel().value())
                            : MessageChannel.MEMORY;
            m.memoryEpoch = req.getMemoryEpoch();
            m.decodedContent = req.getContent();
            m.content = encryptContent(m.decodedContent);
            m.conversationGroupId = c.conversationGroupId;
            Instant createdAt = Instant.now();
            m.createdAt = createdAt;
            messageRepository.persist(m);
            if (m.channel == MessageChannel.HISTORY) {
                latestHistoryTimestamp = createdAt;
            }
            created.add(toMessageDto(m));
        }
        if (latestHistoryTimestamp != null) {
            c.updatedAt = latestHistoryTimestamp;
            conversationRepository.persistOrUpdate(c);
        }
        return created;
    }

    @Override
    @Transactional
    public SyncResult syncAgentMessages(
            String userId,
            String conversationId,
            List<CreateMessageRequest> messages,
            String clientId) {
        validateSyncMessages(messages);
        Long latestEpoch = messageRepository.findLatestMemoryEpoch(conversationId, clientId);
        List<MessageDto> latestEpochMessages =
                latestEpoch != null
                        ? messageRepository
                                .listMemoryMessagesByEpoch(conversationId, latestEpoch, clientId)
                                .stream()
                                .map(this::toMessageDto)
                                .collect(Collectors.toList())
                        : Collections.emptyList();

        List<MemorySyncHelper.MessageContent> existing =
                MemorySyncHelper.fromDtos(latestEpochMessages);
        List<MemorySyncHelper.MessageContent> incoming = MemorySyncHelper.fromRequests(messages);

        SyncResult result = new SyncResult();
        result.setMessages(Collections.emptyList());
        result.setMemoryEpoch(latestEpoch);

        // If no existing messages and incoming is empty, that's a no-op (shouldn't happen due to
        // validation)
        if (existing.isEmpty() && incoming.isEmpty()) {
            result.setNoOp(true);
            return result;
        }

        // If existing messages match incoming exactly, it's a no-op
        if (!existing.isEmpty() && existing.equals(incoming)) {
            result.setNoOp(true);
            return result;
        }

        // If incoming is a prefix extension of existing (only adding more messages), append to
        // current epoch
        if (!existing.isEmpty()
                && incoming.size() > existing.size()
                && MemorySyncHelper.isPrefix(existing, incoming)) {
            List<CreateMessageRequest> delta =
                    MemorySyncHelper.withEpoch(
                            messages.subList(existing.size(), messages.size()), latestEpoch);
            List<MessageDto> appended =
                    appendAgentMessages(userId, conversationId, delta, clientId);
            result.setMessages(appended);
            result.setEpochIncremented(false);
            result.setNoOp(false);
            return result;
        }

        // Otherwise, create a new epoch with all incoming messages
        Long nextEpoch = MemorySyncHelper.nextEpoch(latestEpoch);
        List<CreateMessageRequest> toAppend = MemorySyncHelper.withEpoch(messages, nextEpoch);
        List<MessageDto> appended = appendAgentMessages(userId, conversationId, toAppend, clientId);
        result.setMemoryEpoch(nextEpoch);
        result.setMessages(appended);
        result.setEpochIncremented(true);
        result.setNoOp(false);
        return result;
    }

    private void validateSyncMessages(List<CreateMessageRequest> messages) {
        if (messages == null || messages.isEmpty()) {
            throw new IllegalArgumentException("messages are required");
        }
        for (CreateMessageRequest message : messages) {
            if (message == null
                    || message.getChannel() == null
                    || message.getChannel() != CreateMessageRequest.ChannelEnum.MEMORY) {
                throw new IllegalArgumentException("sync messages must target memory channel");
            }
        }
    }

    @Override
    @Transactional
    public MessageDto createSummary(
            String conversationId, CreateSummaryRequest request, String clientId) {
        MongoConversation c = conversationRepository.findById(conversationId);
        if (c == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        MongoMessage summary = new MongoMessage();
        summary.id = UUID.randomUUID().toString();
        summary.conversationId = conversationId;
        summary.userId = null;
        summary.clientId = clientId;
        summary.channel = MessageChannel.SUMMARY;
        summary.memoryEpoch = null;
        summary.decodedContent = buildSummaryContent(request);
        summary.content = encryptContent(summary.decodedContent);
        summary.conversationGroupId = c.conversationGroupId;
        summary.createdAt = Instant.now();
        messageRepository.persist(summary);

        boolean conversationUpdated = false;
        if (request.getTitle() != null && !request.getTitle().isBlank()) {
            c.title = encryptTitle(request.getTitle());
            conversationUpdated = true;
        }
        if (shouldVectorize()) {
            vectorizeSummary(c, summary, request);
        } else if (conversationUpdated) {
            conversationRepository.persistOrUpdate(c);
        }

        return toMessageDto(summary);
    }

    private List<Object> buildSummaryContent(CreateSummaryRequest request) {
        if (request == null) {
            return Collections.emptyList();
        }
        Map<String, Object> block = new HashMap<>();
        block.put("type", "summary");
        block.put("text", request.getSummary());
        if (request.getUntilMessageId() != null) {
            block.put("untilMessageId", request.getUntilMessageId());
        }
        if (request.getSummarizedAt() != null) {
            block.put("summarizedAt", request.getSummarizedAt());
        }
        return List.of(block);
    }

    private boolean shouldVectorize() {
        VectorStore store = vectorStoreSelector.getVectorStore();
        return store != null && store.isEnabled() && embeddingService.isEnabled();
    }

    private void vectorizeSummary(
            MongoConversation conversation, MongoMessage summary, CreateSummaryRequest request) {
        if (request == null || request.getSummary() == null || request.getSummary().isBlank()) {
            return;
        }
        VectorStore store = vectorStoreSelector.getVectorStore();
        try {
            float[] embedding = embeddingService.embed(request.getSummary());
            if (embedding == null || embedding.length == 0) {
                return;
            }
            store.upsertSummaryEmbedding(conversation.id, summary.id, embedding);
            conversation.vectorizedAt = parseInstant(request.getSummarizedAt());
            conversationRepository.persistOrUpdate(conversation);
        } catch (Exception e) {
            LOG.warnf(e, "Failed to vectorize summary for conversationId=%s", conversation.id);
        }
    }

    private Instant parseInstant(String value) {
        if (value == null || value.isBlank()) {
            return null;
        }
        try {
            return Instant.from(ISO_FORMATTER.parse(value));
        } catch (Exception e) {
            return null;
        }
    }

    @Override
    public List<SearchResultDto> searchMessages(String userId, SearchMessagesRequest request) {
        if (request.getQuery() == null || request.getQuery().isBlank()) {
            return Collections.emptyList();
        }

        Set<String> groupIds =
                membershipRepository.list("userId", userId).stream()
                        .map(m -> m.conversationGroupId)
                        .collect(Collectors.toSet());
        if (groupIds.isEmpty()) {
            return Collections.emptyList();
        }

        List<MongoConversation> conversations =
                conversationRepository.find("conversationGroupId in ?1", groupIds).list();
        Set<String> userConversationIds =
                conversations.stream().map(c -> c.id).collect(Collectors.toSet());

        List<String> targetConversationIds;
        if (request.getConversationIds() != null && !request.getConversationIds().isEmpty()) {
            targetConversationIds =
                    request.getConversationIds().stream()
                            .filter(userConversationIds::contains)
                            .collect(Collectors.toList());
            if (targetConversationIds.isEmpty()) {
                return Collections.emptyList();
            }
        } else {
            targetConversationIds = new ArrayList<>(userConversationIds);
        }

        String query = request.getQuery().toLowerCase();
        int limit = request.getTopK() != null ? request.getTopK() : 20;

        List<MongoMessage> candidates =
                messageRepository.find("conversationId in ?1", targetConversationIds).list();

        return candidates.stream()
                .map(
                        m -> {
                            List<Object> content = decryptContent(m.content);
                            if (content == null || content.isEmpty()) {
                                return null;
                            }
                            String text = extractSearchText(content);
                            if (text == null || !text.toLowerCase().contains(query)) {
                                return null;
                            }
                            SearchResultDto dto = new SearchResultDto();
                            dto.setMessage(toMessageDto(m, content));
                            dto.setScore(1.0);
                            dto.setHighlights(null);
                            return dto;
                        })
                .filter(r -> r != null)
                .limit(limit)
                .collect(Collectors.toList());
    }

    private void ensureHasAccess(String conversationId, String userId, AccessLevel level) {
        if (!membershipRepository.hasAtLeastAccess(conversationId, userId, level)) {
            throw new AccessDeniedException(
                    "User "
                            + userId
                            + " does not have "
                            + level
                            + " access to conversation "
                            + conversationId);
        }
    }

    private ConversationSummaryDto toConversationSummaryDto(
            MongoConversation entity, AccessLevel accessLevel, String lastMessagePreview) {
        ConversationSummaryDto dto = new ConversationSummaryDto();
        dto.setId(entity.id);
        dto.setTitle(decryptTitle(entity.title));
        dto.setOwnerUserId(entity.ownerUserId);
        dto.setCreatedAt(formatInstant(entity.createdAt));
        dto.setUpdatedAt(formatInstant(entity.updatedAt));
        dto.setLastMessagePreview(lastMessagePreview);
        dto.setAccessLevel(accessLevel);
        return dto;
    }

    private ConversationDto toConversationDto(
            MongoConversation entity, AccessLevel accessLevel, String lastMessagePreview) {
        ConversationDto dto = new ConversationDto();
        dto.setId(entity.id);
        dto.setTitle(decryptTitle(entity.title));
        dto.setOwnerUserId(entity.ownerUserId);
        dto.setCreatedAt(formatInstant(entity.createdAt));
        dto.setUpdatedAt(formatInstant(entity.updatedAt));
        dto.setLastMessagePreview(lastMessagePreview);
        dto.setAccessLevel(accessLevel);
        dto.setConversationGroupId(entity.conversationGroupId);
        dto.setForkedAtMessageId(entity.forkedAtMessageId);
        dto.setForkedAtConversationId(entity.forkedAtConversationId);
        return dto;
    }

    private ConversationMembershipDto toMembershipDto(MongoConversationMembership entity) {
        ConversationMembershipDto dto = new ConversationMembershipDto();
        dto.setConversationGroupId(entity.conversationGroupId);
        dto.setUserId(entity.userId);
        dto.setAccessLevel(entity.accessLevel);
        dto.setCreatedAt(formatInstant(entity.createdAt));
        return dto;
    }

    private MessageDto toMessageDto(MongoMessage entity) {
        List<Object> content =
                entity.decodedContent != null
                        ? entity.decodedContent
                        : decryptContent(entity.content);
        return toMessageDto(entity, content);
    }

    private MessageDto toMessageDto(MongoMessage entity, List<Object> content) {
        MessageDto dto = new MessageDto();
        dto.setId(entity.id);
        dto.setConversationId(entity.conversationId);
        dto.setUserId(entity.userId);
        dto.setChannel(entity.channel);
        dto.setMemoryEpoch(entity.memoryEpoch);
        dto.setContent(content != null ? content : Collections.emptyList());
        dto.setCreatedAt(formatInstant(entity.createdAt));
        return dto;
    }

    private byte[] encryptContent(List<Object> content) {
        if (content == null) {
            return null;
        }
        try {
            String json = objectMapper.writeValueAsString(content);
            return dataEncryptionService.encrypt(json.getBytes(StandardCharsets.UTF_8));
        } catch (Exception e) {
            throw new RuntimeException("Failed to serialize message content", e);
        }
    }

    @SuppressWarnings("unchecked")
    private List<Object> decryptContent(byte[] content) {
        if (content == null) {
            return null;
        }
        byte[] plain = dataEncryptionService.decrypt(content);
        try {
            return objectMapper.readValue(plain, List.class);
        } catch (Exception e) {
            throw new RuntimeException("Failed to deserialize message content", e);
        }
    }

    private String extractSearchText(List<Object> content) {
        for (Object block : content) {
            if (block == null) {
                continue;
            }
            if (block instanceof Map<?, ?> map) {
                Object text = map.get("text");
                if (text instanceof String s && !s.isBlank()) {
                    return s;
                }
            } else if (block instanceof String s && !s.isBlank()) {
                return s;
            }
        }
        return null;
    }

    private String inferTitleFromUserMessage(CreateUserMessageRequest request) {
        if (request == null) {
            return null;
        }
        String rawContent = request.getContent();
        if (rawContent == null || rawContent.isBlank()) {
            return null;
        }
        String extractedText = tryExtractTextFromJson(rawContent);
        return abbreviateTitle(extractedText);
    }

    private String inferTitleFromMessages(List<CreateMessageRequest> messages) {
        if (messages == null || messages.isEmpty()) {
            return null;
        }
        for (CreateMessageRequest message : messages) {
            if (message == null) {
                continue;
            }
            String text = extractSearchText(message.getContent());
            if (text != null && !text.isBlank()) {
                return abbreviateTitle(text);
            }
        }
        return null;
    }

    @SuppressWarnings("unchecked")
    private String tryExtractTextFromJson(String json) {
        if (json == null || json.isBlank()) {
            return null;
        }
        try {
            List<Object> parsed = objectMapper.readValue(json, List.class);
            String text = extractSearchText(parsed);
            if (text != null && !text.isBlank()) {
                return text;
            }
        } catch (Exception ignored) {
            return json;
        }
        return json;
    }

    private String abbreviateTitle(String text) {
        if (text == null) {
            return null;
        }
        String normalized = text.strip().replaceAll("\\s+", " ");
        if (normalized.isBlank()) {
            return null;
        }
        return normalized.length() <= 40 ? normalized : normalized.substring(0, 40);
    }

    private String resolveGroupId(String conversationId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        return conversation.conversationGroupId;
    }

    private byte[] encryptTitle(String title) {
        if (title == null || title.isBlank()) {
            return null;
        }
        byte[] plain = title.getBytes(StandardCharsets.UTF_8);
        return dataEncryptionService.encrypt(plain);
    }

    private String decryptTitle(byte[] encryptedTitle) {
        if (encryptedTitle == null) {
            return null;
        }
        byte[] plain = dataEncryptionService.decrypt(encryptedTitle);
        return new String(plain, StandardCharsets.UTF_8);
    }

    private String formatInstant(Instant instant) {
        return ISO_FORMATTER.format(instant.atOffset(ZoneOffset.UTC));
    }
}
