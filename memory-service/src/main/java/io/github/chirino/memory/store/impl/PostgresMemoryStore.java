package io.github.chirino.memory.store.impl;

import com.fasterxml.jackson.databind.ObjectMapper;
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
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.api.dto.ShareConversationRequest;
import io.github.chirino.memory.api.dto.SyncResult;
import io.github.chirino.memory.api.dto.UnindexedEntriesResponse;
import io.github.chirino.memory.api.dto.UnindexedEntry;
import io.github.chirino.memory.cache.CachedMemoryEntries;
import io.github.chirino.memory.cache.MemoryEntriesCache;
import io.github.chirino.memory.cache.MemoryEntriesCacheSelector;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.config.SearchStoreSelector;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.model.AdminConversationQuery;
import io.github.chirino.memory.model.AdminMessageQuery;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.model.Channel;
import io.github.chirino.memory.persistence.entity.ConversationEntity;
import io.github.chirino.memory.persistence.entity.ConversationGroupEntity;
import io.github.chirino.memory.persistence.entity.ConversationMembershipEntity;
import io.github.chirino.memory.persistence.entity.ConversationOwnershipTransferEntity;
import io.github.chirino.memory.persistence.entity.EntryEntity;
import io.github.chirino.memory.persistence.repo.ConversationGroupRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.ConversationOwnershipTransferRepository;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.EntryRepository;
import io.github.chirino.memory.persistence.repo.TaskRepository;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.MemoryEpochFilter;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceConflictException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.github.chirino.memory.vector.EmbeddingService;
import io.github.chirino.memory.vector.EntryVectorizationEvent;
import io.github.chirino.memory.vector.EntryVectorizationEvent.EntryToVectorize;
import io.github.chirino.memory.vector.VectorSearchStore;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Event;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
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
import java.util.Objects;
import java.util.Optional;
import java.util.Set;
import java.util.UUID;
import java.util.stream.Collectors;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import org.jboss.logging.Logger;

@ApplicationScoped
public class PostgresMemoryStore implements MemoryStore {

    private static final Logger LOG = Logger.getLogger(PostgresMemoryStore.class);
    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @Inject Event<EntryVectorizationEvent> vectorizationEvent;

    @Inject ConversationRepository conversationRepository;

    @Inject ConversationGroupRepository conversationGroupRepository;

    @Inject ConversationMembershipRepository membershipRepository;

    @Inject EntryRepository entryRepository;

    @Inject ConversationOwnershipTransferRepository ownershipTransferRepository;

    @Inject DataEncryptionService dataEncryptionService;

    @Inject ObjectMapper objectMapper;

    @Inject SearchStoreSelector searchStoreSelector;

    @Inject EmbeddingService embeddingService;

    @Inject EntityManager entityManager;

    @Inject TaskRepository taskRepository;

    @Inject MemoryEntriesCacheSelector memoryCacheSelector;

    @Inject io.github.chirino.memory.attachment.AttachmentStoreSelector attachmentStoreSelector;

    @Inject io.github.chirino.memory.attachment.FileStoreSelector fileStoreSelector;

    @Inject io.github.chirino.memory.attachment.AttachmentDeletionService attachmentDeletionService;

    @Inject io.github.chirino.memory.security.MembershipAuditLogger membershipAuditLogger;

    private MemoryEntriesCache memoryEntriesCache;

    /**
     * Represents a conversation in the fork ancestry chain. The forkedAtEntryId indicates the entry
     * in THIS conversation where we should stop including entries (fork point). For the root
     * conversation, forkedAtEntryId is null, meaning include all entries.
     */
    private record ForkAncestor(UUID conversationId, UUID forkedAtEntryId) {}

    @PostConstruct
    void init() {
        memoryEntriesCache = memoryCacheSelector.select();
    }

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
            String userId, String query, String afterCursor, int limit, ConversationListMode mode) {
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
                                    "conversationGroup.id in ?1 and forkedAtEntryId is null and"
                                        + " forkedAtConversationId is null and deletedAt IS NULL"
                                        + " and conversationGroup.deletedAt IS NULL",
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
                conversationRepository
                        .find(
                                "conversationGroup.id in ?1 and deletedAt IS NULL and"
                                        + " conversationGroup.deletedAt IS NULL",
                                groupIds)
                        .list();

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
                        .findActiveById(id)
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
    public ConversationDto updateConversation(String userId, String conversationId, String title) {
        UUID id = UUID.fromString(conversationId);
        ConversationEntity conversation =
                conversationRepository
                        .findActiveById(id)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        UUID groupId = conversation.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.WRITER);
        conversation.setTitle(encryptTitle(title));
        ConversationMembershipEntity membership =
                membershipRepository
                        .findMembership(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        return toConversationDto(conversation, membership.getAccessLevel(), null);
    }

    @Override
    @Transactional
    public void deleteConversation(String userId, String conversationId) {
        UUID id = UUID.fromString(conversationId);
        // Check if conversation exists first to avoid Hibernate transient entity issues
        ConversationEntity conversation =
                conversationRepository
                        .findActiveById(id)
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
        softDeleteConversationGroup(groupId, userId);
    }

    @Override
    public List<ConversationMembershipDto> listMemberships(
            String userId, String conversationId, String afterCursor, int limit) {
        UUID cid = UUID.fromString(conversationId);
        UUID groupId = resolveGroupId(cid);
        // Any member can view the membership list
        ensureHasAccess(groupId, userId, AccessLevel.READER);

        StringBuilder jpql =
                new StringBuilder(
                        "SELECT cm FROM ConversationMembershipEntity cm"
                                + " WHERE cm.id.conversationGroupId = :groupId");
        if (afterCursor != null && !afterCursor.isBlank()) {
            jpql.append(" AND cm.id.userId > :afterCursor");
        }
        jpql.append(" ORDER BY cm.id.userId ASC");

        var query =
                entityManager
                        .createQuery(jpql.toString(), ConversationMembershipEntity.class)
                        .setParameter("groupId", groupId)
                        .setMaxResults(limit);
        if (afterCursor != null && !afterCursor.isBlank()) {
            query.setParameter("afterCursor", afterCursor);
        }

        return query.getResultList().stream()
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
                        .findActiveById(cid)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        ConversationGroupEntity conversationGroup = conversation.getConversationGroup();

        // Idempotent: if membership already exists, update access level if different
        var existing =
                membershipRepository.findMembership(conversationGroup.getId(), request.getUserId());
        ConversationMembershipEntity membership;
        if (existing.isPresent()) {
            membership = existing.get();
            if (membership.getAccessLevel() != request.getAccessLevel()) {
                membership.setAccessLevel(request.getAccessLevel());
            }
        } else {
            membership =
                    membershipRepository.createMembership(
                            conversationGroup, request.getUserId(), request.getAccessLevel());
        }

        // Audit log the addition
        membershipAuditLogger.logAdd(
                userId, conversationId, request.getUserId(), request.getAccessLevel());

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
            AccessLevel oldLevel = membership.getAccessLevel();
            membership.setAccessLevel(request.getAccessLevel());

            // Audit log the update
            membershipAuditLogger.logUpdate(
                    userId, conversationId, memberUserId, oldLevel, request.getAccessLevel());
        }
        return toMembershipDto(membership);
    }

    @Override
    @Transactional
    public void deleteMembership(String userId, String conversationId, String memberUserId) {
        UUID cid = UUID.fromString(conversationId);
        UUID groupId = resolveGroupId(cid);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);

        // Get the membership before deletion for audit logging
        Optional<ConversationMembershipEntity> membership =
                membershipRepository.findMembership(groupId, memberUserId);

        if (membership.isPresent()) {
            AccessLevel level = membership.get().getAccessLevel();

            // Hard delete the membership
            membershipRepository.delete(
                    "id.conversationGroupId = ?1 AND id.userId = ?2", groupId, memberUserId);

            // Audit log the removal
            membershipAuditLogger.logRemove(userId, conversationId, memberUserId, level);
        }

        // Delete any pending ownership transfer to the removed member
        ownershipTransferRepository.deleteByConversationGroupAndToUser(groupId, memberUserId);
    }

    /**
     * Sets up a conversation entity as a fork of an existing conversation.
     * The fork joins the parent's ConversationGroup and sets fork pointers.
     * The forkedAtEntryId on the entity is resolved to the entry BEFORE the target,
     * so "fork at entry X" means "include entries before X, exclude X and after".
     */
    private void setupForkConversation(
            ConversationEntity forkEntity,
            String userId,
            String parentConversationId,
            String forkAtEntryId,
            String inferredTitle) {
        UUID originalId = UUID.fromString(parentConversationId);
        ConversationEntity originalEntity =
                conversationRepository
                        .findActiveById(originalId)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", parentConversationId));
        UUID groupId = originalEntity.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.WRITER);

        EntryEntity target =
                entryRepository
                        .findByIdOptional(UUID.fromString(forkAtEntryId))
                        .orElseThrow(() -> new ResourceNotFoundException("entry", forkAtEntryId));
        if (target.getConversation() == null
                || !originalId.equals(target.getConversation().getId())) {
            throw new ResourceNotFoundException("entry", forkAtEntryId);
        }
        if (target.getChannel() != Channel.HISTORY) {
            throw new AccessDeniedException("Forking is only allowed for history entries");
        }

        // Find the previous entry of ANY channel before the target entry.
        // forkedAtEntryId is set to this previous entry — all parent entries up to and including
        // forkedAtEntryId are visible to the fork.
        EntryEntity previous =
                entryRepository
                        .find(
                                "from EntryEntity m where m.conversation.id = ?1 and (m.createdAt <"
                                        + " ?2 or (m.createdAt = ?2 and m.id < ?3)) order by"
                                        + " m.createdAt desc, m.id desc",
                                originalId,
                                target.getCreatedAt(),
                                target.getId())
                        .firstResult();
        UUID previousId = previous != null ? previous.getId() : null;

        forkEntity.setOwnerUserId(originalEntity.getOwnerUserId());
        forkEntity.setTitle(
                encryptTitle(
                        inferredTitle != null
                                ? inferredTitle
                                : decryptTitle(originalEntity.getTitle())));
        forkEntity.setConversationGroup(originalEntity.getConversationGroup());
        forkEntity.setForkedAtConversationId(originalEntity.getId());
        forkEntity.setForkedAtEntryId(previousId);
    }

    @Override
    public List<ConversationForkSummaryDto> listForks(
            String userId, String conversationId, String afterCursor, int limit) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation =
                conversationRepository
                        .findActiveById(cid)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        UUID groupId = conversation.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.READER);

        StringBuilder jpql =
                new StringBuilder(
                        "FROM ConversationEntity c WHERE c.conversationGroup.id = :groupId"
                                + " AND c.deletedAt IS NULL"
                                + " AND c.conversationGroup.deletedAt IS NULL");
        if (afterCursor != null && !afterCursor.isBlank()) {
            jpql.append(" AND c.id > :afterCursor");
        }
        jpql.append(" ORDER BY c.id ASC");

        var query =
                entityManager
                        .createQuery(jpql.toString(), ConversationEntity.class)
                        .setParameter("groupId", groupId)
                        .setMaxResults(limit);
        if (afterCursor != null && !afterCursor.isBlank()) {
            query.setParameter("afterCursor", UUID.fromString(afterCursor));
        }

        List<ConversationEntity> candidates = query.getResultList();
        List<ConversationForkSummaryDto> results = new ArrayList<>();
        for (ConversationEntity candidate : candidates) {
            ConversationForkSummaryDto dto = new ConversationForkSummaryDto();
            dto.setConversationId(candidate.getId().toString());
            dto.setConversationGroupId(groupId.toString());
            dto.setForkedAtEntryId(
                    candidate.getForkedAtEntryId() != null
                            ? candidate.getForkedAtEntryId().toString()
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
    public List<OwnershipTransferDto> listPendingTransfers(
            String userId, String role, String afterCursor, int limit) {
        StringBuilder jpql =
                new StringBuilder("SELECT t FROM ConversationOwnershipTransferEntity t WHERE ");
        switch (role) {
            case "sender":
                jpql.append("t.fromUserId = :userId");
                break;
            case "recipient":
                jpql.append("t.toUserId = :userId");
                break;
            default: // "all"
                jpql.append("(t.fromUserId = :userId OR t.toUserId = :userId)");
        }
        if (afterCursor != null && !afterCursor.isBlank()) {
            jpql.append(" AND t.id > :afterCursor");
        }
        jpql.append(" ORDER BY t.id ASC");

        var query =
                entityManager
                        .createQuery(jpql.toString(), ConversationOwnershipTransferEntity.class)
                        .setParameter("userId", userId)
                        .setMaxResults(limit);
        if (afterCursor != null && !afterCursor.isBlank()) {
            query.setParameter("afterCursor", UUID.fromString(afterCursor));
        }

        return query.getResultList().stream()
                .map(this::toOwnershipTransferDto)
                .collect(Collectors.toList());
    }

    @Override
    public Optional<OwnershipTransferDto> getTransfer(String userId, String transferId) {
        UUID id = UUID.fromString(transferId);
        return ownershipTransferRepository
                .findByIdAndParticipant(id, userId)
                .map(this::toOwnershipTransferDto);
    }

    @Override
    @Transactional
    public OwnershipTransferDto createOwnershipTransfer(
            String userId, CreateOwnershipTransferRequest request) {
        UUID conversationId = UUID.fromString(request.getConversationId());
        UUID groupId = resolveGroupId(conversationId);

        // Verify user is owner
        ConversationMembershipEntity membership =
                membershipRepository
                        .findMembership(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        if (membership.getAccessLevel() != AccessLevel.OWNER) {
            throw new AccessDeniedException("Only owner may transfer ownership");
        }

        // Verify not transferring to self
        String newOwnerUserId = request.getNewOwnerUserId();
        if (userId.equals(newOwnerUserId)) {
            throw new IllegalArgumentException("Cannot transfer ownership to yourself");
        }

        // Verify recipient is a member
        membershipRepository
                .findMembership(groupId, newOwnerUserId)
                .orElseThrow(
                        () ->
                                new IllegalArgumentException(
                                        "Proposed owner must be a member of the conversation"));

        // Check for existing transfer (only one can exist at a time)
        Optional<ConversationOwnershipTransferEntity> existing =
                ownershipTransferRepository.findByConversationGroup(groupId);
        if (existing.isPresent()) {
            throw new ResourceConflictException(
                    "transfer",
                    existing.get().getId().toString(),
                    "A pending ownership transfer already exists for this conversation");
        }

        // Create transfer
        ConversationOwnershipTransferEntity transfer = new ConversationOwnershipTransferEntity();
        transfer.setConversationGroup(membership.getConversationGroup());
        transfer.setFromUserId(userId);
        transfer.setToUserId(newOwnerUserId);
        ownershipTransferRepository.persist(transfer);

        return toOwnershipTransferDto(transfer);
    }

    @Override
    @Transactional
    public void acceptTransfer(String userId, String transferId) {
        UUID id = UUID.fromString(transferId);
        ConversationOwnershipTransferEntity transfer =
                ownershipTransferRepository
                        .findByIdOptional(id)
                        .orElseThrow(() -> new ResourceNotFoundException("transfer", transferId));

        // Verify user is recipient
        if (!userId.equals(transfer.getToUserId())) {
            throw new AccessDeniedException("Only the recipient can accept a transfer");
        }

        UUID groupId = transfer.getConversationGroup().getId();

        // Update new owner's membership to OWNER
        membershipRepository.update(
                "accessLevel = ?1 WHERE id.conversationGroupId = ?2 AND id.userId = ?3",
                AccessLevel.OWNER,
                groupId,
                transfer.getToUserId());

        // Update old owner's membership to MANAGER
        membershipRepository.update(
                "accessLevel = ?1 WHERE id.conversationGroupId = ?2 AND id.userId = ?3",
                AccessLevel.MANAGER,
                groupId,
                transfer.getFromUserId());

        // Update conversation owner_user_id for all conversations in the group
        conversationRepository.update(
                "ownerUserId = ?1 WHERE conversationGroup.id = ?2",
                transfer.getToUserId(),
                groupId);

        // Delete the transfer (transfers are always pending while they exist)
        ownershipTransferRepository.delete(transfer);
    }

    @Override
    @Transactional
    public void deleteTransfer(String userId, String transferId) {
        UUID id = UUID.fromString(transferId);
        ConversationOwnershipTransferEntity transfer =
                ownershipTransferRepository
                        .findByIdOptional(id)
                        .orElseThrow(() -> new ResourceNotFoundException("transfer", transferId));

        // Verify user is sender or recipient
        if (!userId.equals(transfer.getFromUserId()) && !userId.equals(transfer.getToUserId())) {
            throw new AccessDeniedException("Only the sender or recipient can delete a transfer");
        }

        // Hard delete the transfer
        ownershipTransferRepository.delete(transfer);
    }

    private OwnershipTransferDto toOwnershipTransferDto(
            ConversationOwnershipTransferEntity entity) {
        OwnershipTransferDto dto = new OwnershipTransferDto();
        dto.setId(entity.getId().toString());

        // Get conversation ID from group (first non-deleted conversation)
        UUID groupId = entity.getConversationGroup().getId();
        conversationRepository
                .find("conversationGroup.id = ?1 AND deletedAt IS NULL", groupId)
                .firstResultOptional()
                .ifPresent(
                        conv -> {
                            dto.setConversationId(conv.getId().toString());
                            dto.setConversationTitle(decryptTitle(conv.getTitle()));
                        });

        dto.setFromUserId(entity.getFromUserId());
        dto.setToUserId(entity.getToUserId());
        dto.setCreatedAt(ISO_FORMATTER.format(entity.getCreatedAt()));
        return dto;
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
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findActiveById(cid).orElse(null);
        if (conversation == null) {
            LOG.infof(
                    "getEntries: conversation %s not found, returning empty result",
                    conversationId);
            PagedEntries empty = new PagedEntries();
            empty.setConversationId(conversationId);
            empty.setEntries(Collections.emptyList());
            empty.setAfterCursor(null);
            return empty;
        }
        UUID groupId = conversation.getConversationGroup().getId();
        LOG.infof(
                "getEntries: conversationId=%s, groupId=%s, forkedAtConversationId=%s,"
                        + " forkedAtEntryId=%s, channel=%s, clientId=%s, allForks=%s",
                conversationId,
                groupId,
                conversation.getForkedAtConversationId(),
                conversation.getForkedAtEntryId(),
                channel,
                clientId,
                allForks);
        ensureHasAccess(groupId, userId, AccessLevel.READER);

        // When allForks=true, bypass fork ancestry and return all entries in the group
        if (allForks) {
            List<EntryEntity> entries =
                    entryRepository.listByConversationGroup(
                            groupId, channel, channel == Channel.MEMORY ? clientId : null);
            List<EntryEntity> paginatedEntries = applyPagination(entries, afterEntryId, limit);
            LOG.infof(
                    "getEntries: found %d entries for conversationId=%s, allForks=true",
                    paginatedEntries.size(), conversationId);
            return buildPagedEntries(conversationId, paginatedEntries, limit);
        }

        // For MEMORY channel, use fork-aware retrieval with epoch handling
        if (channel == Channel.MEMORY) {
            PagedEntries result =
                    getMemoryEntriesWithForkSupport(
                            conversation, afterEntryId, limit, epochFilter, clientId);
            LOG.infof(
                    "getEntries: found %d MEMORY entries for conversationId=%s (fork-aware)",
                    result.getEntries().size(), conversationId);
            return result;
        }

        // For HISTORY channel (or all channels), use fork-aware retrieval
        PagedEntries result =
                getEntriesWithForkSupport(conversation, afterEntryId, limit, channel, clientId);
        LOG.infof(
                "getEntries: found %d entries for conversationId=%s, channel=%s (fork-aware)",
                result.getEntries().size(), conversationId, channel);
        return result;
    }

    private List<EntryEntity> fetchMemoryEntries(
            UUID conversationId,
            String afterEntryId,
            int limit,
            MemoryEpochFilter epochFilter,
            String clientId) {
        if (clientId == null || clientId.isBlank()) {
            LOG.infof("fetchMemoryEntries: clientId is null/blank, returning empty list");
            return Collections.emptyList();
        }
        MemoryEpochFilter filter = epochFilter != null ? epochFilter : MemoryEpochFilter.latest();
        LOG.infof(
                "fetchMemoryEntries: conversationId=%s, afterEntryId=%s, limit=%d,"
                        + " epochFilter.mode=%s, clientId=%s",
                conversationId, afterEntryId, limit, filter.getMode(), clientId);
        List<EntryEntity> result =
                switch (filter.getMode()) {
                    case ALL ->
                            entryRepository.listByChannel(
                                    conversationId, afterEntryId, limit, Channel.MEMORY, clientId);
                    case LATEST ->
                            fetchLatestMemoryEntries(conversationId, afterEntryId, limit, clientId);
                    case EPOCH ->
                            entryRepository.listMemoryEntriesByEpoch(
                                    conversationId,
                                    afterEntryId,
                                    limit,
                                    filter.getEpoch(),
                                    clientId);
                };
        LOG.infof(
                "fetchMemoryEntries: returning %d entries for conversationId=%s",
                result.size(), conversationId);
        return result;
    }

    private List<EntryEntity> fetchLatestMemoryEntries(
            UUID conversationId, String afterEntryId, int limit, String clientId) {
        // Try cache first - cache stores the complete list, pagination is applied in-memory
        Optional<CachedMemoryEntries> cached = memoryEntriesCache.get(conversationId, clientId);
        if (cached.isPresent()) {
            return paginateCachedEntries(
                    cached.get(), conversationId, clientId, afterEntryId, limit);
        }

        // Cache miss - fetch ALL entries from database to populate cache
        List<EntryEntity> allEntries =
                entryRepository.listMemoryEntriesAtLatestEpoch(conversationId, clientId);

        // Populate cache with complete list
        if (!allEntries.isEmpty()) {
            CachedMemoryEntries toCache = toCachedMemoryEntries(allEntries);
            memoryEntriesCache.set(conversationId, clientId, toCache);
        }

        // Apply pagination in-memory
        return paginateEntries(allEntries, afterEntryId, limit);
    }

    private List<EntryEntity> paginateCachedEntries(
            CachedMemoryEntries cached,
            UUID conversationId,
            String clientId,
            String afterEntryId,
            int limit) {
        List<CachedMemoryEntries.CachedEntry> entries = cached.entries();
        long epoch = cached.epoch();

        // Find starting index based on afterEntryId cursor
        int startIndex = 0;
        if (afterEntryId != null) {
            UUID afterId = UUID.fromString(afterEntryId);
            for (int i = 0; i < entries.size(); i++) {
                if (entries.get(i).id().equals(afterId)) {
                    startIndex = i + 1; // Start after the cursor entry
                    break;
                }
            }
        }

        // Apply pagination and convert to EntryEntity
        return entries.stream()
                .skip(startIndex)
                .limit(limit)
                .map(ce -> toCachedEntryEntity(ce, conversationId, clientId, epoch))
                .toList();
    }

    private List<EntryEntity> paginateEntries(
            List<EntryEntity> entries, String afterEntryId, int limit) {
        // Find starting index based on afterEntryId cursor
        int startIndex = 0;
        if (afterEntryId != null) {
            UUID afterId = UUID.fromString(afterEntryId);
            for (int i = 0; i < entries.size(); i++) {
                if (entries.get(i).getId().equals(afterId)) {
                    startIndex = i + 1; // Start after the cursor entry
                    break;
                }
            }
        }

        // Apply pagination
        return entries.stream().skip(startIndex).limit(limit).toList();
    }

    private CachedMemoryEntries toCachedMemoryEntries(List<EntryEntity> entries) {
        if (entries.isEmpty()) {
            return null;
        }
        long epoch = entries.get(0).getEpoch() != null ? entries.get(0).getEpoch() : 0L;
        List<CachedMemoryEntries.CachedEntry> cachedEntries =
                entries.stream()
                        .map(
                                e ->
                                        new CachedMemoryEntries.CachedEntry(
                                                e.getId(),
                                                e.getContentType(),
                                                e.getContent(), // Already encrypted bytes
                                                e.getCreatedAt().toInstant()))
                        .toList();
        return new CachedMemoryEntries(epoch, cachedEntries);
    }

    /**
     * Updates the cache with the current latest epoch entries. Called after sync modifications to
     * keep the cache warm instead of invalidating it.
     */
    private void updateCacheWithLatestEntries(UUID conversationId, String clientId) {
        LOG.infof(
                "updateCacheWithLatestEntries: querying DB for conversationId=%s, clientId=%s",
                conversationId, clientId);
        List<EntryEntity> entries =
                entryRepository.listMemoryEntriesAtLatestEpoch(conversationId, clientId);
        LOG.infof(
                "updateCacheWithLatestEntries: DB returned %d entries for conversationId=%s",
                entries.size(), conversationId);
        if (!entries.isEmpty()) {
            CachedMemoryEntries cached = toCachedMemoryEntries(entries);
            memoryEntriesCache.set(conversationId, clientId, cached);
            LOG.infof(
                    "updateCacheWithLatestEntries: cached %d entries at epoch %d for"
                            + " conversationId=%s",
                    cached.entries().size(), cached.epoch(), conversationId);
        } else {
            // No entries at latest epoch - remove stale cache entry
            memoryEntriesCache.remove(conversationId, clientId);
            LOG.infof(
                    "updateCacheWithLatestEntries: removed cache for conversationId=%s (no"
                            + " entries)",
                    conversationId);
        }
    }

    private EntryEntity toCachedEntryEntity(
            CachedMemoryEntries.CachedEntry cached,
            UUID conversationId,
            String clientId,
            long epoch) {
        EntryEntity entity = new EntryEntity();
        entity.setId(cached.id());
        // Create a minimal ConversationEntity with just the ID for toEntryDto
        ConversationEntity conversation = new ConversationEntity();
        conversation.setId(conversationId);
        entity.setConversation(conversation);
        entity.setClientId(clientId);
        entity.setChannel(Channel.MEMORY);
        entity.setEpoch(epoch);
        entity.setContentType(cached.contentType());
        entity.setContent(cached.encryptedContent()); // Still encrypted
        entity.setCreatedAt(cached.createdAt().atOffset(java.time.ZoneOffset.UTC));
        return entity;
    }

    /**
     * Builds the ancestry stack for fork-aware entry retrieval. The stack contains all
     * conversations from the root to the target conversation, in order.
     *
     * <p>Each ForkAncestor contains:
     *
     * <ul>
     *   <li>conversationId: the conversation in the chain
     *   <li>forkedAtEntryId: the last entry to include from this conversation (fork point), null
     *       means include all entries from this conversation
     * </ul>
     *
     * <p>Important: The fork point for a parent conversation comes from its child's
     * forkedAtEntryId. For example, if fork B has forkedAtEntryId=X pointing to an entry in
     * conversation A, then conversation A should only include entries up to and including X.
     *
     * @param targetConversation the conversation to build ancestry for
     * @return list of ancestors from root (first) to target (last)
     */
    private List<ForkAncestor> buildAncestryStack(ConversationEntity targetConversation) {
        UUID groupId = targetConversation.getConversationGroup().getId();

        // Single query: get all conversations in the group
        List<ConversationEntity> allConversations =
                conversationRepository.findActiveByGroupId(groupId);

        // Build lookup map
        Map<UUID, ConversationEntity> byId =
                allConversations.stream()
                        .collect(Collectors.toMap(ConversationEntity::getId, c -> c));

        // Build ancestry chain by traversing from target to root
        // Store tuples of (conversation, forkPointFromChild) where forkPointFromChild
        // is the forkedAtEntryId from the child conversation that tells us where to stop
        List<ForkAncestor> stack = new ArrayList<>();
        ConversationEntity current = targetConversation;
        UUID forkPointFromChild = null; // Target conversation includes all its entries

        while (current != null) {
            // Add current conversation with the fork point limit from its child
            stack.add(new ForkAncestor(current.getId(), forkPointFromChild));

            // For the next iteration (parent), the fork point comes from current's metadata
            forkPointFromChild = current.getForkedAtEntryId();
            UUID parentId = current.getForkedAtConversationId();
            current = (parentId != null) ? byId.get(parentId) : null;
        }

        Collections.reverse(stack); // Root first
        LOG.infof(
                "buildAncestryStack: targetConversation=%s, stack size=%d, stack=%s",
                targetConversation.getId(),
                stack.size(),
                stack.stream()
                        .map(
                                a ->
                                        String.format(
                                                "(conv=%s, stopAt=%s)",
                                                a.conversationId(), a.forkedAtEntryId()))
                        .toList());
        return stack;
    }

    /**
     * Checks if a conversation is a fork (has a parent conversation).
     *
     * @param conversation the conversation to check
     * @return true if this is a forked conversation
     */
    private boolean isFork(ConversationEntity conversation) {
        return conversation.getForkedAtConversationId() != null;
    }

    /**
     * Shared implementation for fork-aware entry retrieval. Used by both user and admin APIs.
     *
     * @param conversation the conversation to fetch entries for
     * @param afterEntryId cursor for pagination
     * @param limit maximum entries to return
     * @param channel channel filter (null for all channels)
     * @param clientId client ID (required for MEMORY channel)
     * @return paged entries including parent entries up to fork point
     */
    private PagedEntries getEntriesWithForkSupport(
            ConversationEntity conversation,
            String afterEntryId,
            int limit,
            Channel channel,
            String clientId) {
        UUID conversationId = conversation.getId();

        // Build ancestry stack for fork-aware retrieval
        List<ForkAncestor> ancestry = buildAncestryStack(conversation);
        UUID groupId = conversation.getConversationGroup().getId();

        // Query ALL entries in the group (need all channels for fork point tracking)
        List<EntryEntity> allEntries = entryRepository.listByConversationGroup(groupId, null, null);
        LOG.infof(
                "getEntriesWithForkSupport: fetched %d entries from group %s",
                allEntries.size(), groupId);

        // Filter entries based on ancestry chain
        List<EntryEntity> filteredEntries = filterEntriesByAncestry(allEntries, ancestry);
        LOG.infof(
                "getEntriesWithForkSupport: after ancestry filter, %d entries",
                filteredEntries.size());

        // Post-filter by channel and clientId (after ancestry is resolved)
        if (channel != null) {
            filteredEntries =
                    filteredEntries.stream().filter(e -> e.getChannel() == channel).toList();
        }
        if (channel == Channel.MEMORY && clientId != null) {
            filteredEntries =
                    filteredEntries.stream().filter(e -> clientId.equals(e.getClientId())).toList();
        }

        // Apply pagination
        List<EntryEntity> paginatedEntries = applyPagination(filteredEntries, afterEntryId, limit);

        return buildPagedEntries(conversationId.toString(), paginatedEntries, limit);
    }

    /**
     * Filters entries based on the fork ancestry chain. Only includes entries that belong to:
     *
     * <ul>
     *   <li>Ancestor conversations, up to (and including) their fork point
     *   <li>The target conversation (all entries)
     * </ul>
     */
    private List<EntryEntity> filterEntriesByAncestry(
            List<EntryEntity> allEntries, List<ForkAncestor> ancestry) {
        if (ancestry.isEmpty()) {
            return allEntries;
        }

        List<EntryEntity> result = new ArrayList<>();
        int ancestorIndex = 0;
        ForkAncestor current = ancestry.get(ancestorIndex);
        boolean isTargetConversation = (ancestorIndex == ancestry.size() - 1);

        for (EntryEntity entry : allEntries) {
            UUID entryConversationId = entry.getConversation().getId();

            // Check if we're processing the current ancestor
            if (entryConversationId.equals(current.conversationId())) {
                // For target conversation (last in ancestry), include all entries
                // For ancestors, include entries up to and including the fork point
                if (isTargetConversation) {
                    result.add(entry);
                } else {
                    // This is an ancestor conversation
                    result.add(entry);

                    // Check if we've reached the fork point for this ancestor
                    if (current.forkedAtEntryId() != null
                            && entry.getId().equals(current.forkedAtEntryId())) {
                        // Move to next in ancestry chain
                        ancestorIndex++;
                        if (ancestorIndex < ancestry.size()) {
                            current = ancestry.get(ancestorIndex);
                            isTargetConversation = (ancestorIndex == ancestry.size() - 1);
                        }
                    }
                }
            }
            // Entries from other conversations in the group are skipped
        }

        return result;
    }

    /**
     * Fork-aware MEMORY channel retrieval with epoch handling.
     *
     * <p>For forked conversations, this method:
     * <ol>
     *   <li>Builds the ancestry stack
     *   <li>Queries all entries in the conversation group (all channels for fork tracking)
     *   <li>Filters entries following the fork ancestry path
     *   <li>Applies epoch filtering (LATEST mode clears previous epochs when higher epoch found)
     * </ol>
     *
     * @param conversation the conversation to fetch entries for
     * @param afterEntryId cursor for pagination
     * @param limit maximum entries to return
     * @param epochFilter epoch filter mode (LATEST, ALL, or specific epoch)
     * @param clientId client ID (required for MEMORY channel)
     * @return paged entries with epoch-aware filtering
     */
    private PagedEntries getMemoryEntriesWithForkSupport(
            ConversationEntity conversation,
            String afterEntryId,
            int limit,
            MemoryEpochFilter epochFilter,
            String clientId) {
        UUID conversationId = conversation.getId();

        // For non-forked conversations, use existing efficient queries
        if (!isFork(conversation)) {
            LOG.infof(
                    "getMemoryEntriesWithForkSupport: conversation %s is not a fork, using direct"
                            + " query",
                    conversationId);
            List<EntryEntity> entries =
                    fetchMemoryEntries(conversationId, afterEntryId, limit, epochFilter, clientId);
            return buildPagedEntries(conversationId.toString(), entries, limit);
        }

        // Build ancestry stack for fork-aware retrieval
        List<ForkAncestor> ancestry = buildAncestryStack(conversation);
        UUID groupId = conversation.getConversationGroup().getId();

        // Query ALL entries in the group (need all channels for fork point tracking)
        // Don't filter by clientId in query - filter in Java after fork traversal
        List<EntryEntity> allEntries = entryRepository.listByConversationGroup(groupId, null, null);
        LOG.infof(
                "getMemoryEntriesWithForkSupport: fetched %d entries from group %s",
                allEntries.size(), groupId);

        // Filter entries based on ancestry and epoch
        List<EntryEntity> filteredEntries =
                filterMemoryEntriesWithEpoch(allEntries, ancestry, clientId, epochFilter);
        LOG.infof(
                "getMemoryEntriesWithForkSupport: after epoch filter, %d MEMORY entries",
                filteredEntries.size());

        // Apply pagination
        List<EntryEntity> paginatedEntries = applyPagination(filteredEntries, afterEntryId, limit);

        return buildPagedEntries(conversationId.toString(), paginatedEntries, limit);
    }

    /**
     * Filters MEMORY entries following fork ancestry with epoch handling.
     *
     * <p>Algorithm:
     * <ul>
     *   <li>Iterate through entries following fork ancestry path (using ALL entries for tracking)
     *   <li>Track maxEpochSeen; when epoch > maxEpochSeen, clear result (new epoch supersedes all)
     *   <li>Only include MEMORY entries with matching clientId in the result
     * </ul>
     *
     * <p>Key insight: All entries (all channels) are needed for fork point tracking, but only
     * MEMORY entries with matching clientId are added to the result.
     *
     * @param allEntries all entries in the conversation group (all channels)
     * @param ancestry fork ancestry chain from root to target
     * @param clientId the client ID to filter MEMORY entries
     * @param epochFilter epoch filter mode
     * @return filtered MEMORY entries following fork ancestry with epoch handling
     */
    private List<EntryEntity> filterMemoryEntriesWithEpoch(
            List<EntryEntity> allEntries,
            List<ForkAncestor> ancestry,
            String clientId,
            MemoryEpochFilter epochFilter) {

        if (ancestry.isEmpty()) {
            // No ancestry, return all MEMORY entries matching clientId and epoch
            return filterByEpochOnly(allEntries, clientId, epochFilter);
        }

        MemoryEpochFilter filter = epochFilter != null ? epochFilter : MemoryEpochFilter.latest();
        List<EntryEntity> result = new ArrayList<>();
        long maxEpochSeen = 0;
        int ancestorIndex = 0;
        ForkAncestor currentAncestor = ancestry.get(ancestorIndex);
        boolean isTargetConversation = (ancestorIndex == ancestry.size() - 1);

        for (EntryEntity entry : allEntries) {
            UUID entryConversationId = entry.getConversation().getId();

            // Skip entries not in current ancestor conversation
            if (!entryConversationId.equals(currentAncestor.conversationId())) {
                continue;
            }

            // Process MEMORY entries with matching clientId for the result
            if (entry.getChannel() == Channel.MEMORY) {
                if (clientId != null && clientId.equals(entry.getClientId())) {
                    long entryEpoch = entry.getEpoch() != null ? entry.getEpoch() : 0L;

                    switch (filter.getMode()) {
                        case LATEST -> {
                            if (entryEpoch > maxEpochSeen) {
                                result.clear(); // New epoch supersedes all previous
                                maxEpochSeen = entryEpoch;
                            }
                            if (entryEpoch == maxEpochSeen) {
                                result.add(entry);
                            }
                            // entryEpoch < maxEpochSeen: skip (outdated)
                        }
                        case ALL -> result.add(entry);
                        case EPOCH -> {
                            Long filterEpoch = filter.getEpoch();
                            if ((filterEpoch == null && entryEpoch == 0L)
                                    || (filterEpoch != null && filterEpoch == entryEpoch)) {
                                result.add(entry);
                            }
                        }
                    }
                }
            }

            // Check if we've reached the fork point (after processing the entry)
            // This uses ALL entries for tracking, not just MEMORY
            if (!isTargetConversation
                    && currentAncestor.forkedAtEntryId() != null
                    && entry.getId().equals(currentAncestor.forkedAtEntryId())) {
                // Move to next in ancestry chain
                ancestorIndex++;
                if (ancestorIndex < ancestry.size()) {
                    currentAncestor = ancestry.get(ancestorIndex);
                    isTargetConversation = (ancestorIndex == ancestry.size() - 1);
                }
            }
        }

        return result;
    }

    /**
     * Simple epoch-only filtering for non-forked conversations.
     */
    private List<EntryEntity> filterByEpochOnly(
            List<EntryEntity> allEntries, String clientId, MemoryEpochFilter epochFilter) {
        MemoryEpochFilter filter = epochFilter != null ? epochFilter : MemoryEpochFilter.latest();
        List<EntryEntity> result = new ArrayList<>();
        long maxEpochSeen = 0;

        for (EntryEntity entry : allEntries) {
            if (entry.getChannel() != Channel.MEMORY) {
                continue;
            }
            if (clientId != null && !clientId.equals(entry.getClientId())) {
                continue;
            }

            long entryEpoch = entry.getEpoch() != null ? entry.getEpoch() : 0L;

            switch (filter.getMode()) {
                case LATEST -> {
                    if (entryEpoch > maxEpochSeen) {
                        result.clear();
                        maxEpochSeen = entryEpoch;
                    }
                    if (entryEpoch == maxEpochSeen) {
                        result.add(entry);
                    }
                }
                case ALL -> result.add(entry);
                case EPOCH -> {
                    Long filterEpoch = filter.getEpoch();
                    if ((filterEpoch == null && entryEpoch == 0L)
                            || (filterEpoch != null && filterEpoch == entryEpoch)) {
                        result.add(entry);
                    }
                }
            }
        }

        return result;
    }

    /**
     * Applies cursor-based pagination to the entry list.
     *
     * @param entries the filtered entries (already in order)
     * @param afterEntryId cursor entry ID to start after (null to start from beginning)
     * @param limit maximum entries to return
     * @return paginated entries
     */
    private List<EntryEntity> applyPagination(
            List<EntryEntity> entries, String afterEntryId, int limit) {
        int startIndex = 0;
        if (afterEntryId != null) {
            UUID afterId = UUID.fromString(afterEntryId);
            for (int i = 0; i < entries.size(); i++) {
                if (entries.get(i).getId().equals(afterId)) {
                    startIndex = i + 1;
                    break;
                }
            }
        }
        return entries.stream().skip(startIndex).limit(limit).toList();
    }

    /**
     * Builds a PagedEntries response from entry entities.
     *
     * @param conversationId the conversation ID for the response
     * @param entries the entries to include
     * @param limit the requested limit (for determining next cursor)
     * @return paged entries with cursor for pagination
     */
    private PagedEntries buildPagedEntries(
            String conversationId, List<EntryEntity> entries, int limit) {
        PagedEntries page = new PagedEntries();
        page.setConversationId(conversationId);
        List<EntryDto> dtos = entries.stream().map(this::toEntryDto).toList();
        page.setEntries(dtos);
        String nextCursor =
                dtos.size() == limit && !dtos.isEmpty() ? dtos.get(dtos.size() - 1).getId() : null;
        page.setAfterCursor(nextCursor);
        return page;
    }

    @Override
    @Transactional
    public List<EntryDto> appendMemoryEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId,
            Long epoch) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findActiveById(cid).orElse(null);

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (conversation == null) {
            // Check first entry for fork metadata
            CreateEntryRequest firstEntry = entries.isEmpty() ? null : entries.get(0);
            String forkConvId =
                    firstEntry != null && firstEntry.getForkedAtConversationId() != null
                            ? firstEntry.getForkedAtConversationId().toString()
                            : null;
            String forkEntryId =
                    firstEntry != null && firstEntry.getForkedAtEntryId() != null
                            ? firstEntry.getForkedAtEntryId().toString()
                            : null;

            conversation = new ConversationEntity();
            conversation.setId(cid);
            conversation.setMetadata(Collections.emptyMap());

            if (forkConvId != null && forkEntryId != null) {
                // Fork auto-creation: join parent's group instead of creating a new one
                setupForkConversation(
                        conversation,
                        userId,
                        forkConvId,
                        forkEntryId,
                        inferTitleFromEntries(entries));
                conversationRepository.persist(conversation);
                // No membership creation for forks — user already has membership via parent group
            } else {
                // Root conversation auto-creation
                conversation.setOwnerUserId(userId);
                conversation.setTitle(encryptTitle(inferTitleFromEntries(entries)));
                ConversationGroupEntity conversationGroup =
                        conversationGroupRepository
                                .findActiveById(cid)
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
            }
        } else {
            UUID groupId = conversation.getConversationGroup().getId();
            ensureHasAccess(groupId, userId, AccessLevel.WRITER);
        }

        // For MEMORY channel entries, determine the epoch to use.
        // INVARIANT: Memory channel entries must ALWAYS have a non-null epoch.
        // If no epoch is provided, look up the latest epoch or default to 1.
        Long effectiveEpoch = epoch;
        boolean hasMemoryEntries =
                entries.stream()
                        .anyMatch(
                                e ->
                                        e.getChannel() == CreateEntryRequest.ChannelEnum.MEMORY
                                                || e.getChannel() == null);
        if (hasMemoryEntries && effectiveEpoch == null) {
            // Look up the latest epoch for this conversation+clientId
            List<EntryEntity> latestEntries =
                    entryRepository.listMemoryEntriesAtLatestEpoch(cid, clientId);
            if (latestEntries.isEmpty()) {
                effectiveEpoch = 1L; // Initial epoch starts at 1
            } else {
                effectiveEpoch = latestEntries.get(0).getEpoch();
                if (effectiveEpoch == null) {
                    // Safety: if somehow existing entries have null epoch, start fresh at 1
                    effectiveEpoch = 1L;
                }
            }
            LOG.infof(
                    "Auto-calculated epoch for MEMORY entries: conversationId=%s, clientId=%s,"
                            + " epoch=%d",
                    conversationId, clientId, effectiveEpoch);
        }

        OffsetDateTime latestHistoryTimestamp = null;
        List<EntryDto> created = new ArrayList<>(entries.size());
        List<EntryEntity> entriesToVectorize = new ArrayList<>();
        for (CreateEntryRequest req : entries) {
            EntryEntity entity = new EntryEntity();
            entity.setConversation(conversation);
            entity.setClientId(clientId);
            Channel channel;
            if (req.getChannel() != null) {
                channel =
                        io.github.chirino.memory.model.Channel.fromString(req.getChannel().value());
            } else {
                channel = io.github.chirino.memory.model.Channel.MEMORY;
            }
            entity.setChannel(channel);
            entity.setUserId(req.getUserId());

            // Set epoch based on channel type
            // INVARIANT: Memory channel entries must ALWAYS have a non-null epoch.
            // History channel entries always have null epoch.
            if (channel == Channel.MEMORY) {
                entity.setEpoch(effectiveEpoch);
            } else {
                entity.setEpoch(null);
            }

            String defaultContentType = (channel == Channel.HISTORY) ? "history" : "message";
            entity.setContentType(
                    req.getContentType() != null ? req.getContentType() : defaultContentType);
            entity.setContent(encryptContent(req.getContent()));
            // For HISTORY entries, default indexedContent to text extracted from content
            String indexedContent = req.getIndexedContent();
            if (indexedContent == null && channel == Channel.HISTORY && req.getContent() != null) {
                indexedContent = extractSearchText(req.getContent());
            }
            entity.setIndexedContent(indexedContent);
            entity.setConversationGroupId(conversation.getConversationGroup().getId());
            OffsetDateTime createdAt = OffsetDateTime.now();
            entity.setCreatedAt(createdAt);
            entryRepository.persist(entity);
            boolean requestProvidedIndexedContent =
                    req.getIndexedContent() != null && !req.getIndexedContent().isBlank();
            if (indexedContent != null && !indexedContent.isBlank()) {
                LOG.infof(
                        "Entry created with indexedContent: entryId=%s, conversationId=%s,"
                                + " channel=%s, source=%s, contentLength=%d",
                        entity.getId(),
                        conversation.getId(),
                        channel,
                        requestProvidedIndexedContent ? "request" : "derived",
                        indexedContent.length());
            }
            if (indexedContent != null && !indexedContent.isBlank()) {
                entriesToVectorize.add(entity);
            }
            if (entity.getChannel() == Channel.HISTORY) {
                latestHistoryTimestamp = createdAt;
            }
            created.add(toEntryDto(entity));
        }
        // Flush to ensure entries are visible to subsequent queries (e.g., cache updates)
        LOG.infof("appendMemoryEntries: flushing %d entries to database", created.size());
        entryRepository.flush();
        LOG.infof("appendMemoryEntries: flush completed");

        // Fire a CDI event for best-effort vectorization. The observer runs AFTER this
        // transaction commits (TransactionPhase.AFTER_SUCCESS), so:
        // 1. The entry rows are committed and visible for the embedding FK constraint.
        // 2. The DB connection is released — not held open during the embedding network call.
        // 3. If vectorization fails, entries remain with indexed_at=NULL for retry.
        if (!entriesToVectorize.isEmpty()) {
            List<EntryToVectorize> toVectorize =
                    entriesToVectorize.stream()
                            .map(
                                    e ->
                                            new EntryToVectorize(
                                                    e.getId().toString(),
                                                    e.getConversation().getId().toString(),
                                                    e.getConversationGroupId().toString(),
                                                    e.getIndexedContent()))
                            .toList();
            vectorizationEvent.fire(new EntryVectorizationEvent(toVectorize));
        }

        // Invalidate/update cache if MEMORY entries were created
        if (hasMemoryEntries && clientId != null) {
            LOG.infof(
                    "appendMemoryEntries: updating cache for conversationId=%s, clientId=%s",
                    conversationId, clientId);
            updateCacheWithLatestEntries(cid, clientId);
        }

        if (latestHistoryTimestamp != null) {
            conversationRepository.update(
                    "updatedAt = ?1 where id = ?2", latestHistoryTimestamp, conversation.getId());
        }
        return created;
    }

    @Override
    @Transactional
    public SyncResult syncAgentEntry(
            String userId, String conversationId, CreateEntryRequest entry, String clientId) {
        validateSyncEntry(entry);
        UUID cid = UUID.fromString(conversationId);

        // Load conversation to check if it's a fork
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        if (conversation == null) {
            // Auto-create: no existing entries, so just append all incoming content as epoch 1.
            // appendMemoryEntries handles conversation auto-creation.
            SyncResult result = new SyncResult();
            if (entry.getContent() == null || entry.getContent().isEmpty()) {
                result.setNoOp(true);
                return result;
            }
            Long initialEpoch = 1L;
            List<EntryDto> appended =
                    appendMemoryEntries(
                            userId, conversationId, List.of(entry), clientId, initialEpoch);
            updateCacheWithLatestEntries(cid, clientId);
            result.setEpoch(initialEpoch);
            result.setEntry(appended.isEmpty() ? null : appended.get(0));
            result.setEpochIncremented(true);
            result.setNoOp(false);
            return result;
        }

        List<EntryDto> latestEpochEntries;
        Long latestEpoch;

        if (isFork(conversation)) {
            // Fork-aware retrieval: include inherited parent entries for content comparison.
            // Without this, prefix detection fails because the fork has no entries of its own
            // initially, causing all incoming messages to be bundled into a single entry.
            List<ForkAncestor> ancestry = buildAncestryStack(conversation);
            UUID groupId = conversation.getConversationGroup().getId();
            List<EntryEntity> allEntries =
                    entryRepository.listByConversationGroup(groupId, null, null);
            List<EntryEntity> filteredEntries =
                    filterMemoryEntriesWithEpoch(
                            allEntries, ancestry, clientId, MemoryEpochFilter.latest());
            latestEpochEntries =
                    filteredEntries.stream().map(this::toEntryDto).collect(Collectors.toList());
            latestEpoch =
                    filteredEntries.stream()
                            .map(EntryEntity::getEpoch)
                            .filter(Objects::nonNull)
                            .max(Long::compare)
                            .orElse(null);
        } else {
            // Combined query: finds max epoch and lists entries in single round-trip
            List<EntryEntity> latestEpochEntityList =
                    entryRepository.listMemoryEntriesAtLatestEpoch(cid, clientId);
            latestEpochEntries =
                    latestEpochEntityList.stream()
                            .map(this::toEntryDto)
                            .collect(Collectors.toList());
            // Extract epoch from entries (all entries in the list share the same epoch)
            latestEpoch =
                    latestEpochEntityList.isEmpty()
                            ? null
                            : latestEpochEntityList.get(0).getEpoch();
        }

        // Flatten content from all existing entries and get incoming content
        List<Object> existingContent = MemorySyncHelper.flattenContent(latestEpochEntries);
        List<Object> incomingContent =
                entry.getContent() != null ? entry.getContent() : Collections.emptyList();

        SyncResult result = new SyncResult();
        result.setEntry(null);
        result.setEpoch(latestEpoch);

        // Check for contentType mismatch - if any existing entry has different contentType, diverge
        if (MemorySyncHelper.hasContentTypeMismatch(latestEpochEntries, entry.getContentType())) {
            // ContentType changed - create new epoch with all incoming content
            Long nextEpoch = MemorySyncHelper.nextEpoch(latestEpoch);
            CreateEntryRequest toAppend = MemorySyncHelper.withContent(entry, incomingContent);
            List<EntryDto> appended =
                    appendMemoryEntries(
                            userId, conversationId, List.of(toAppend), clientId, nextEpoch);
            // Update cache with new epoch entries
            updateCacheWithLatestEntries(cid, clientId);
            result.setEpoch(nextEpoch);
            result.setEntry(appended.isEmpty() ? null : appended.get(0));
            result.setEpochIncremented(true);
            result.setNoOp(false);
            return result;
        }

        // If existing content matches incoming exactly, it's a no-op
        if (MemorySyncHelper.contentEquals(existingContent, incomingContent)) {
            LOG.infof(
                    "syncAgentEntry: content equals - no-op for conversationId=%s (no cache"
                            + " update)",
                    conversationId);
            result.setNoOp(true);
            return result;
        }

        // If incoming is a prefix extension of existing (only adding more content), append delta
        if (MemorySyncHelper.isContentPrefix(existingContent, incomingContent)) {
            List<Object> deltaContent =
                    MemorySyncHelper.extractDelta(existingContent, incomingContent);
            if (deltaContent.isEmpty()) {
                // No new content - this is a no-op
                LOG.infof(
                        "syncAgentEntry: prefix but empty delta - no-op for conversationId=%s (no"
                                + " cache update)",
                        conversationId);
                result.setNoOp(true);
                return result;
            }
            LOG.infof(
                    "syncAgentEntry: prefix extension with %d delta items for conversationId=%s",
                    deltaContent.size(), conversationId);
            // Use latestEpoch if available, otherwise this is the first entry so use initial epoch
            Long epochToUse = latestEpoch != null ? latestEpoch : 1L;
            CreateEntryRequest deltaEntry = MemorySyncHelper.withContent(entry, deltaContent);
            List<EntryDto> appended =
                    appendMemoryEntries(
                            userId, conversationId, List.of(deltaEntry), clientId, epochToUse);
            // Update cache with appended entry
            updateCacheWithLatestEntries(cid, clientId);
            result.setEntry(appended.isEmpty() ? null : appended.get(0));
            result.setEpochIncremented(false);
            result.setNoOp(false);
            return result;
        }

        // Content diverged - create new epoch with all incoming content
        Long nextEpoch = MemorySyncHelper.nextEpoch(latestEpoch);
        CreateEntryRequest toAppend = MemorySyncHelper.withContent(entry, incomingContent);
        List<EntryDto> appended =
                appendMemoryEntries(userId, conversationId, List.of(toAppend), clientId, nextEpoch);
        // Update cache with new epoch entries
        updateCacheWithLatestEntries(cid, clientId);
        result.setEpoch(nextEpoch);
        result.setEntry(appended.isEmpty() ? null : appended.get(0));
        result.setEpochIncremented(true);
        result.setNoOp(false);
        return result;
    }

    private void validateSyncEntry(CreateEntryRequest entry) {
        if (entry == null) {
            throw new IllegalArgumentException("entry is required");
        }
        if (entry.getChannel() == null
                || entry.getChannel() != CreateEntryRequest.ChannelEnum.MEMORY) {
            throw new IllegalArgumentException("sync entry must target memory channel");
        }
        // Empty content is allowed - it creates an empty epoch to clear memory
    }

    @Override
    @Transactional
    public IndexConversationsResponse indexEntries(List<IndexEntryRequest> entries) {
        int indexed = 0;
        List<EntryEntity> entitiesToVectorize = new ArrayList<>();

        for (IndexEntryRequest req : entries) {
            UUID conversationId = UUID.fromString(req.getConversationId());
            UUID entryId = UUID.fromString(req.getEntryId());

            EntryEntity entry = entryRepository.findById(entryId);
            if (entry == null) {
                throw new ResourceNotFoundException("entry", req.getEntryId());
            }
            if (!entry.getConversation().getId().equals(conversationId)) {
                throw new ResourceNotFoundException("entry", req.getEntryId());
            }
            if (entry.getChannel() != Channel.HISTORY) {
                throw new IllegalArgumentException("Only history channel entries can be indexed");
            }

            entry.setIndexedContent(req.getIndexedContent());
            entryRepository.persist(entry);
            if (req.getIndexedContent() != null) {
                LOG.infof(
                        "Index request received indexedContent: entryId=%s, conversationId=%s,"
                                + " contentLength=%d",
                        entry.getId(),
                        entry.getConversation().getId(),
                        req.getIndexedContent().length());
            }
            entitiesToVectorize.add(entry);
            indexed++;
        }

        // Attempt synchronous vector store indexing
        if (shouldVectorize()) {
            boolean anyFailed = false;
            for (EntryEntity entry : entitiesToVectorize) {
                try {
                    vectorizeEntry(entry);
                    entry.setIndexedAt(OffsetDateTime.now());
                    entryRepository.persist(entry);
                } catch (Exception e) {
                    LOG.warnf(e, "Failed to vectorize entry %s", entry.getId());
                    anyFailed = true;
                }
            }
            if (anyFailed) {
                // Create singleton retry task
                taskRepository.createTask(
                        "vector_store_index_retry", "vector_store_index_retry", Map.of());
            }
        } else {
            // No vector store active — full-text indexing (via DB generated column) is the only
            // store and succeeds automatically on persist, so mark entries as fully indexed.
            for (EntryEntity entry : entitiesToVectorize) {
                entry.setIndexedAt(OffsetDateTime.now());
                entryRepository.persist(entry);
            }
        }

        return new IndexConversationsResponse(indexed);
    }

    private void vectorizeEntry(EntryEntity entry) {
        String text = entry.getIndexedContent();
        if (text == null || text.isBlank()) {
            return;
        }
        VectorSearchStore store = searchStoreSelector.getSearchStore();
        if (store == null || !store.isEnabled()) {
            return;
        }
        float[] embedding = embeddingService.embed(text);
        if (embedding == null || embedding.length == 0) {
            return;
        }
        store.upsertTranscriptEmbedding(
                entry.getConversationGroupId().toString(),
                entry.getConversation().getId().toString(),
                entry.getId().toString(),
                embedding);
    }

    @Override
    public UnindexedEntriesResponse listUnindexedEntries(int limit, String afterCursor) {
        // Query entries where channel = HISTORY AND indexed_content IS NULL
        // Order by created_at for consistent pagination

        StringBuilder queryBuilder =
                new StringBuilder(
                        "SELECT e FROM EntryEntity e WHERE e.channel = :channel AND"
                                + " e.indexedContent IS NULL");

        // Handle cursor-based pagination
        if (afterCursor != null && !afterCursor.isBlank()) {
            try {
                // Cursor is base64 encoded createdAt timestamp
                String decoded =
                        new String(
                                java.util.Base64.getDecoder().decode(afterCursor),
                                StandardCharsets.UTF_8);
                queryBuilder.append(" AND e.createdAt > :cursorTime");
            } catch (Exception e) {
                // Invalid cursor, ignore
            }
        }

        queryBuilder.append(" ORDER BY e.createdAt ASC");

        var query =
                entityManager
                        .createQuery(queryBuilder.toString(), EntryEntity.class)
                        .setParameter("channel", Channel.HISTORY)
                        .setMaxResults(limit + 1); // Fetch one extra to check for next page

        if (afterCursor != null && !afterCursor.isBlank()) {
            try {
                String decoded =
                        new String(
                                java.util.Base64.getDecoder().decode(afterCursor),
                                StandardCharsets.UTF_8);
                OffsetDateTime cursorTime = OffsetDateTime.parse(decoded, ISO_FORMATTER);
                query.setParameter("cursorTime", cursorTime);
            } catch (Exception e) {
                // Invalid cursor, ignore
            }
        }

        List<EntryEntity> results = query.getResultList();

        // Determine next cursor
        String nextCursor = null;
        if (results.size() > limit) {
            results = results.subList(0, limit);
            if (!results.isEmpty()) {
                EntryEntity last = results.get(results.size() - 1);
                String timestamp = last.getCreatedAt().format(ISO_FORMATTER);
                nextCursor =
                        java.util.Base64.getEncoder()
                                .encodeToString(timestamp.getBytes(StandardCharsets.UTF_8));
            }
        }

        // Convert to DTOs
        List<UnindexedEntry> data =
                results.stream()
                        .map(
                                e ->
                                        new UnindexedEntry(
                                                e.getConversation().getId().toString(),
                                                toEntryDto(e)))
                        .collect(Collectors.toList());

        return new UnindexedEntriesResponse(data, nextCursor);
    }

    @Override
    public List<EntryDto> findEntriesPendingVectorIndexing(int limit) {
        // Query entries where indexed_content IS NOT NULL AND indexed_at IS NULL
        List<EntryEntity> entries =
                entityManager
                        .createQuery(
                                "SELECT e FROM EntryEntity e WHERE e.indexedContent IS NOT NULL AND"
                                        + " e.indexedAt IS NULL ORDER BY e.createdAt ASC",
                                EntryEntity.class)
                        .setMaxResults(limit)
                        .getResultList();

        return entries.stream().map(this::toEntryDto).collect(Collectors.toList());
    }

    @Override
    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public void setIndexedAt(String entryId, OffsetDateTime indexedAt) {
        UUID id = UUID.fromString(entryId);
        EntryEntity entry = entryRepository.findById(id);
        if (entry != null) {
            entry.setIndexedAt(indexedAt);
            entryRepository.persist(entry);
        }
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

    private boolean shouldVectorize() {
        VectorSearchStore store = searchStoreSelector.getSearchStore();
        return store != null
                && store.isEnabled()
                && store.isSemanticSearchAvailable()
                && embeddingService.isEnabled();
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

    private String inferTitleFromEntries(List<CreateEntryRequest> entries) {
        if (entries == null || entries.isEmpty()) {
            return null;
        }
        for (CreateEntryRequest entry : entries) {
            if (entry == null) {
                continue;
            }
            String text = extractSearchText(entry.getContent());
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

    private SearchResultDto toSearchResult(EntryEntity entity, List<Object> content) {
        SearchResultDto dto = new SearchResultDto();
        dto.setEntry(toEntryDto(entity, content));
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

    private void softDeleteConversationGroup(UUID conversationGroupId, String actorUserId) {
        OffsetDateTime now = OffsetDateTime.now();

        // Log and hard delete memberships BEFORE soft-deleting the group
        List<ConversationMembershipEntity> memberships =
                membershipRepository.listForConversationGroup(conversationGroupId);
        for (ConversationMembershipEntity m : memberships) {
            membershipAuditLogger.logRemove(
                    actorUserId,
                    conversationGroupId.toString(),
                    m.getId().getUserId(),
                    m.getAccessLevel());
        }
        membershipRepository.delete("id.conversationGroupId", conversationGroupId);

        // Mark conversation group as deleted
        conversationGroupRepository.update(
                "deletedAt = ?1 WHERE id = ?2 AND deletedAt IS NULL", now, conversationGroupId);

        // Mark all conversations in the group as deleted
        conversationRepository.update(
                "deletedAt = ?1 WHERE conversationGroup.id = ?2 AND deletedAt IS NULL",
                now,
                conversationGroupId);

        // Hard delete ownership transfers
        ownershipTransferRepository.deleteByConversationGroup(conversationGroupId);
    }

    private UUID resolveGroupId(UUID conversationId) {
        ConversationEntity conversation =
                conversationRepository
                        .findActiveById(conversationId)
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
        if (entity.getDeletedAt() != null) {
            dto.setDeletedAt(ISO_FORMATTER.format(entity.getDeletedAt()));
        }
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
        if (entity.getDeletedAt() != null) {
            dto.setDeletedAt(ISO_FORMATTER.format(entity.getDeletedAt()));
        }
        dto.setConversationGroupId(entity.getConversationGroup().getId().toString());
        dto.setForkedAtEntryId(
                entity.getForkedAtEntryId() != null
                        ? entity.getForkedAtEntryId().toString()
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

    private EntryDto toEntryDto(EntryEntity entity) {
        return toEntryDto(entity, decryptContent(entity.getContent()));
    }

    private EntryDto toEntryDto(EntryEntity entity, List<Object> content) {
        EntryDto dto = new EntryDto();
        dto.setId(entity.getId().toString());
        dto.setConversationId(entity.getConversation().getId().toString());
        dto.setUserId(entity.getUserId());
        dto.setClientId(entity.getClientId());
        dto.setChannel(entity.getChannel());
        dto.setEpoch(entity.getEpoch());
        dto.setContentType(entity.getContentType());
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

    /**
     * Creates content blocks for history channel entries with the required format.
     * @param text The message text (nullable if attachments present)
     * @param role Either "USER" or "AI"
     * @param attachments Optional list of attachment objects
     * @return Content array with a single object containing role and at least one of text or attachments
     */
    // Admin methods

    @Override
    public List<ConversationSummaryDto> adminListConversations(AdminConversationQuery query) {
        StringBuilder jpql = new StringBuilder("FROM ConversationEntity c WHERE 1=1");
        List<Object> params = new ArrayList<>();
        int paramIndex = 1;

        if (query.getUserId() != null && !query.getUserId().isBlank()) {
            jpql.append(" AND c.ownerUserId = ?").append(paramIndex++);
            params.add(query.getUserId());
        }

        // Handle mode-specific filtering
        ConversationListMode mode =
                query.getMode() != null ? query.getMode() : ConversationListMode.LATEST_FORK;
        if (mode == ConversationListMode.ROOTS) {
            jpql.append(" AND c.forkedAtEntryId IS NULL AND c.forkedAtConversationId IS NULL");
        }

        if (query.isOnlyDeleted()) {
            jpql.append(" AND c.deletedAt IS NOT NULL");
            if (query.getDeletedAfter() != null) {
                jpql.append(" AND c.deletedAt >= ?").append(paramIndex++);
                params.add(query.getDeletedAfter());
            }
            if (query.getDeletedBefore() != null) {
                jpql.append(" AND c.deletedAt < ?").append(paramIndex++);
                params.add(query.getDeletedBefore());
            }
        } else if (!query.isIncludeDeleted()) {
            jpql.append(" AND c.deletedAt IS NULL AND c.conversationGroup.deletedAt IS NULL");
        } else {
            // includeDeleted=true: show all, but still filter by date if provided
            if (query.getDeletedAfter() != null) {
                jpql.append(" AND (c.deletedAt IS NULL OR c.deletedAt >= ?").append(paramIndex++);
                params.add(query.getDeletedAfter());
                jpql.append(")");
            }
            if (query.getDeletedBefore() != null) {
                jpql.append(" AND (c.deletedAt IS NULL OR c.deletedAt < ?").append(paramIndex++);
                params.add(query.getDeletedBefore());
                jpql.append(")");
            }
        }

        jpql.append(" ORDER BY c.updatedAt DESC");

        int limit = query.getLimit() > 0 ? query.getLimit() : 100;
        List<ConversationEntity> entities =
                conversationRepository
                        .find(jpql.toString(), params.toArray())
                        .page(0, limit)
                        .list();

        // For LATEST_FORK mode, filter to only the most recently updated conversation per group
        if (mode == ConversationListMode.LATEST_FORK) {
            Map<UUID, ConversationEntity> latestByGroup = new HashMap<>();
            for (ConversationEntity candidate : entities) {
                UUID groupId = candidate.getConversationGroup().getId();
                ConversationEntity current = latestByGroup.get(groupId);
                if (current == null || candidate.getUpdatedAt().isAfter(current.getUpdatedAt())) {
                    latestByGroup.put(groupId, candidate);
                }
            }
            return latestByGroup.values().stream()
                    .map(entity -> toConversationSummaryDto(entity, AccessLevel.OWNER, null))
                    .sorted(Comparator.comparing(ConversationSummaryDto::getUpdatedAt).reversed())
                    .limit(limit)
                    .collect(Collectors.toList());
        }

        return entities.stream()
                .map(entity -> toConversationSummaryDto(entity, AccessLevel.OWNER, null))
                .collect(Collectors.toList());
    }

    @Override
    public Optional<ConversationDto> adminGetConversation(String conversationId) {
        UUID id = UUID.fromString(conversationId);
        ConversationEntity entity = conversationRepository.findByIdOptional(id).orElse(null);
        if (entity == null) {
            return Optional.empty();
        }
        return Optional.of(toConversationDto(entity, AccessLevel.OWNER, null));
    }

    @Override
    @Transactional
    public void adminDeleteConversation(String conversationId) {
        UUID id = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(id).orElse(null);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        UUID groupId = conversation.getConversationGroup().getId();
        softDeleteConversationGroup(groupId, "admin");
    }

    @Override
    @Transactional
    public void adminRestoreConversation(String conversationId) {
        UUID id = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(id).orElse(null);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        UUID groupId = conversation.getConversationGroup().getId();
        ConversationGroupEntity group =
                conversationGroupRepository.findByIdOptional(groupId).orElse(null);
        if (group == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        if (group.getDeletedAt() == null) {
            throw new ResourceConflictException(
                    "conversation", conversationId, "Conversation is not deleted");
        }

        // Restore conversation group
        conversationGroupRepository.update("deletedAt = NULL WHERE id = ?1", groupId);

        // Restore all conversations in the group
        conversationRepository.update("deletedAt = NULL WHERE conversationGroup.id = ?1", groupId);

        // Note: Memberships are hard-deleted when a conversation is deleted,
        // so they cannot be restored. The owner must re-share with other users.
    }

    @Override
    public PagedEntries adminGetEntries(String conversationId, AdminMessageQuery query) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }

        int limit = query.getLimit() > 0 ? query.getLimit() : 50;
        String afterEntryId = query.getAfterEntryId();
        Channel channel = query.getChannel();
        boolean allForks = query.isAllForks();

        LOG.infof(
                "adminGetEntries: conversationId=%s, forkedAtConversationId=%s,"
                        + " forkedAtEntryId=%s, channel=%s, limit=%d, afterEntryId=%s, allForks=%s",
                conversationId,
                conversation.getForkedAtConversationId(),
                conversation.getForkedAtEntryId(),
                channel,
                limit,
                afterEntryId,
                allForks);

        // When allForks=true, bypass fork ancestry and return all entries in the group
        if (allForks) {
            UUID groupId = conversation.getConversationGroup().getId();
            List<EntryEntity> entries =
                    entryRepository.listByConversationGroup(groupId, channel, null);
            List<EntryEntity> paginatedEntries = applyPagination(entries, afterEntryId, limit);
            LOG.infof(
                    "adminGetEntries: found %d entries for conversationId=%s, allForks=true",
                    paginatedEntries.size(), conversationId);
            return buildPagedEntries(conversationId, paginatedEntries, limit);
        }

        // Use fork-aware retrieval (clientId is null for admin API)
        PagedEntries result =
                getEntriesWithForkSupport(conversation, afterEntryId, limit, channel, null);
        LOG.infof(
                "adminGetEntries: found %d entries for conversationId=%s (fork-aware)",
                result.getEntries().size(), conversationId);
        return result;
    }

    @Override
    public List<ConversationMembershipDto> adminListMemberships(
            String conversationId, String afterCursor, int limit) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        UUID groupId = conversation.getConversationGroup().getId();

        StringBuilder jpql =
                new StringBuilder(
                        "SELECT cm FROM ConversationMembershipEntity cm"
                                + " WHERE cm.id.conversationGroupId = :groupId");
        if (afterCursor != null && !afterCursor.isBlank()) {
            jpql.append(" AND cm.id.userId > :afterCursor");
        }
        jpql.append(" ORDER BY cm.id.userId ASC");

        var query =
                entityManager
                        .createQuery(jpql.toString(), ConversationMembershipEntity.class)
                        .setParameter("groupId", groupId)
                        .setMaxResults(limit);
        if (afterCursor != null && !afterCursor.isBlank()) {
            query.setParameter("afterCursor", afterCursor);
        }

        return query.getResultList().stream()
                .map(this::toMembershipDto)
                .collect(Collectors.toList());
    }

    @Override
    public List<ConversationForkSummaryDto> adminListForks(
            String conversationId, String afterCursor, int limit) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        UUID groupId = conversation.getConversationGroup().getId();

        // Admin can see all forks including deleted ones
        StringBuilder jpql =
                new StringBuilder(
                        "FROM ConversationEntity c WHERE c.conversationGroup.id = :groupId");
        if (afterCursor != null && !afterCursor.isBlank()) {
            jpql.append(" AND c.id > :afterCursor");
        }
        jpql.append(" ORDER BY c.id ASC");

        var query =
                entityManager
                        .createQuery(jpql.toString(), ConversationEntity.class)
                        .setParameter("groupId", groupId)
                        .setMaxResults(limit);
        if (afterCursor != null && !afterCursor.isBlank()) {
            query.setParameter("afterCursor", UUID.fromString(afterCursor));
        }

        List<ConversationEntity> candidates = query.getResultList();
        List<ConversationForkSummaryDto> results = new ArrayList<>();
        for (ConversationEntity candidate : candidates) {
            ConversationForkSummaryDto dto = new ConversationForkSummaryDto();
            dto.setConversationId(candidate.getId().toString());
            dto.setConversationGroupId(groupId.toString());
            dto.setForkedAtEntryId(
                    candidate.getForkedAtEntryId() != null
                            ? candidate.getForkedAtEntryId().toString()
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
    public SearchResultsDto adminSearchEntries(AdminSearchQuery query) {
        SearchResultsDto result = new SearchResultsDto();
        result.setResults(Collections.emptyList());
        result.setAfterCursor(null);

        if (query.getQuery() == null || query.getQuery().isBlank()) {
            return result;
        }

        StringBuilder jpql = new StringBuilder("FROM ConversationEntity c WHERE 1=1");
        List<Object> params = new ArrayList<>();
        int paramIndex = 1;

        if (query.getUserId() != null && !query.getUserId().isBlank()) {
            jpql.append(" AND c.ownerUserId = ?").append(paramIndex++);
            params.add(query.getUserId());
        }

        if (!query.isIncludeDeleted()) {
            jpql.append(" AND c.deletedAt IS NULL AND c.conversationGroup.deletedAt IS NULL");
        }

        List<ConversationEntity> conversations =
                conversationRepository.find(jpql.toString(), params.toArray()).list();
        Map<UUID, ConversationEntity> conversationMap =
                conversations.stream().collect(Collectors.toMap(ConversationEntity::getId, c -> c));
        Set<UUID> conversationIds = conversationMap.keySet();

        if (conversationIds.isEmpty()) {
            return result;
        }

        String searchQuery = query.getQuery().toLowerCase();
        int limit = query.getLimit() != null ? query.getLimit() : 20;

        // Parse after cursor if present
        UUID afterEntryId = null;
        if (query.getAfterCursor() != null && !query.getAfterCursor().isBlank()) {
            try {
                afterEntryId = UUID.fromString(query.getAfterCursor());
            } catch (IllegalArgumentException e) {
                // Invalid cursor, ignore
            }
        }

        List<EntryEntity> candidates =
                entryRepository
                        .find(
                                "conversation.id in ?1 order by createdAt desc, id desc",
                                conversationIds)
                        .list();

        // Skip entries until we find the cursor
        final UUID finalAfterEntryId = afterEntryId;
        boolean skipMode = afterEntryId != null;
        List<SearchResultDto> resultsList = new ArrayList<>();

        for (EntryEntity m : candidates) {
            if (skipMode) {
                if (m.getId().equals(finalAfterEntryId)) {
                    skipMode = false;
                }
                continue;
            }

            List<Object> content = decryptContent(m.getContent());
            if (content == null || content.isEmpty()) {
                continue;
            }
            String text = extractSearchText(content);
            if (text == null || !text.toLowerCase().contains(searchQuery)) {
                continue;
            }

            SearchResultDto dto = new SearchResultDto();
            dto.setConversationId(m.getConversation().getId().toString());
            ConversationEntity conv = conversationMap.get(m.getConversation().getId());
            dto.setConversationTitle(conv != null ? decryptTitle(conv.getTitle()) : null);
            dto.setEntryId(m.getId().toString());
            dto.setEntry(toEntryDto(m, content));
            dto.setScore(1.0);
            dto.setHighlights(null);
            resultsList.add(dto);

            // Fetch one extra to determine if there's a next page
            if (resultsList.size() > limit) {
                break;
            }
        }

        // Determine next cursor
        if (resultsList.size() > limit) {
            SearchResultDto last = resultsList.get(limit - 1);
            result.setAfterCursor(last.getEntry().getId());
            resultsList = resultsList.subList(0, limit);
        }

        result.setResults(resultsList);
        return result;
    }

    @Override
    @Transactional
    public List<String> findEvictableGroupIds(OffsetDateTime cutoff, int limit) {
        @SuppressWarnings("unchecked")
        List<UUID> ids =
                entityManager
                        .createNativeQuery(
                                "SELECT id FROM conversation_groups "
                                        + "WHERE deleted_at IS NOT NULL AND deleted_at < :cutoff "
                                        + "ORDER BY deleted_at "
                                        + "LIMIT :limit "
                                        + "FOR UPDATE SKIP LOCKED",
                                UUID.class)
                        .setParameter("cutoff", cutoff)
                        .setParameter("limit", limit)
                        .getResultList();
        return ids.stream().map(UUID::toString).collect(Collectors.toList());
    }

    @Override
    @Transactional
    public long countEvictableGroups(OffsetDateTime cutoff) {
        return ((Number)
                        entityManager
                                .createNativeQuery(
                                        "SELECT COUNT(*) FROM conversation_groups WHERE deleted_at"
                                                + " IS NOT NULL AND deleted_at < :cutoff")
                                .setParameter("cutoff", cutoff)
                                .getSingleResult())
                .longValue();
    }

    @Override
    @Transactional
    public void hardDeleteConversationGroups(List<String> groupIds) {
        if (groupIds.isEmpty()) {
            return;
        }

        // 1. Create tasks for vector store cleanup
        for (String groupId : groupIds) {
            taskRepository.createTask(
                    "vector_store_delete", Map.of("conversationGroupId", groupId));
        }

        // 2. Delete FileStore blobs for attachments in these groups (with ref-count safety)
        try {
            UUID[] groupUuids = groupIds.stream().map(UUID::fromString).toArray(UUID[]::new);
            @SuppressWarnings("unchecked")
            List<String> entryIds =
                    entityManager
                            .createNativeQuery(
                                    "SELECT id::text FROM entries WHERE conversation_group_id ="
                                            + " ANY(:ids)")
                            .setParameter("ids", groupUuids)
                            .getResultList();
            if (!entryIds.isEmpty()) {
                var attachmentStore = attachmentStoreSelector.getStore();
                var attachments = attachmentStore.findByEntryIds(entryIds);
                attachmentDeletionService.deleteAttachments(attachments);
            }
        } catch (Exception e) {
            LOG.warnf(
                    "Failed to cleanup attachment blobs for groups %s: %s",
                    groupIds, e.getMessage());
        }

        // 3. Single DELETE statement - ON DELETE CASCADE handles all children
        UUID[] uuids = groupIds.stream().map(UUID::fromString).toArray(UUID[]::new);
        entityManager
                .createNativeQuery("DELETE FROM conversation_groups WHERE id = ANY(:ids)")
                .setParameter("ids", uuids)
                .executeUpdate();
    }
}
