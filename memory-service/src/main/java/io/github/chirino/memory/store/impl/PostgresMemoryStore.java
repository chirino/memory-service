package io.github.chirino.memory.store.impl;

import com.fasterxml.jackson.databind.ObjectMapper;
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
import io.github.chirino.memory.api.dto.IndexConversationsResponse;
import io.github.chirino.memory.api.dto.IndexEntryRequest;
import io.github.chirino.memory.api.dto.OwnershipTransferDto;
import io.github.chirino.memory.api.dto.PagedEntries;
import io.github.chirino.memory.api.dto.SearchEntriesRequest;
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
import io.github.chirino.memory.config.VectorStoreSelector;
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
import io.github.chirino.memory.store.EpochKey;
import io.github.chirino.memory.store.MemoryEpochFilter;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceConflictException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.github.chirino.memory.vector.EmbeddingService;
import io.github.chirino.memory.vector.VectorStore;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
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

    @Inject ConversationRepository conversationRepository;

    @Inject ConversationGroupRepository conversationGroupRepository;

    @Inject ConversationMembershipRepository membershipRepository;

    @Inject EntryRepository entryRepository;

    @Inject ConversationOwnershipTransferRepository ownershipTransferRepository;

    @Inject DataEncryptionService dataEncryptionService;

    @Inject ObjectMapper objectMapper;

    @Inject VectorStoreSelector vectorStoreSelector;

    @Inject EmbeddingService embeddingService;

    @Inject EntityManager entityManager;

    @Inject TaskRepository taskRepository;

    @Inject MemoryEntriesCacheSelector memoryCacheSelector;

    @Inject io.github.chirino.memory.security.MembershipAuditLogger membershipAuditLogger;

    private MemoryEntriesCache memoryEntriesCache;

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
    @Transactional
    public EntryDto appendUserEntry(
            String userId, String conversationId, CreateUserEntryRequest request) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findActiveById(cid).orElse(null);

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (conversation == null) {
            conversation = new ConversationEntity();
            conversation.setId(cid);
            conversation.setOwnerUserId(userId);
            String inferredTitle = inferTitleFromUserEntry(request);
            conversation.setTitle(encryptTitle(inferredTitle));
            conversation.setMetadata(Collections.emptyMap());
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
        } else {
            UUID groupId = conversation.getConversationGroup().getId();
            ensureHasAccess(groupId, userId, AccessLevel.WRITER);
        }

        EntryEntity entry = new EntryEntity();
        entry.setConversation(conversation);
        entry.setUserId(userId);
        entry.setChannel(Channel.HISTORY);
        entry.setEpoch(null);
        entry.setContentType("history");
        entry.setContent(encryptContent(toHistoryContent(request.getContent(), "USER")));
        entry.setConversationGroupId(conversation.getConversationGroup().getId());
        OffsetDateTime createdAt = OffsetDateTime.now();
        entry.setCreatedAt(createdAt);
        entryRepository.persist(entry);
        conversationRepository.update(
                "updatedAt = ?1 where id = ?2", createdAt, conversation.getId());
        return toEntryDto(entry);
    }

    @Override
    public List<ConversationMembershipDto> listMemberships(String userId, String conversationId) {
        UUID cid = UUID.fromString(conversationId);
        UUID groupId = resolveGroupId(cid);
        // Any member can view the membership list
        ensureHasAccess(groupId, userId, AccessLevel.READER);
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
                        .findActiveById(cid)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        ConversationGroupEntity conversationGroup = conversation.getConversationGroup();
        ConversationMembershipEntity membership =
                membershipRepository.createMembership(
                        conversationGroup, request.getUserId(), request.getAccessLevel());

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

    @Override
    @Transactional
    public ConversationDto forkConversationAtEntry(
            String userId, String conversationId, String entryId, ForkFromEntryRequest request) {
        // Create a new fork conversation without copying entries.
        UUID originalId = UUID.fromString(conversationId);
        ConversationEntity originalEntity =
                conversationRepository
                        .findActiveById(originalId)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        UUID groupId = originalEntity.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.WRITER);

        EntryEntity target =
                entryRepository
                        .findByIdOptional(UUID.fromString(entryId))
                        .orElseThrow(() -> new ResourceNotFoundException("entry", entryId));
        if (target.getConversation() == null
                || !originalId.equals(target.getConversation().getId())) {
            throw new ResourceNotFoundException("entry", entryId);
        }
        if (target.getChannel() != Channel.HISTORY) {
            throw new AccessDeniedException("Forking is only allowed for history entries");
        }

        EntryEntity previous =
                entryRepository
                        .find(
                                "from EntryEntity m where m.conversation.id = ?1 and m.channel = ?2"
                                    + " and (m.createdAt < ?3 or (m.createdAt = ?3 and m.id < ?4))"
                                    + " order by m.createdAt desc, m.id desc",
                                originalId,
                                Channel.HISTORY,
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
        forkEntity.setForkedAtEntryId(previousId);
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
                        .findActiveById(cid)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));
        UUID groupId = conversation.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.READER);

        List<ConversationEntity> candidates =
                conversationRepository
                        .find(
                                "conversationGroup.id = ?1 AND deletedAt IS NULL AND"
                                        + " conversationGroup.deletedAt IS NULL"
                                        + " ORDER BY forkedAtEntryId NULLS FIRST, updatedAt DESC",
                                groupId)
                        .list();
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
    public List<OwnershipTransferDto> listPendingTransfers(String userId, String role) {
        List<ConversationOwnershipTransferEntity> transfers;
        switch (role) {
            case "sender":
                transfers = ownershipTransferRepository.listByFromUser(userId);
                break;
            case "recipient":
                transfers = ownershipTransferRepository.listByToUser(userId);
                break;
            default: // "all"
                transfers = ownershipTransferRepository.listByUser(userId);
        }
        return transfers.stream().map(this::toOwnershipTransferDto).collect(Collectors.toList());
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
            String clientId) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findActiveById(cid).orElse(null);
        if (conversation == null) {
            PagedEntries empty = new PagedEntries();
            empty.setConversationId(conversationId);
            empty.setEntries(Collections.emptyList());
            empty.setNextCursor(null);
            return empty;
        }
        UUID groupId = conversation.getConversationGroup().getId();
        ensureHasAccess(groupId, userId, AccessLevel.READER);
        List<EntryEntity> entries;
        if (channel == Channel.MEMORY) {
            entries = fetchMemoryEntries(cid, afterEntryId, limit, epochFilter, clientId);
        } else {
            entries = entryRepository.listByChannel(cid, afterEntryId, limit, channel, clientId);
        }
        PagedEntries page = new PagedEntries();
        page.setConversationId(conversationId);
        List<EntryDto> dtos = entries.stream().map(this::toEntryDto).toList();
        page.setEntries(dtos);
        String nextCursor =
                dtos.size() == limit && !dtos.isEmpty() ? dtos.get(dtos.size() - 1).getId() : null;
        page.setNextCursor(nextCursor);
        return page;
    }

    private List<EntryEntity> fetchMemoryEntries(
            UUID conversationId,
            String afterEntryId,
            int limit,
            MemoryEpochFilter epochFilter,
            String clientId) {
        if (clientId == null || clientId.isBlank()) {
            return Collections.emptyList();
        }
        MemoryEpochFilter filter = epochFilter != null ? epochFilter : MemoryEpochFilter.latest();
        return switch (filter.getMode()) {
            case ALL ->
                    entryRepository.listByChannel(
                            conversationId, afterEntryId, limit, Channel.MEMORY, clientId);
            case LATEST -> fetchLatestMemoryEntries(conversationId, afterEntryId, limit, clientId);
            case EPOCH ->
                    entryRepository.listMemoryEntriesByEpoch(
                            conversationId, afterEntryId, limit, filter.getEpoch(), clientId);
        };
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
        List<EntryEntity> entries =
                entryRepository.listMemoryEntriesAtLatestEpoch(conversationId, clientId);
        if (!entries.isEmpty()) {
            CachedMemoryEntries cached = toCachedMemoryEntries(entries);
            memoryEntriesCache.set(conversationId, clientId, cached);
        } else {
            // No entries at latest epoch - remove stale cache entry
            memoryEntriesCache.remove(conversationId, clientId);
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

    @Override
    @Transactional
    public List<EntryDto> appendAgentEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findActiveById(cid).orElse(null);

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (conversation == null) {
            conversation = new ConversationEntity();
            conversation.setId(cid);
            conversation.setOwnerUserId(userId);
            String inferredTitle = inferTitleFromEntries(entries);
            conversation.setTitle(encryptTitle(inferredTitle));
            conversation.setMetadata(Collections.emptyMap());
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
        } else {
            UUID groupId = conversation.getConversationGroup().getId();
            ensureHasAccess(groupId, userId, AccessLevel.WRITER);
        }

        // Make conversation effectively final for lambda
        OffsetDateTime latestHistoryTimestamp = null;
        List<EntryDto> created = new ArrayList<>(entries.size());
        for (CreateEntryRequest req : entries) {
            EntryEntity entity = new EntryEntity();
            entity.setConversation(conversation);
            entity.setUserId(req.getUserId());
            entity.setClientId(clientId);
            if (req.getChannel() != null) {
                entity.setChannel(
                        io.github.chirino.memory.model.Channel.fromString(
                                req.getChannel().value()));
            } else {
                entity.setChannel(io.github.chirino.memory.model.Channel.MEMORY);
            }
            entity.setEpoch(req.getEpoch());
            entity.setContentType(req.getContentType() != null ? req.getContentType() : "message");
            entity.setContent(encryptContent(req.getContent()));
            entity.setIndexedContent(req.getIndexedContent());
            entity.setConversationGroupId(conversation.getConversationGroup().getId());
            OffsetDateTime createdAt = OffsetDateTime.now();
            entity.setCreatedAt(createdAt);
            entryRepository.persist(entity);
            if (entity.getChannel() == Channel.HISTORY) {
                latestHistoryTimestamp = createdAt;
            }
            created.add(toEntryDto(entity));
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
        // Combined query: finds max epoch and lists entries in single round-trip
        List<EntryEntity> latestEpochEntityList =
                entryRepository.listMemoryEntriesAtLatestEpoch(cid, clientId);
        List<EntryDto> latestEpochEntries =
                latestEpochEntityList.stream().map(this::toEntryDto).collect(Collectors.toList());
        // Extract epoch from entries (all entries in the list share the same epoch)
        Long latestEpoch =
                latestEpochEntityList.isEmpty() ? null : latestEpochEntityList.get(0).getEpoch();

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
            CreateEntryRequest toAppend =
                    MemorySyncHelper.withEpochAndContent(entry, nextEpoch, incomingContent);
            List<EntryDto> appended =
                    appendAgentEntries(userId, conversationId, List.of(toAppend), clientId);
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
            result.setNoOp(true);
            return result;
        }

        // If incoming is a prefix extension of existing (only adding more content), append delta
        if (MemorySyncHelper.isContentPrefix(existingContent, incomingContent)) {
            List<Object> deltaContent =
                    MemorySyncHelper.extractDelta(existingContent, incomingContent);
            if (deltaContent.isEmpty()) {
                // No new content - this is a no-op
                result.setNoOp(true);
                return result;
            }
            CreateEntryRequest deltaEntry =
                    MemorySyncHelper.withEpochAndContent(entry, latestEpoch, deltaContent);
            List<EntryDto> appended =
                    appendAgentEntries(userId, conversationId, List.of(deltaEntry), clientId);
            // Update cache with appended entry
            updateCacheWithLatestEntries(cid, clientId);
            result.setEntry(appended.isEmpty() ? null : appended.get(0));
            result.setEpochIncremented(false);
            result.setNoOp(false);
            return result;
        }

        // Content diverged - create new epoch with all incoming content
        Long nextEpoch = MemorySyncHelper.nextEpoch(latestEpoch);
        CreateEntryRequest toAppend =
                MemorySyncHelper.withEpochAndContent(entry, nextEpoch, incomingContent);
        List<EntryDto> appended =
                appendAgentEntries(userId, conversationId, List.of(toAppend), clientId);
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
        }

        return new IndexConversationsResponse(indexed);
    }

    private void vectorizeEntry(EntryEntity entry) {
        String text = entry.getIndexedContent();
        if (text == null || text.isBlank()) {
            return;
        }
        VectorStore store = vectorStoreSelector.getVectorStore();
        float[] embedding = embeddingService.embed(text);
        if (embedding == null || embedding.length == 0) {
            return;
        }
        store.upsertTranscriptEmbedding(
                entry.getConversation().getId().toString(), entry.getId().toString(), embedding);
    }

    @Override
    public UnindexedEntriesResponse listUnindexedEntries(int limit, String cursor) {
        // Query entries where channel = HISTORY AND indexed_content IS NULL
        // Order by created_at for consistent pagination

        StringBuilder queryBuilder =
                new StringBuilder(
                        "SELECT e FROM EntryEntity e WHERE e.channel = :channel AND"
                                + " e.indexedContent IS NULL");

        // Handle cursor-based pagination
        if (cursor != null && !cursor.isBlank()) {
            try {
                // Cursor is base64 encoded createdAt timestamp
                String decoded =
                        new String(
                                java.util.Base64.getDecoder().decode(cursor),
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

        if (cursor != null && !cursor.isBlank()) {
            try {
                String decoded =
                        new String(
                                java.util.Base64.getDecoder().decode(cursor),
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
    @Transactional
    public void setIndexedAt(String entryId, OffsetDateTime indexedAt) {
        UUID id = UUID.fromString(entryId);
        EntryEntity entry = entryRepository.findById(id);
        if (entry != null) {
            entry.setIndexedAt(indexedAt);
            entryRepository.persist(entry);
        }
    }

    @Override
    public SearchResultsDto searchEntries(String userId, SearchEntriesRequest request) {
        SearchResultsDto result = new SearchResultsDto();
        result.setResults(Collections.emptyList());
        result.setNextCursor(null);

        if (request.getQuery() == null || request.getQuery().isBlank()) {
            return result;
        }

        List<ConversationMembershipEntity> memberships =
                membershipRepository.listForUser(userId, Integer.MAX_VALUE);
        if (memberships.isEmpty()) {
            return result;
        }

        Set<UUID> groupIds =
                memberships.stream()
                        .map(m -> m.getId().getConversationGroupId())
                        .collect(Collectors.toSet());
        if (groupIds.isEmpty()) {
            return result;
        }
        List<ConversationEntity> conversations =
                conversationRepository.find("conversationGroup.id in ?1", groupIds).list();
        Map<UUID, ConversationEntity> conversationMap =
                conversations.stream().collect(Collectors.toMap(ConversationEntity::getId, c -> c));
        Set<UUID> userConversationIds = conversationMap.keySet();

        if (userConversationIds.isEmpty()) {
            return result;
        }

        String query = request.getQuery().toLowerCase();
        int limit = request.getLimit() != null ? request.getLimit() : 20;

        // Parse after cursor if present
        UUID afterEntryId = null;
        if (request.getAfter() != null && !request.getAfter().isBlank()) {
            try {
                afterEntryId = UUID.fromString(request.getAfter());
            } catch (IllegalArgumentException e) {
                // Invalid cursor, ignore
            }
        }

        List<EntryEntity> candidates =
                entryRepository
                        .find(
                                "conversation.id in ?1 order by createdAt desc, id desc",
                                userConversationIds)
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
            String indexedContent = m.getIndexedContent();
            boolean matchesContent = text != null && text.toLowerCase().contains(query);
            boolean matchesIndexed =
                    indexedContent != null && indexedContent.toLowerCase().contains(query);
            if (!matchesContent && !matchesIndexed) {
                continue;
            }
            // Prefer indexed content for highlights if it matched
            String highlightSource = matchesIndexed ? indexedContent : text;

            SearchResultDto dto = new SearchResultDto();
            dto.setConversationId(m.getConversation().getId().toString());
            ConversationEntity conv = conversationMap.get(m.getConversation().getId());
            dto.setConversationTitle(conv != null ? decryptTitle(conv.getTitle()) : null);
            dto.setEntryId(m.getId().toString());
            dto.setEntry(toEntryDto(m, content));
            dto.setScore(1.0);
            dto.setHighlights(extractHighlight(highlightSource, query));
            resultsList.add(dto);

            // Fetch one extra to determine if there's a next page
            if (resultsList.size() > limit) {
                break;
            }
        }

        // Determine next cursor
        if (resultsList.size() > limit) {
            SearchResultDto last = resultsList.get(limit - 1);
            result.setNextCursor(last.getEntry().getId());
            resultsList = resultsList.subList(0, limit);
        }

        result.setResults(resultsList);
        return result;
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
        VectorStore store = vectorStoreSelector.getVectorStore();
        return store != null && store.isEnabled() && embeddingService.isEnabled();
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

    /**
     * Extracts a highlight snippet showing the query match in context.
     *
     * @param text  The full text to extract from
     * @param query The search query (lowercase)
     * @return A snippet with context around the match, or null if not found
     */
    private String extractHighlight(String text, String query) {
        if (text == null || query == null) {
            return null;
        }
        String lowerText = text.toLowerCase();
        int matchIndex = lowerText.indexOf(query);
        if (matchIndex < 0) {
            return null;
        }

        // Extract a snippet with context around the match
        int contextChars = 50;
        int start = Math.max(0, matchIndex - contextChars);
        int end = Math.min(text.length(), matchIndex + query.length() + contextChars);

        StringBuilder snippet = new StringBuilder();
        if (start > 0) {
            snippet.append("...");
        }
        snippet.append(text, start, end);
        if (end < text.length()) {
            snippet.append("...");
        }

        return snippet.toString().trim();
    }

    private String inferTitleFromUserEntry(CreateUserEntryRequest request) {
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
     * @param text The message text
     * @param role Either "USER" or "AI"
     * @return Content array with a single object containing text and role fields
     */
    private List<Object> toHistoryContent(String text, String role) {
        if (text == null) {
            return Collections.emptyList();
        }
        return List.of(Map.of("text", text, "role", role));
    }

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

        StringBuilder jpql = new StringBuilder("FROM EntryEntity m WHERE m.conversation.id = ?1");
        List<Object> params = new ArrayList<>();
        params.add(cid);
        int paramIndex = 2;

        if (query.getChannel() != null) {
            jpql.append(" AND m.channel = ?").append(paramIndex++);
            params.add(query.getChannel());
        }

        if (query.getAfterEntryId() != null && !query.getAfterEntryId().isBlank()) {
            UUID afterId = UUID.fromString(query.getAfterEntryId());
            jpql.append(
                            " AND (m.createdAt > (SELECT m2.createdAt FROM EntryEntity m2 WHERE"
                                    + " m2.id = ?")
                    .append(paramIndex)
                    .append(
                            ") OR (m.createdAt = (SELECT m2.createdAt FROM EntryEntity m2 WHERE"
                                    + " m2.id = ?")
                    .append(paramIndex)
                    .append(") AND m.id > ?")
                    .append(paramIndex)
                    .append("))");
            params.add(afterId);
            params.add(afterId);
            params.add(afterId);
            paramIndex += 3;
        }

        jpql.append(" ORDER BY m.createdAt ASC, m.id ASC");

        List<EntryEntity> entities =
                entryRepository
                        .find(jpql.toString(), params.toArray())
                        .page(0, query.getLimit() > 0 ? query.getLimit() : 50)
                        .list();

        List<EntryDto> entryDtos =
                entities.stream().map(this::toEntryDto).collect(Collectors.toList());

        String nextCursor = null;
        if (entryDtos.size() == query.getLimit()) {
            EntryEntity last = entities.get(entities.size() - 1);
            nextCursor = last.getId().toString();
        }

        PagedEntries result = new PagedEntries();
        result.setConversationId(conversationId);
        result.setEntries(entryDtos);
        result.setNextCursor(nextCursor);
        return result;
    }

    @Override
    public List<ConversationMembershipDto> adminListMemberships(String conversationId) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        UUID groupId = conversation.getConversationGroup().getId();
        List<ConversationMembershipEntity> memberships =
                membershipRepository.find("id.conversationGroupId", groupId).list();
        return memberships.stream().map(this::toMembershipDto).collect(Collectors.toList());
    }

    @Override
    public List<ConversationForkSummaryDto> adminListForks(String conversationId) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        UUID groupId = conversation.getConversationGroup().getId();

        // Admin can see all forks including deleted ones
        List<ConversationEntity> candidates =
                conversationRepository
                        .find(
                                "conversationGroup.id = ?1"
                                        + " ORDER BY forkedAtEntryId NULLS FIRST, updatedAt DESC",
                                groupId)
                        .list();
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
        result.setNextCursor(null);

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
        if (query.getAfter() != null && !query.getAfter().isBlank()) {
            try {
                afterEntryId = UUID.fromString(query.getAfter());
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
            result.setNextCursor(last.getEntry().getId());
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

        // 2. Single DELETE statement - ON DELETE CASCADE handles all children
        UUID[] uuids = groupIds.stream().map(UUID::fromString).toArray(UUID[]::new);
        entityManager
                .createNativeQuery("DELETE FROM conversation_groups WHERE id = ANY(:ids)")
                .setParameter("ids", uuids)
                .executeUpdate();
    }

    @Override
    @Transactional
    public List<EpochKey> findEvictableEpochs(OffsetDateTime cutoff, int limit) {
        @SuppressWarnings("unchecked")
        List<Object[]> results =
                entityManager
                        .createNativeQuery(
                                """
                                WITH epoch_stats AS (
                                    SELECT
                                        conversation_id,
                                        client_id,
                                        epoch,
                                        MAX(created_at) as last_updated,
                                        MAX(epoch) OVER (PARTITION BY conversation_id, client_id) as latest_epoch
                                    FROM entries
                                    WHERE channel = 'MEMORY'
                                      AND epoch IS NOT NULL
                                    GROUP BY conversation_id, client_id, epoch
                                )
                                SELECT conversation_id, client_id, epoch
                                FROM epoch_stats
                                WHERE epoch < latest_epoch
                                  AND last_updated < :cutoff
                                LIMIT :limit
                                FOR UPDATE SKIP LOCKED
                                """)
                        .setParameter("cutoff", cutoff)
                        .setParameter("limit", limit)
                        .getResultList();

        return results.stream()
                .map(
                        row ->
                                new EpochKey(
                                        (UUID) row[0],
                                        (String) row[1],
                                        ((Number) row[2]).longValue()))
                .toList();
    }

    @Override
    @Transactional
    public long countEvictableEpochEntries(OffsetDateTime cutoff) {
        return ((Number)
                        entityManager
                                .createNativeQuery(
                                        """
                                        WITH evictable_epochs AS (
                                            SELECT
                                                conversation_id,
                                                client_id,
                                                epoch,
                                                MAX(created_at) as last_updated,
                                                MAX(epoch) OVER (PARTITION BY conversation_id, client_id) as latest_epoch
                                            FROM entries
                                            WHERE channel = 'MEMORY'
                                              AND epoch IS NOT NULL
                                            GROUP BY conversation_id, client_id, epoch
                                        )
                                        SELECT COUNT(*) FROM entries e
                                        JOIN evictable_epochs ev
                                          ON e.conversation_id = ev.conversation_id
                                         AND e.client_id = ev.client_id
                                         AND e.epoch = ev.epoch
                                        WHERE ev.epoch < ev.latest_epoch
                                          AND ev.last_updated < :cutoff
                                          AND e.channel = 'MEMORY'
                                        """)
                                .setParameter("cutoff", cutoff)
                                .getSingleResult())
                .longValue();
    }

    @Override
    @Transactional
    public int deleteEntriesForEpochs(List<EpochKey> epochs) {
        if (epochs.isEmpty()) {
            return 0;
        }

        // 1. Get entry IDs for vector store cleanup
        List<UUID> entryIds = findEntryIdsForEpochs(epochs);

        // 2. Queue vector store cleanup tasks
        for (UUID entryId : entryIds) {
            taskRepository.createTask(
                    "vector_store_delete_entry", Map.of("entryId", entryId.toString()));
        }

        // 3. Delete entries - build VALUES clause dynamically
        // Use CAST(:param AS uuid) instead of :param::uuid to avoid JPA parsing issues
        StringBuilder values = new StringBuilder();
        for (int i = 0; i < epochs.size(); i++) {
            if (i > 0) {
                values.append(", ");
            }
            values.append("(CAST(:conv")
                    .append(i)
                    .append(" AS uuid), :client")
                    .append(i)
                    .append(", :epoch")
                    .append(i)
                    .append(")");
        }

        var query =
                entityManager.createNativeQuery(
                        String.format(
                                """
                                DELETE FROM entries
                                WHERE (conversation_id, client_id, epoch) IN (VALUES %s)
                                  AND channel = 'MEMORY'
                                """,
                                values.toString()));

        for (int i = 0; i < epochs.size(); i++) {
            EpochKey key = epochs.get(i);
            query.setParameter("conv" + i, key.conversationId().toString());
            query.setParameter("client" + i, key.clientId());
            query.setParameter("epoch" + i, key.epoch());
        }

        return query.executeUpdate();
    }

    private List<UUID> findEntryIdsForEpochs(List<EpochKey> epochs) {
        if (epochs.isEmpty()) {
            return List.of();
        }

        // Build VALUES clause dynamically
        // Use CAST(:param AS uuid) instead of :param::uuid to avoid JPA parsing issues
        StringBuilder values = new StringBuilder();
        for (int i = 0; i < epochs.size(); i++) {
            if (i > 0) {
                values.append(", ");
            }
            values.append("(CAST(:conv")
                    .append(i)
                    .append(" AS uuid), :client")
                    .append(i)
                    .append(", :epoch")
                    .append(i)
                    .append(")");
        }

        var query =
                entityManager.createNativeQuery(
                        String.format(
                                """
                                SELECT id FROM entries
                                WHERE (conversation_id, client_id, epoch) IN (VALUES %s)
                                  AND channel = 'MEMORY'
                                """,
                                values.toString()),
                        UUID.class);

        for (int i = 0; i < epochs.size(); i++) {
            EpochKey key = epochs.get(i);
            query.setParameter("conv" + i, key.conversationId().toString());
            query.setParameter("client" + i, key.clientId());
            query.setParameter("epoch" + i, key.epoch());
        }

        @SuppressWarnings("unchecked")
        List<UUID> result = query.getResultList();
        return result;
    }
}
