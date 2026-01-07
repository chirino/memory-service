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
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.config.VectorStoreSelector;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.model.MessageChannel;
import io.github.chirino.memory.persistence.entity.ConversationEntity;
import io.github.chirino.memory.persistence.entity.ConversationGroupEntity;
import io.github.chirino.memory.persistence.entity.ConversationMembershipEntity;
import io.github.chirino.memory.persistence.entity.ConversationOwnershipTransferEntity;
import io.github.chirino.memory.persistence.entity.MessageEntity;
import io.github.chirino.memory.persistence.repo.ConversationGroupRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.ConversationOwnershipTransferRepository;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.MessageRepository;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.github.chirino.memory.vector.EmbeddingService;
import io.github.chirino.memory.vector.VectorStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import java.nio.charset.StandardCharsets;
import java.time.OffsetDateTime;
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
public class PostgresMemoryStore implements MemoryStore {

    private static final Logger LOG = Logger.getLogger(PostgresMemoryStore.class);
    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @Inject ConversationRepository conversationRepository;

    @Inject ConversationGroupRepository conversationGroupRepository;

    @Inject ConversationMembershipRepository membershipRepository;

    @Inject MessageRepository messageRepository;

    @Inject ConversationOwnershipTransferRepository ownershipTransferRepository;

    @Inject DataEncryptionService dataEncryptionService;

    @Inject ObjectMapper objectMapper;

    @Inject VectorStoreSelector vectorStoreSelector;

    @Inject EmbeddingService embeddingService;

    @Override
    @Transactional
    public ConversationDto createConversation(String userId, CreateConversationRequest request) {
        UUID conversationId = UUID.randomUUID();
        ConversationGroupEntity conversationGroup = new ConversationGroupEntity();
        conversationGroup.setId(conversationId);
        conversationGroupRepository.persist(conversationGroup);

        ConversationEntity entity = new ConversationEntity();
        entity.setId(conversationId);
        entity.setOwnerUserId(userId);
        entity.setTitle(encryptTitle(request.getTitle()));
        entity.setMetadata(
                request.getMetadata() != null ? request.getMetadata() : Collections.emptyMap());
        entity.setConversationGroup(conversationGroup);
        conversationRepository.persist(entity);

        membershipRepository.createMembership(conversationGroup, userId, AccessLevel.OWNER);

        return toConversationDto(entity, AccessLevel.OWNER, null);
    }

    @Override
    public List<ConversationSummaryDto> listConversations(
            String userId, String query, String after, int limit, ConversationListMode mode) {
        // For now, list by memberships and ignore cursor/query for simplicity.
        List<ConversationMembershipEntity> memberships =
                membershipRepository.listForUser(userId, limit);
        if (mode == ConversationListMode.ROOTS) {
            Map<UUID, AccessLevel> accessByGroup =
                    memberships.stream()
                            .collect(
                                    Collectors.toMap(
                                            m -> m.getId().getConversationGroupId(),
                                            ConversationMembershipEntity::getAccessLevel,
                                            (left, right) -> left));
            if (accessByGroup.isEmpty()) {
                return List.of();
            }
            List<ConversationEntity> roots =
                    conversationRepository
                            .find(
                                    "conversationGroup.id in ?1 and forkedAtMessageId is null and"
                                            + " forkedAtConversationId is null",
                                    accessByGroup.keySet())
                            .list();
            return roots.stream()
                    .map(
                            entity ->
                                    toConversationSummaryDto(
                                            entity,
                                            accessByGroup.get(
                                                    entity.getConversationGroup().getId()),
                                            null))
                    .sorted(Comparator.comparing(ConversationSummaryDto::getUpdatedAt).reversed())
                    .limit(limit)
                    .collect(Collectors.toList());
        }

        Map<UUID, AccessLevel> accessByGroup =
                memberships.stream()
                        .collect(
                                Collectors.toMap(
                                        m -> m.getId().getConversationGroupId(),
                                        ConversationMembershipEntity::getAccessLevel));
        if (accessByGroup.isEmpty()) {
            return List.of();
        }
        Set<UUID> groupIds = accessByGroup.keySet();
        List<ConversationEntity> candidates =
                conversationRepository.find("conversationGroup.id in ?1", groupIds).list();

        if (mode == ConversationListMode.LATEST_FORK) {
            Map<UUID, ConversationEntity> latestByGroup = new HashMap<>();
            for (ConversationEntity candidate : candidates) {
                UUID groupId = candidate.getConversationGroup().getId();
                if (!accessByGroup.containsKey(groupId)) {
                    continue;
                }
                ConversationEntity current = latestByGroup.get(groupId);
                if (current == null || candidate.getUpdatedAt().isAfter(current.getUpdatedAt())) {
                    latestByGroup.put(groupId, candidate);
                }
            }
            return latestByGroup.values().stream()
                    .map(
                            entity ->
                                    toConversationSummaryDto(
                                            entity,
                                            accessByGroup.get(
                                                    entity.getConversationGroup().getId()),
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
                                        accessByGroup.get(entity.getConversationGroup().getId()),
                                        null))
                .filter(dto -> dto.getAccessLevel() != null)
                .sorted(Comparator.comparing(ConversationSummaryDto::getUpdatedAt).reversed())
                .limit(limit)
                .collect(Collectors.toList());
    }

    @Override
    public ConversationDto getConversation(String userId, String conversationId) {
        UUID id = UUID.fromString(conversationId);
        ConversationEntity entity =
                conversationRepository
                        .findByIdOptional(id)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        UUID groupId = entity.getConversationGroup().getId();
        ConversationMembershipEntity membership =
                membershipRepository
                        .findMembership(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        return toConversationDto(entity, membership.getAccessLevel(), null);
    }

    @Override
    @Transactional
    public void deleteConversation(String userId, String conversationId) {
        UUID id = UUID.fromString(conversationId);
        // Check if conversation exists first to avoid Hibernate transient entity issues
        ConversationEntity conversation =
                conversationRepository
                        .findByIdOptional(id)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        UUID groupId = conversation.getConversationGroup().getId();
        // Use a projection query to get only the access level without loading the conversation
        // relationship to avoid Hibernate transient entity issues
        AccessLevel accessLevel =
                membershipRepository
                        .findAccessLevel(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        if (accessLevel != AccessLevel.OWNER && accessLevel != AccessLevel.MANAGER) {
            throw new AccessDeniedException("Only owner or manager can delete conversation");
        }
        deleteConversationGroupData(groupId);
    }

    @Override
    @Transactional
    public MessageDto appendUserMessage(
            String userId, String conversationId, CreateUserMessageRequest request) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (conversation == null) {
            conversation = new ConversationEntity();
            conversation.setId(cid);
            conversation.setOwnerUserId(userId);
            String inferredTitle = inferTitleFromUserMessage(request);
            conversation.setTitle(encryptTitle(inferredTitle));
            conversation.setMetadata(Collections.emptyMap());
            ConversationGroupEntity conversationGroup =
                    conversationGroupRepository
                            .findByIdOptional(cid)
                            .orElseGet(
                                    () -> {
                                        ConversationGroupEntity group =
                                                new ConversationGroupEntity();
                                        group.setId(cid);
                                        conversationGroupRepository.persist(group);
                                        return group;
                                    });
            conversation.setConversationGroup(conversationGroup);
            conversationRepository.persist(conversation);
            membershipRepository.createMembership(conversationGroup, userId, AccessLevel.OWNER);
        } else {
            UUID groupId = conversation.getConversationGroup().getId();
            ensureHasAccess(groupId, userId, AccessLevel.WRITER);
        }

        MessageEntity message = new MessageEntity();
        message.setConversation(conversation);
        message.setUserId(userId);
        message.setChannel(io.github.chirino.memory.model.MessageChannel.HISTORY);
        message.setMemoryEpoch(null);
        message.setContent(encryptContent(toContentBlocksFromUser(request.getContent())));
        message.setConversationGroupId(conversation.getConversationGroup().getId());
        OffsetDateTime createdAt = OffsetDateTime.now();
        message.setCreatedAt(createdAt);
        messageRepository.persist(message);
        conversationRepository.update(
                "updatedAt = ?1 where id = ?2", createdAt, conversation.getId());
        return toMessageDto(message);
    }

    @Override
    public List<ConversationMembershipDto> listMemberships(String userId, String conversationId) {
        UUID cid = UUID.fromString(conversationId);
        UUID groupId = resolveGroupId(cid);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);
        return membershipRepository.listForConversationGroup(groupId).stream()
                .map(this::toMembershipDto)
                .collect(Collectors.toList());
    }

    @Override
    @Transactional
    public ConversationMembershipDto shareConversation(
            String userId, String conversationId, ShareConversationRequest request) {
        UUID cid = UUID.fromString(conversationId);
        UUID groupId = resolveGroupId(cid);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);
        ConversationEntity conversation =
                conversationRepository
                        .findByIdOptional(cid)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        ConversationGroupEntity conversationGroup = conversation.getConversationGroup();
        ConversationMembershipEntity membership =
                membershipRepository.createMembership(
                        conversationGroup, request.getUserId(), request.getAccessLevel());
        return toMembershipDto(membership);
    }

    @Override
    @Transactional
    public ConversationMembershipDto updateMembership(
            String userId,
            String conversationId,
            String memberUserId,
            ShareConversationRequest request) {
        UUID cid = UUID.fromString(conversationId);
        UUID groupId = resolveGroupId(cid);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);
        ConversationMembershipEntity membership =
                membershipRepository
                        .findMembership(groupId, memberUserId)
                        .orElseThrow(
                                () -> new ResourceNotFoundException("membership", memberUserId));
        if (request.getAccessLevel() != null) {
            membership.setAccessLevel(request.getAccessLevel());
        }
        return toMembershipDto(membership);
    }

    @Override
    @Transactional
    public void deleteMembership(String userId, String conversationId, String memberUserId) {
        UUID cid = UUID.fromString(conversationId);
        UUID groupId = resolveGroupId(cid);
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
        // Create a new fork conversation without copying messages.
        UUID originalId = UUID.fromString(conversationId);
        ConversationEntity originalEntity =
                conversationRepository
                        .findByIdOptional(originalId)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        UUID groupId = originalEntity.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.WRITER);

        MessageEntity target =
                messageRepository
                        .findByIdOptional(UUID.fromString(messageId))
                        .orElseThrow(() -> new ResourceNotFoundException("message", messageId));
        if (target.getConversation() == null
                || !originalId.equals(target.getConversation().getId())) {
            throw new ResourceNotFoundException("message", messageId);
        }
        if (target.getChannel() != MessageChannel.HISTORY) {
            throw new AccessDeniedException("Forking is only allowed for history messages");
        }

        MessageEntity previous =
                messageRepository
                        .find(
                                "from MessageEntity m where m.conversation.id = ?1 and m.channel ="
                                    + " ?2 and (m.createdAt < ?3 or (m.createdAt = ?3 and m.id <"
                                    + " ?4)) order by m.createdAt desc, m.id desc",
                                originalId,
                                MessageChannel.HISTORY,
                                target.getCreatedAt(),
                                target.getId())
                        .firstResult();
        UUID previousId = previous != null ? previous.getId() : null;

        ConversationEntity forkEntity = new ConversationEntity();
        forkEntity.setOwnerUserId(originalEntity.getOwnerUserId());
        forkEntity.setTitle(
                encryptTitle(
                        request.getTitle() != null
                                ? request.getTitle()
                                : decryptTitle(originalEntity.getTitle())));
        forkEntity.setMetadata(Collections.emptyMap());
        forkEntity.setConversationGroup(originalEntity.getConversationGroup());
        forkEntity.setForkedAtConversationId(originalEntity.getId());
        forkEntity.setForkedAtMessageId(previousId);
        conversationRepository.persist(forkEntity);

        return toConversationDto(
                forkEntity,
                membershipRepository
                        .findMembership(groupId, userId)
                        .map(ConversationMembershipEntity::getAccessLevel)
                        .orElse(AccessLevel.READER),
                null);
    }

    @Override
    public List<ConversationForkSummaryDto> listForks(String userId, String conversationId) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation =
                conversationRepository
                        .findByIdOptional(cid)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        UUID groupId = conversation.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.READER);

        List<ConversationEntity> candidates =
                conversationRepository.find("conversationGroup.id", groupId).list();
        List<ConversationForkSummaryDto> results = new ArrayList<>();
        for (ConversationEntity candidate : candidates) {
            ConversationForkSummaryDto dto = new ConversationForkSummaryDto();
            dto.setConversationId(candidate.getId().toString());
            dto.setConversationGroupId(groupId.toString());
            dto.setForkedAtMessageId(
                    candidate.getForkedAtMessageId() != null
                            ? candidate.getForkedAtMessageId().toString()
                            : null);
            dto.setForkedAtConversationId(
                    candidate.getForkedAtConversationId() != null
                            ? candidate.getForkedAtConversationId().toString()
                            : null);
            dto.setTitle(decryptTitle(candidate.getTitle()));
            dto.setCreatedAt(ISO_FORMATTER.format(candidate.getCreatedAt()));
            results.add(dto);
        }
        return results;
    }

    @Override
    @Transactional
    public void requestOwnershipTransfer(
            String userId, String conversationId, String newOwnerUserId) {
        UUID cid = UUID.fromString(conversationId);
        UUID groupId = resolveGroupId(cid);
        ConversationMembershipEntity membership =
                membershipRepository
                        .findMembership(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        if (membership.getAccessLevel() != AccessLevel.OWNER) {
            throw new AccessDeniedException("Only owner may transfer ownership");
        }
        ConversationOwnershipTransferEntity transfer = new ConversationOwnershipTransferEntity();
        transfer.setConversationGroup(membership.getConversationGroup());
        transfer.setFromUserId(userId);
        transfer.setToUserId(newOwnerUserId);
        ownershipTransferRepository.persist(transfer);
    }

    @Override
    public PagedMessages getMessages(
            String userId,
            String conversationId,
            String afterMessageId,
            int limit,
            MessageChannel channel) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        if (conversation == null) {
            PagedMessages empty = new PagedMessages();
            empty.setConversationId(conversationId);
            empty.setMessages(Collections.emptyList());
            empty.setNextCursor(null);
            return empty;
        }
        UUID groupId = conversation.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.READER);
        List<MessageEntity> messages =
                messageRepository.listByChannel(cid, afterMessageId, limit, channel);
        PagedMessages page = new PagedMessages();
        page.setConversationId(conversationId);
        List<MessageDto> dtos = messages.stream().map(this::toMessageDto).toList();
        page.setMessages(dtos);
        String nextCursor =
                dtos.size() == limit && !dtos.isEmpty() ? dtos.get(dtos.size() - 1).getId() : null;
        page.setNextCursor(nextCursor);
        return page;
    }

    @Override
    @Transactional
    public List<MessageDto> appendAgentMessages(
            String userId, String conversationId, List<CreateMessageRequest> messages) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (conversation == null) {
            conversation = new ConversationEntity();
            conversation.setId(cid);
            conversation.setOwnerUserId(userId);
            String inferredTitle = inferTitleFromMessages(messages);
            conversation.setTitle(encryptTitle(inferredTitle));
            conversation.setMetadata(Collections.emptyMap());
            ConversationGroupEntity conversationGroup =
                    conversationGroupRepository
                            .findByIdOptional(cid)
                            .orElseGet(
                                    () -> {
                                        ConversationGroupEntity group =
                                                new ConversationGroupEntity();
                                        group.setId(cid);
                                        conversationGroupRepository.persist(group);
                                        return group;
                                    });
            conversation.setConversationGroup(conversationGroup);
            conversationRepository.persist(conversation);
            membershipRepository.createMembership(conversationGroup, userId, AccessLevel.OWNER);
        } else {
            UUID groupId = conversation.getConversationGroup().getId();
            ensureHasAccess(groupId, userId, AccessLevel.WRITER);
        }

        // Make conversation effectively final for lambda
        OffsetDateTime latestHistoryTimestamp = null;
        List<MessageDto> created = new ArrayList<>(messages.size());
        for (CreateMessageRequest req : messages) {
            MessageEntity entity = new MessageEntity();
            entity.setConversation(conversation);
            entity.setUserId(req.getUserId());
            if (req.getChannel() != null) {
                entity.setChannel(
                        io.github.chirino.memory.model.MessageChannel.fromString(
                                req.getChannel().value()));
            } else {
                entity.setChannel(io.github.chirino.memory.model.MessageChannel.MEMORY);
            }
            entity.setMemoryEpoch(req.getMemoryEpoch());
            entity.setContent(encryptContent(req.getContent()));
            entity.setConversationGroupId(conversation.getConversationGroup().getId());
            OffsetDateTime createdAt = OffsetDateTime.now();
            entity.setCreatedAt(createdAt);
            messageRepository.persist(entity);
            if (entity.getChannel() == MessageChannel.HISTORY) {
                latestHistoryTimestamp = createdAt;
            }
            created.add(toMessageDto(entity));
        }
        if (latestHistoryTimestamp != null) {
            conversationRepository.update(
                    "updatedAt = ?1 where id = ?2", latestHistoryTimestamp, conversation.getId());
        }
        return created;
    }

    @Override
    @Transactional
    public MessageDto createSummary(String conversationId, CreateSummaryRequest request) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation =
                conversationRepository
                        .findByIdOptional(cid)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));

        MessageEntity summaryMessage = new MessageEntity();
        summaryMessage.setConversation(conversation);
        summaryMessage.setUserId(null);
        summaryMessage.setChannel(io.github.chirino.memory.model.MessageChannel.SUMMARY);
        summaryMessage.setMemoryEpoch(null);
        summaryMessage.setContent(encryptContentFromSummary(request));
        summaryMessage.setConversationGroupId(conversation.getConversationGroup().getId());
        messageRepository.persist(summaryMessage);

        if (request.getTitle() != null && !request.getTitle().isBlank()) {
            conversation.setTitle(encryptTitle(request.getTitle()));
        }

        if (shouldVectorize()) {
            vectorizeSummary(conversation, summaryMessage, request);
        }

        return toMessageDto(summaryMessage);
    }

    @Override
    public List<SearchResultDto> searchMessages(String userId, SearchMessagesRequest request) {
        if (request.getQuery() == null || request.getQuery().isBlank()) {
            return Collections.emptyList();
        }

        List<ConversationMembershipEntity> memberships =
                membershipRepository.listForUser(userId, Integer.MAX_VALUE);
        if (memberships.isEmpty()) {
            return Collections.emptyList();
        }

        Set<UUID> groupIds =
                memberships.stream()
                        .map(m -> m.getId().getConversationGroupId())
                        .collect(Collectors.toSet());
        if (groupIds.isEmpty()) {
            return Collections.emptyList();
        }
        List<ConversationEntity> conversations =
                conversationRepository.find("conversationGroup.id in ?1", groupIds).list();
        Set<UUID> userConversationIds =
                conversations.stream().map(ConversationEntity::getId).collect(Collectors.toSet());

        List<UUID> targetConversationIds;
        if (request.getConversationIds() != null && !request.getConversationIds().isEmpty()) {
            List<UUID> requested =
                    request.getConversationIds().stream()
                            .map(UUID::fromString)
                            .collect(Collectors.toList());
            targetConversationIds =
                    requested.stream()
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

        List<MessageEntity> candidates =
                messageRepository.find("conversation.id in ?1", targetConversationIds).list();

        return candidates.stream()
                .map(
                        m -> {
                            List<Object> content = decryptContent(m.getContent());
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

    private byte[] encryptContentFromSummary(CreateSummaryRequest request) {
        if (request == null || request.getSummary() == null) {
            return null;
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
        return encryptContent(Collections.singletonList(block));
    }

    private boolean shouldVectorize() {
        VectorStore store = vectorStoreSelector.getVectorStore();
        return store != null && store.isEnabled() && embeddingService.isEnabled();
    }

    private void vectorizeSummary(
            ConversationEntity conversation,
            MessageEntity summaryMessage,
            CreateSummaryRequest request) {
        if (request == null || request.getSummary() == null || request.getSummary().isBlank()) {
            return;
        }
        VectorStore store = vectorStoreSelector.getVectorStore();
        try {
            float[] embedding = embeddingService.embed(request.getSummary());
            if (embedding == null || embedding.length == 0) {
                return;
            }
            store.upsertSummaryEmbedding(
                    conversation.getId().toString(), summaryMessage.getId().toString(), embedding);
            conversation.setVectorizedAt(parseOffsetDateTime(request.getSummarizedAt()));
            conversationRepository.persist(conversation);
        } catch (Exception e) {
            LOG.warnf(e, "Failed to vectorize summary for conversationId=%s", conversation.getId());
        }
    }

    private OffsetDateTime parseOffsetDateTime(String value) {
        if (value == null || value.isBlank()) {
            return null;
        }
        try {
            return OffsetDateTime.parse(value, ISO_FORMATTER);
        } catch (Exception e) {
            return null;
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

    private SearchResultDto toSearchResult(MessageEntity entity, List<Object> content) {
        SearchResultDto dto = new SearchResultDto();
        dto.setMessage(toMessageDto(entity, content));
        dto.setScore(1.0);
        dto.setHighlights(null);
        return dto;
    }

    private void ensureHasAccess(UUID conversationId, String userId, AccessLevel level) {
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

    private void deleteConversationGroupData(UUID conversationGroupId) {
        // Delete summaries before messages to avoid FK violations on until_message_id.
        messageRepository.delete("conversationGroupId", conversationGroupId);
        conversationRepository.delete("conversationGroup.id", conversationGroupId);
        membershipRepository.delete("id.conversationGroupId", conversationGroupId);
        ownershipTransferRepository.delete("conversationGroup.id", conversationGroupId);
        conversationGroupRepository.delete("id", conversationGroupId);
    }

    private UUID resolveGroupId(UUID conversationId) {
        ConversationEntity conversation =
                conversationRepository
                        .findByIdOptional(conversationId)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId.toString()));
        return conversation.getConversationGroup().getId();
    }

    private ConversationSummaryDto toConversationSummaryDto(
            ConversationEntity entity, AccessLevel accessLevel, String lastMessagePreview) {
        ConversationSummaryDto dto = new ConversationSummaryDto();
        dto.setId(entity.getId().toString());
        dto.setTitle(decryptTitle(entity.getTitle()));
        dto.setOwnerUserId(entity.getOwnerUserId());
        dto.setCreatedAt(ISO_FORMATTER.format(entity.getCreatedAt()));
        dto.setUpdatedAt(ISO_FORMATTER.format(entity.getUpdatedAt()));
        dto.setLastMessagePreview(lastMessagePreview);
        dto.setAccessLevel(accessLevel);
        return dto;
    }

    private ConversationDto toConversationDto(
            ConversationEntity entity, AccessLevel accessLevel, String lastMessagePreview) {
        ConversationDto dto = new ConversationDto();
        dto.setId(entity.getId().toString());
        dto.setTitle(decryptTitle(entity.getTitle()));
        dto.setOwnerUserId(entity.getOwnerUserId());
        dto.setCreatedAt(ISO_FORMATTER.format(entity.getCreatedAt()));
        dto.setUpdatedAt(ISO_FORMATTER.format(entity.getUpdatedAt()));
        dto.setLastMessagePreview(lastMessagePreview);
        dto.setAccessLevel(accessLevel);
        dto.setConversationGroupId(entity.getConversationGroup().getId().toString());
        dto.setForkedAtMessageId(
                entity.getForkedAtMessageId() != null
                        ? entity.getForkedAtMessageId().toString()
                        : null);
        dto.setForkedAtConversationId(
                entity.getForkedAtConversationId() != null
                        ? entity.getForkedAtConversationId().toString()
                        : null);
        return dto;
    }

    private ConversationMembershipDto toMembershipDto(ConversationMembershipEntity entity) {
        ConversationMembershipDto dto = new ConversationMembershipDto();
        dto.setConversationGroupId(entity.getConversationGroup().getId().toString());
        dto.setUserId(entity.getId().getUserId());
        dto.setAccessLevel(entity.getAccessLevel());
        dto.setCreatedAt(ISO_FORMATTER.format(entity.getCreatedAt()));
        return dto;
    }

    private MessageDto toMessageDto(MessageEntity entity) {
        return toMessageDto(entity, decryptContent(entity.getContent()));
    }

    private MessageDto toMessageDto(MessageEntity entity, List<Object> content) {
        MessageDto dto = new MessageDto();
        dto.setId(entity.getId().toString());
        dto.setConversationId(entity.getConversation().getId().toString());
        dto.setUserId(entity.getUserId());
        dto.setChannel(entity.getChannel());
        dto.setMemoryEpoch(entity.getMemoryEpoch());
        dto.setContent(content != null ? content : Collections.emptyList());
        dto.setCreatedAt(ISO_FORMATTER.format(entity.getCreatedAt()));
        return dto;
    }

    private List<Object> toContentBlocksFromUser(String text) {
        if (text == null) {
            return Collections.emptyList();
        }
        return List.of(Map.of("type", "text", "text", text));
    }
}
