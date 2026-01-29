package io.github.chirino.memory.store.impl;

import com.fasterxml.jackson.databind.ObjectMapper;
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
import io.github.chirino.memory.store.MemoryEpochFilter;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceConflictException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.github.chirino.memory.vector.EmbeddingService;
import io.github.chirino.memory.vector.VectorStore;
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
        softDeleteConversationGroup(groupId);
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
        entry.setContentType("message");
        entry.setContent(encryptContent(toContentBlocksFromUser(request.getContent())));
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
                        .findActiveById(cid)
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
        membershipRepository.update(
                "deletedAt = ?1 WHERE id.conversationGroupId = ?2 AND id.userId = ?3 AND deletedAt"
                        + " IS NULL",
                OffsetDateTime.now(),
                groupId,
                memberUserId);
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
                                        + " conversationGroup.deletedAt IS NULL",
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
            case LATEST -> {
                Long latestEpoch = entryRepository.findLatestMemoryEpoch(conversationId, clientId);
                // If no entries with epochs exist, list all memory entries
                if (latestEpoch == null) {
                    yield entryRepository.listByChannel(
                            conversationId, afterEntryId, limit, Channel.MEMORY, clientId);
                }
                yield entryRepository.listMemoryEntriesByEpoch(
                        conversationId, afterEntryId, limit, latestEpoch, clientId);
            }
            case EPOCH ->
                    entryRepository.listMemoryEntriesByEpoch(
                            conversationId, afterEntryId, limit, filter.getEpoch(), clientId);
        };
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
    public SyncResult syncAgentEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId) {
        validateSyncEntries(entries);
        UUID cid = UUID.fromString(conversationId);
        Long latestEpoch = entryRepository.findLatestMemoryEpoch(cid, clientId);
        List<EntryDto> latestEpochEntries =
                latestEpoch != null
                        ? entryRepository
                                .listMemoryEntriesByEpoch(cid, latestEpoch, clientId)
                                .stream()
                                .map(this::toEntryDto)
                                .collect(Collectors.toList())
                        : Collections.emptyList();

        List<MemorySyncHelper.MessageContent> existing =
                MemorySyncHelper.fromDtos(latestEpochEntries);
        List<MemorySyncHelper.MessageContent> incoming = MemorySyncHelper.fromRequests(entries);

        SyncResult result = new SyncResult();
        result.setEntries(Collections.emptyList());
        result.setEpoch(latestEpoch);

        // If no existing entries and incoming is empty, that's a no-op (shouldn't happen due to
        // validation)
        if (existing.isEmpty() && incoming.isEmpty()) {
            result.setNoOp(true);
            return result;
        }

        // If existing entries match incoming exactly, it's a no-op
        if (!existing.isEmpty() && existing.equals(incoming)) {
            result.setNoOp(true);
            return result;
        }

        // If incoming is a prefix extension of existing (only adding more entries), append to
        // current epoch
        if (!existing.isEmpty()
                && incoming.size() > existing.size()
                && MemorySyncHelper.isPrefix(existing, incoming)) {
            List<CreateEntryRequest> delta =
                    MemorySyncHelper.withEpoch(
                            entries.subList(existing.size(), entries.size()), latestEpoch);
            List<EntryDto> appended = appendAgentEntries(userId, conversationId, delta, clientId);
            result.setEntries(appended);
            result.setEpochIncremented(false);
            result.setNoOp(false);
            return result;
        }

        // Otherwise, create a new epoch with all incoming entries
        Long nextEpoch = MemorySyncHelper.nextEpoch(latestEpoch);
        List<CreateEntryRequest> toAppend = MemorySyncHelper.withEpoch(entries, nextEpoch);
        List<EntryDto> appended = appendAgentEntries(userId, conversationId, toAppend, clientId);
        result.setEpoch(nextEpoch);
        result.setEntries(appended);
        result.setEpochIncremented(true);
        result.setNoOp(false);
        return result;
    }

    private void validateSyncEntries(List<CreateEntryRequest> entries) {
        if (entries == null || entries.isEmpty()) {
            throw new IllegalArgumentException("entries are required");
        }
        for (CreateEntryRequest entry : entries) {
            if (entry == null
                    || entry.getChannel() == null
                    || entry.getChannel() != CreateEntryRequest.ChannelEnum.MEMORY) {
                throw new IllegalArgumentException("all sync entries must target memory channel");
            }
        }
    }

    @Override
    @Transactional
    public EntryDto indexTranscript(IndexTranscriptRequest request, String clientId) {
        String conversationId = request.getConversationId();
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation =
                conversationRepository
                        .findActiveById(cid)
                        .orElseThrow(
                                () ->
                                        new ResourceNotFoundException(
                                                "conversation", conversationId));

        EntryEntity transcriptEntry = new EntryEntity();
        transcriptEntry.setConversation(conversation);
        transcriptEntry.setUserId(null);
        transcriptEntry.setClientId(clientId);
        transcriptEntry.setChannel(io.github.chirino.memory.model.Channel.TRANSCRIPT);
        transcriptEntry.setEpoch(null);
        transcriptEntry.setContentType("transcript");
        transcriptEntry.setContent(encryptContentFromTranscript(request));
        transcriptEntry.setConversationGroupId(conversation.getConversationGroup().getId());
        entryRepository.persist(transcriptEntry);

        if (request.getTitle() != null && !request.getTitle().isBlank()) {
            conversation.setTitle(encryptTitle(request.getTitle()));
        }

        if (shouldVectorize()) {
            vectorizeTranscript(conversation, transcriptEntry, request);
        }

        return toEntryDto(transcriptEntry);
    }

    @Override
    public List<SearchResultDto> searchEntries(String userId, SearchEntriesRequest request) {
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

        List<EntryEntity> candidates =
                entryRepository.find("conversation.id in ?1", targetConversationIds).list();

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
                            dto.setEntry(toEntryDto(m, content));
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

    private byte[] encryptContentFromTranscript(IndexTranscriptRequest request) {
        if (request == null || request.getTranscript() == null) {
            return null;
        }
        Map<String, Object> block = new HashMap<>();
        block.put("type", "transcript");
        block.put("text", request.getTranscript());
        if (request.getUntilEntryId() != null) {
            block.put("untilEntryId", request.getUntilEntryId());
        }
        return encryptContent(Collections.singletonList(block));
    }

    private boolean shouldVectorize() {
        VectorStore store = vectorStoreSelector.getVectorStore();
        return store != null && store.isEnabled() && embeddingService.isEnabled();
    }

    private void vectorizeTranscript(
            ConversationEntity conversation,
            EntryEntity transcriptEntry,
            IndexTranscriptRequest request) {
        if (request == null
                || request.getTranscript() == null
                || request.getTranscript().isBlank()) {
            return;
        }
        VectorStore store = vectorStoreSelector.getVectorStore();
        try {
            float[] embedding = embeddingService.embed(request.getTranscript());
            if (embedding == null || embedding.length == 0) {
                return;
            }
            store.upsertTranscriptEmbedding(
                    conversation.getId().toString(), transcriptEntry.getId().toString(), embedding);
            conversation.setVectorizedAt(OffsetDateTime.now());
            conversationRepository.persist(conversation);
        } catch (Exception e) {
            LOG.warnf(
                    e,
                    "Failed to vectorize transcript for conversationId=%s",
                    conversation.getId());
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

    private void softDeleteConversationGroup(UUID conversationGroupId) {
        OffsetDateTime now = OffsetDateTime.now();

        // Mark conversation group as deleted
        conversationGroupRepository.update(
                "deletedAt = ?1 WHERE id = ?2 AND deletedAt IS NULL", now, conversationGroupId);

        // Mark all conversations in the group as deleted
        conversationRepository.update(
                "deletedAt = ?1 WHERE conversationGroup.id = ?2 AND deletedAt IS NULL",
                now,
                conversationGroupId);

        // Mark all memberships as deleted
        membershipRepository.update(
                "deletedAt = ?1 WHERE id.conversationGroupId = ?2 AND deletedAt IS NULL",
                now,
                conversationGroupId);

        // Expire pending ownership transfers
        ownershipTransferRepository.update(
                "status = ?1, updatedAt = ?2 WHERE conversationGroup.id = ?3 AND status = ?4",
                ConversationOwnershipTransferEntity.TransferStatus.EXPIRED,
                now,
                conversationGroupId,
                ConversationOwnershipTransferEntity.TransferStatus.PENDING);
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
        return entities.stream()
                .map(entity -> toConversationSummaryDto(entity, AccessLevel.OWNER, null))
                .collect(Collectors.toList());
    }

    @Override
    public Optional<ConversationDto> adminGetConversation(
            String conversationId, boolean includeDeleted) {
        UUID id = UUID.fromString(conversationId);
        ConversationEntity entity;
        if (includeDeleted) {
            entity = conversationRepository.findByIdOptional(id).orElse(null);
        } else {
            entity = conversationRepository.findActiveById(id).orElse(null);
        }
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
        softDeleteConversationGroup(groupId);
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

        // Restore all memberships
        membershipRepository.update("deletedAt = NULL WHERE id.conversationGroupId = ?1", groupId);
    }

    @Override
    public PagedEntries adminGetEntries(String conversationId, AdminMessageQuery query) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation;
        if (query.isIncludeDeleted()) {
            conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        } else {
            conversation = conversationRepository.findActiveById(cid).orElse(null);
        }
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }

        StringBuilder jpql = new StringBuilder("FROM EntryEntity m WHERE m.conversation.id = ?1");
        List<Object> params = new ArrayList<>();
        params.add(cid);
        int paramIndex = 2;

        if (!query.isIncludeDeleted()) {
            jpql.append(" AND m.conversation.deletedAt IS NULL");
            jpql.append(" AND m.conversation.conversationGroup.deletedAt IS NULL");
        }

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
    public List<ConversationMembershipDto> adminListMemberships(
            String conversationId, boolean includeDeleted) {
        UUID cid = UUID.fromString(conversationId);
        ConversationEntity conversation;
        if (includeDeleted) {
            conversation = conversationRepository.findByIdOptional(cid).orElse(null);
        } else {
            conversation = conversationRepository.findActiveById(cid).orElse(null);
        }
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        UUID groupId = conversation.getConversationGroup().getId();
        List<ConversationMembershipEntity> memberships;
        if (includeDeleted) {
            memberships = membershipRepository.find("id.conversationGroupId", groupId).list();
        } else {
            memberships = membershipRepository.listForConversationGroup(groupId);
        }
        return memberships.stream().map(this::toMembershipDto).collect(Collectors.toList());
    }

    @Override
    public List<SearchResultDto> adminSearchEntries(AdminSearchQuery query) {
        if (query.getQuery() == null || query.getQuery().isBlank()) {
            return Collections.emptyList();
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
        Set<UUID> conversationIds =
                conversations.stream().map(ConversationEntity::getId).collect(Collectors.toSet());

        if (query.getConversationIds() != null && !query.getConversationIds().isEmpty()) {
            Set<UUID> requested =
                    query.getConversationIds().stream()
                            .map(UUID::fromString)
                            .collect(Collectors.toSet());
            conversationIds.retainAll(requested);
        }

        if (conversationIds.isEmpty()) {
            return Collections.emptyList();
        }

        String searchQuery = query.getQuery().toLowerCase();
        int limit = query.getTopK() != null ? query.getTopK() : 20;

        List<EntryEntity> candidates =
                entryRepository.find("conversation.id in ?1", conversationIds).list();

        return candidates.stream()
                .map(
                        m -> {
                            List<Object> content = decryptContent(m.getContent());
                            if (content == null || content.isEmpty()) {
                                return null;
                            }
                            String text = extractSearchText(content);
                            if (text == null || !text.toLowerCase().contains(searchQuery)) {
                                return null;
                            }
                            return toSearchResult(m, content);
                        })
                .filter(r -> r != null)
                .limit(limit)
                .collect(Collectors.toList());
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
    public long countEvictableMemberships(OffsetDateTime cutoff) {
        return ((Number)
                        entityManager
                                .createNativeQuery(
                                        "SELECT COUNT(*) FROM conversation_memberships WHERE"
                                            + " deleted_at IS NOT NULL AND deleted_at < :cutoff")
                                .setParameter("cutoff", cutoff)
                                .getSingleResult())
                .longValue();
    }

    @Override
    @Transactional
    public int hardDeleteMembershipsBatch(OffsetDateTime cutoff, int limit) {
        // Single statement: select and delete in one CTE
        // FOR UPDATE SKIP LOCKED ensures concurrent safety
        return entityManager
                .createNativeQuery(
                        "WITH batch AS (  SELECT conversation_group_id, user_id FROM"
                            + " conversation_memberships   WHERE deleted_at IS NOT NULL AND"
                            + " deleted_at < :cutoff   LIMIT :limit   FOR UPDATE SKIP LOCKED)"
                            + " DELETE FROM conversation_memberships m USING batch b WHERE"
                            + " m.conversation_group_id = b.conversation_group_id   AND m.user_id ="
                            + " b.user_id")
                .setParameter("cutoff", cutoff)
                .setParameter("limit", limit)
                .executeUpdate();
    }
}
