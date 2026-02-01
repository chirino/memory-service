package io.github.chirino.memory.store.impl;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.mongodb.client.MongoClient;
import com.mongodb.client.MongoCollection;
import com.mongodb.client.model.Filters;
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
import io.github.chirino.memory.mongo.model.MongoConversation;
import io.github.chirino.memory.mongo.model.MongoConversationGroup;
import io.github.chirino.memory.mongo.model.MongoConversationMembership;
import io.github.chirino.memory.mongo.model.MongoConversationOwnershipTransfer;
import io.github.chirino.memory.mongo.model.MongoEntry;
import io.github.chirino.memory.mongo.repo.MongoConversationGroupRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationOwnershipTransferRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationRepository;
import io.github.chirino.memory.mongo.repo.MongoEntryRepository;
import io.github.chirino.memory.mongo.repo.MongoTaskRepository;
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
import jakarta.transaction.Transactional;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
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
import org.bson.Document;
import org.bson.conversions.Bson;
import org.jboss.logging.Logger;

@ApplicationScoped
public class MongoMemoryStore implements MemoryStore {

    private static final Logger LOG = Logger.getLogger(MongoMemoryStore.class);
    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @Inject MongoConversationRepository conversationRepository;

    @Inject MongoConversationGroupRepository conversationGroupRepository;

    @Inject MongoConversationMembershipRepository membershipRepository;

    @Inject MongoEntryRepository entryRepository;

    @Inject MongoConversationOwnershipTransferRepository ownershipTransferRepository;

    @Inject ObjectMapper objectMapper;

    @Inject DataEncryptionService dataEncryptionService;

    @Inject VectorStoreSelector vectorStoreSelector;

    @Inject EmbeddingService embeddingService;

    @Inject MongoClient mongoClient;

    @Inject MongoTaskRepository taskRepository;

    @Inject MemoryEntriesCacheSelector memoryCacheSelector;

    @Inject io.github.chirino.memory.security.MembershipAuditLogger membershipAuditLogger;

    private MemoryEntriesCache memoryEntriesCache;

    @PostConstruct
    void init() {
        memoryEntriesCache = memoryCacheSelector.select();
    }

    private MongoCollection<Document> getConversationGroupCollection() {
        return mongoClient.getDatabase("memory").getCollection("conversationGroups");
    }

    private MongoCollection<Document> getConversationCollection() {
        return mongoClient.getDatabase("memory").getCollection("conversations");
    }

    private MongoCollection<Document> getEntryCollection() {
        return mongoClient.getDatabase("memory").getCollection("entries");
    }

    private MongoCollection<Document> getMembershipCollection() {
        return mongoClient.getDatabase("memory").getCollection("conversationMemberships");
    }

    private MongoCollection<Document> getOwnershipTransferCollection() {
        return mongoClient.getDatabase("memory").getCollection("conversationOwnershipTransfers");
    }

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
                                    "conversationGroupId in ?1 and forkedAtEntryId is null and"
                                            + " forkedAtConversationId is null",
                                    accessByGroup.keySet())
                            .stream()
                            .filter(c -> c.deletedAt == null)
                            .collect(Collectors.toList());
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
                conversationRepository.find("conversationGroupId in ?1", groupIds).stream()
                        .filter(c -> c.deletedAt == null)
                        .collect(Collectors.toList());

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
        if (c == null || c.deletedAt != null) {
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
        softDeleteConversationGroup(groupId, userId);
    }

    @Override
    @Transactional
    public EntryDto appendUserEntry(
            String userId, String conversationId, CreateUserEntryRequest request) {
        MongoConversation c = conversationRepository.findById(conversationId);
        if (c != null && c.deletedAt != null) {
            c = null; // Treat soft-deleted as non-existent for auto-create
        }

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (c == null) {
            c = new MongoConversation();
            c.id = conversationId;
            c.conversationGroupId = conversationId;
            MongoConversationGroup existingGroup =
                    conversationGroupRepository.findById(conversationId);
            if (existingGroup == null || existingGroup.deletedAt != null) {
                MongoConversationGroup group = new MongoConversationGroup();
                group.id = conversationId;
                group.createdAt = Instant.now();
                conversationGroupRepository.persist(group);
            }
            c.ownerUserId = userId;
            String inferredTitle = inferTitleFromUserEntry(request);
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

        MongoEntry m = new MongoEntry();
        m.id = UUID.randomUUID().toString();
        m.conversationId = conversationId;
        m.userId = userId;
        m.channel = Channel.HISTORY;
        m.epoch = null;
        m.contentType = "history";
        m.conversationGroupId = c.conversationGroupId;
        m.decodedContent = List.of(Map.of("text", request.getContent(), "role", "USER"));
        m.content = encryptContent(m.decodedContent);
        Instant createdAt = Instant.now();
        m.createdAt = createdAt;
        entryRepository.persist(m);
        c.updatedAt = createdAt;
        conversationRepository.persistOrUpdate(c);
        return toEntryDto(m);
    }

    @Override
    public List<ConversationMembershipDto> listMemberships(String userId, String conversationId) {
        String groupId = resolveGroupId(conversationId);
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
        String groupId = resolveGroupId(conversationId);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);
        MongoConversation c = conversationRepository.findById(conversationId);
        if (c == null || c.deletedAt != null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        MongoConversationMembership m =
                membershipRepository.createMembership(
                        groupId, request.getUserId(), request.getAccessLevel());

        // Audit log the addition
        membershipAuditLogger.logAdd(
                userId, conversationId, request.getUserId(), request.getAccessLevel());

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
            AccessLevel oldLevel = m.accessLevel;
            m.accessLevel = request.getAccessLevel();
            membershipRepository.update(m);

            // Audit log the update
            membershipAuditLogger.logUpdate(
                    userId, conversationId, memberUserId, oldLevel, request.getAccessLevel());
        }
        return toMembershipDto(m);
    }

    @Override
    @Transactional
    public void deleteMembership(String userId, String conversationId, String memberUserId) {
        String groupId = resolveGroupId(conversationId);
        ensureHasAccess(groupId, userId, AccessLevel.MANAGER);

        // Get the membership before deletion for audit logging
        Optional<MongoConversationMembership> membership =
                membershipRepository.findMembership(groupId, memberUserId);

        if (membership.isPresent()) {
            AccessLevel level = membership.get().accessLevel;

            // Hard delete the membership
            membershipRepository.deleteById(membership.get().id);

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
        MongoConversation original = conversationRepository.findById(conversationId);
        if (original == null || original.deletedAt != null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = original.conversationGroupId;
        ensureHasAccess(groupId, userId, AccessLevel.WRITER);

        MongoEntry target =
                entryRepository
                        .findByIdOptional(entryId)
                        .orElseThrow(() -> new ResourceNotFoundException("entry", entryId));
        if (!conversationId.equals(target.conversationId)) {
            throw new ResourceNotFoundException("entry", entryId);
        }
        if (target.channel != Channel.HISTORY) {
            throw new AccessDeniedException("Forking is only allowed for history entries");
        }

        MongoEntry previous =
                entryRepository
                        .find(
                                "conversationId = ?1 and channel = ?2 and (createdAt < ?3 or"
                                        + " (createdAt = ?3 and id < ?4))",
                                io.quarkus.panache.common.Sort.by("createdAt")
                                        .descending()
                                        .and("id")
                                        .descending(),
                                conversationId,
                                Channel.HISTORY,
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
        fork.forkedAtEntryId = previous != null ? previous.id : null;
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
        if (conversation == null || conversation.deletedAt != null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;
        ensureHasAccess(groupId, userId, AccessLevel.READER);

        List<MongoConversation> candidates =
                conversationRepository.find("conversationGroupId", groupId).stream()
                        .filter(c -> c.deletedAt == null)
                        .sorted(
                                Comparator.comparing(
                                                (MongoConversation c) ->
                                                        c.forkedAtEntryId != null ? 1 : 0)
                                        .thenComparing(
                                                Comparator.comparing(
                                                        (MongoConversation c) -> c.updatedAt,
                                                        Comparator.reverseOrder())))
                        .collect(Collectors.toList());
        List<ConversationForkSummaryDto> results = new ArrayList<>();
        for (MongoConversation candidate : candidates) {
            ConversationForkSummaryDto dto = new ConversationForkSummaryDto();
            dto.setConversationId(candidate.id);
            dto.setConversationGroupId(groupId);
            dto.setForkedAtEntryId(candidate.forkedAtEntryId);
            dto.setForkedAtConversationId(candidate.forkedAtConversationId);
            dto.setTitle(decryptTitle(candidate.title));
            dto.setCreatedAt(formatInstant(candidate.createdAt));
            results.add(dto);
        }
        return results;
    }

    @Override
    public List<OwnershipTransferDto> listPendingTransfers(String userId, String role) {
        List<MongoConversationOwnershipTransfer> transfers;
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
        return ownershipTransferRepository
                .findByIdAndParticipant(transferId, userId)
                .map(this::toOwnershipTransferDto);
    }

    @Override
    @Transactional
    public OwnershipTransferDto createOwnershipTransfer(
            String userId, CreateOwnershipTransferRequest request) {
        String groupId = resolveGroupId(request.getConversationId());

        // Verify user is owner
        MongoConversationMembership membership =
                membershipRepository
                        .findMembership(groupId, userId)
                        .orElseThrow(() -> new AccessDeniedException("No access to conversation"));
        if (membership.accessLevel != AccessLevel.OWNER) {
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
        Optional<MongoConversationOwnershipTransfer> existing =
                ownershipTransferRepository.findByConversationGroup(groupId);
        if (existing.isPresent()) {
            throw new ResourceConflictException(
                    "transfer",
                    existing.get().id,
                    "A pending ownership transfer already exists for this conversation");
        }

        // Create transfer
        MongoConversationOwnershipTransfer transfer = new MongoConversationOwnershipTransfer();
        transfer.id = UUID.randomUUID().toString();
        transfer.conversationGroupId = groupId;
        transfer.fromUserId = userId;
        transfer.toUserId = newOwnerUserId;
        transfer.createdAt = Instant.now();
        ownershipTransferRepository.persist(transfer);

        return toOwnershipTransferDto(transfer);
    }

    @Override
    @Transactional
    public void acceptTransfer(String userId, String transferId) {
        MongoConversationOwnershipTransfer transfer =
                ownershipTransferRepository
                        .findByIdOptional(transferId)
                        .orElseThrow(() -> new ResourceNotFoundException("transfer", transferId));

        // Verify user is recipient
        if (!userId.equals(transfer.toUserId)) {
            throw new AccessDeniedException("Only the recipient can accept a transfer");
        }

        String groupId = transfer.conversationGroupId;

        // Update new owner's membership to OWNER
        MongoConversationMembership newOwnerMembership =
                membershipRepository.findMembership(groupId, transfer.toUserId).orElse(null);
        if (newOwnerMembership != null) {
            newOwnerMembership.accessLevel = AccessLevel.OWNER;
            membershipRepository.update(newOwnerMembership);
        }

        // Update old owner's membership to MANAGER
        MongoConversationMembership oldOwnerMembership =
                membershipRepository.findMembership(groupId, transfer.fromUserId).orElse(null);
        if (oldOwnerMembership != null) {
            oldOwnerMembership.accessLevel = AccessLevel.MANAGER;
            membershipRepository.update(oldOwnerMembership);
        }

        // Update conversation owner_user_id for all conversations in the group
        List<MongoConversation> conversations =
                conversationRepository.find("conversationGroupId", groupId).list();
        for (MongoConversation c : conversations) {
            c.ownerUserId = transfer.toUserId;
            conversationRepository.update(c);
        }

        // Delete the transfer (transfers are always pending while they exist)
        ownershipTransferRepository.delete(transfer);
    }

    @Override
    @Transactional
    public void deleteTransfer(String userId, String transferId) {
        MongoConversationOwnershipTransfer transfer =
                ownershipTransferRepository
                        .findByIdOptional(transferId)
                        .orElseThrow(() -> new ResourceNotFoundException("transfer", transferId));

        // Verify user is sender or recipient
        if (!userId.equals(transfer.fromUserId) && !userId.equals(transfer.toUserId)) {
            throw new AccessDeniedException("Only the sender or recipient can delete a transfer");
        }

        // Hard delete the transfer
        ownershipTransferRepository.delete(transfer);
    }

    private OwnershipTransferDto toOwnershipTransferDto(MongoConversationOwnershipTransfer entity) {
        OwnershipTransferDto dto = new OwnershipTransferDto();
        dto.setId(entity.id);

        // Get conversation ID from group (first non-deleted conversation)
        String groupId = entity.conversationGroupId;
        conversationRepository
                .find("conversationGroupId = ?1 and deletedAt is null", groupId)
                .firstResultOptional()
                .ifPresent(
                        conv -> {
                            dto.setConversationId(conv.id);
                            dto.setConversationTitle(decryptTitle(conv.title));
                        });

        dto.setFromUserId(entity.fromUserId);
        dto.setToUserId(entity.toUserId);
        dto.setCreatedAt(formatInstant(entity.createdAt));
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
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null || conversation.deletedAt != null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;
        ensureHasAccess(groupId, userId, AccessLevel.READER);
        List<MongoEntry> entries;
        if (channel == Channel.MEMORY) {
            entries =
                    fetchMemoryEntries(conversationId, afterEntryId, limit, epochFilter, clientId);
        } else {
            entries =
                    entryRepository.listByChannel(
                            conversationId, afterEntryId, limit, channel, clientId);
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

    private List<MongoEntry> fetchMemoryEntries(
            String conversationId,
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

    private List<MongoEntry> fetchLatestMemoryEntries(
            String conversationId, String afterEntryId, int limit, String clientId) {
        UUID cid = UUID.fromString(conversationId);

        // Try cache first - cache stores the complete list, pagination is applied in-memory
        Optional<CachedMemoryEntries> cached = memoryEntriesCache.get(cid, clientId);
        if (cached.isPresent()) {
            return paginateCachedEntries(
                    cached.get(), conversationId, clientId, afterEntryId, limit);
        }

        // Cache miss - fetch ALL entries from database to populate cache
        List<MongoEntry> allEntries =
                entryRepository.listMemoryEntriesAtLatestEpoch(conversationId, clientId);

        // Populate cache with complete list
        if (!allEntries.isEmpty()) {
            CachedMemoryEntries toCache = toCachedMemoryEntries(allEntries);
            memoryEntriesCache.set(cid, clientId, toCache);
        }

        // Apply pagination in-memory
        return paginateEntries(allEntries, afterEntryId, limit);
    }

    private List<MongoEntry> paginateCachedEntries(
            CachedMemoryEntries cached,
            String conversationId,
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

        // Apply pagination and convert to MongoEntry
        return entries.stream()
                .skip(startIndex)
                .limit(limit)
                .map(ce -> toCachedMongoEntry(ce, conversationId, clientId, epoch))
                .toList();
    }

    private List<MongoEntry> paginateEntries(
            List<MongoEntry> entries, String afterEntryId, int limit) {
        // Find starting index based on afterEntryId cursor
        int startIndex = 0;
        if (afterEntryId != null) {
            for (int i = 0; i < entries.size(); i++) {
                if (entries.get(i).id.equals(afterEntryId)) {
                    startIndex = i + 1; // Start after the cursor entry
                    break;
                }
            }
        }

        // Apply pagination
        return entries.stream().skip(startIndex).limit(limit).toList();
    }

    private CachedMemoryEntries toCachedMemoryEntries(List<MongoEntry> entries) {
        if (entries.isEmpty()) {
            return null;
        }
        long epoch = entries.get(0).epoch != null ? entries.get(0).epoch : 0L;
        List<CachedMemoryEntries.CachedEntry> cachedEntries =
                entries.stream()
                        .map(
                                e ->
                                        new CachedMemoryEntries.CachedEntry(
                                                UUID.fromString(e.id),
                                                e.contentType,
                                                e.content, // Already encrypted bytes
                                                e.createdAt))
                        .toList();
        return new CachedMemoryEntries(epoch, cachedEntries);
    }

    /**
     * Updates the cache with the current latest epoch entries. Called after sync modifications to
     * keep the cache warm instead of invalidating it.
     */
    private void updateCacheWithLatestEntries(String conversationId, String clientId) {
        UUID cid = UUID.fromString(conversationId);
        List<MongoEntry> entries =
                entryRepository.listMemoryEntriesAtLatestEpoch(conversationId, clientId);
        if (!entries.isEmpty()) {
            CachedMemoryEntries cached = toCachedMemoryEntries(entries);
            memoryEntriesCache.set(cid, clientId, cached);
        } else {
            // No entries at latest epoch - remove stale cache entry
            memoryEntriesCache.remove(cid, clientId);
        }
    }

    private MongoEntry toCachedMongoEntry(
            CachedMemoryEntries.CachedEntry cached,
            String conversationId,
            String clientId,
            long epoch) {
        MongoEntry entry = new MongoEntry();
        entry.id = cached.id().toString();
        entry.conversationId = conversationId;
        entry.clientId = clientId;
        entry.channel = Channel.MEMORY;
        entry.epoch = epoch;
        entry.contentType = cached.contentType();
        entry.content = cached.encryptedContent(); // Still encrypted
        entry.createdAt = cached.createdAt();
        return entry;
    }

    @Override
    @Transactional
    public List<EntryDto> appendAgentEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId) {
        MongoConversation c = conversationRepository.findById(conversationId);
        if (c != null && c.deletedAt != null) {
            c = null; // Treat soft-deleted as non-existent for auto-create
        }

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (c == null) {
            c = new MongoConversation();
            c.id = conversationId;
            c.conversationGroupId = conversationId;
            MongoConversationGroup existingGroup =
                    conversationGroupRepository.findById(conversationId);
            if (existingGroup == null || existingGroup.deletedAt != null) {
                MongoConversationGroup group = new MongoConversationGroup();
                group.id = conversationId;
                group.createdAt = Instant.now();
                conversationGroupRepository.persist(group);
            }
            c.ownerUserId = userId;
            String inferredTitle = inferTitleFromEntries(entries);
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
        List<EntryDto> created = new ArrayList<>(entries.size());
        for (CreateEntryRequest req : entries) {
            MongoEntry m = new MongoEntry();
            m.id = UUID.randomUUID().toString();
            m.conversationId = conversationId;
            m.userId = req.getUserId();
            m.clientId = clientId;
            m.channel =
                    req.getChannel() != null
                            ? Channel.fromString(req.getChannel().value())
                            : Channel.MEMORY;
            m.epoch = req.getEpoch();
            m.contentType = req.getContentType() != null ? req.getContentType() : "message";
            m.decodedContent = req.getContent();
            m.content = encryptContent(m.decodedContent);
            m.indexedContent = req.getIndexedContent();
            m.conversationGroupId = c.conversationGroupId;
            Instant createdAt = Instant.now();
            m.createdAt = createdAt;
            entryRepository.persist(m);
            if (m.channel == Channel.HISTORY) {
                latestHistoryTimestamp = createdAt;
            }
            created.add(toEntryDto(m));
        }
        if (latestHistoryTimestamp != null) {
            c.updatedAt = latestHistoryTimestamp;
            conversationRepository.persistOrUpdate(c);
        }
        return created;
    }

    @Override
    @Transactional
    public SyncResult syncAgentEntry(
            String userId, String conversationId, CreateEntryRequest entry, String clientId) {
        validateSyncEntry(entry);
        // Combined method: finds max epoch and lists entries
        List<MongoEntry> latestEpochEntityList =
                entryRepository.listMemoryEntriesAtLatestEpoch(conversationId, clientId);
        List<EntryDto> latestEpochEntries =
                latestEpochEntityList.stream().map(this::toEntryDto).collect(Collectors.toList());
        // Extract epoch from entries (all entries in the list share the same epoch)
        Long latestEpoch =
                latestEpochEntityList.isEmpty() ? null : latestEpochEntityList.get(0).epoch;

        // Flatten content from all existing entries and get incoming content
        List<Object> existingContent = MemorySyncHelper.flattenContent(latestEpochEntries);
        List<Object> incomingContent =
                entry.getContent() != null ? entry.getContent() : Collections.emptyList();

        SyncResult result = new SyncResult();
        result.setEntry(null);
        result.setEpoch(latestEpoch);

        UUID cid = UUID.fromString(conversationId);

        // Check for contentType mismatch - if any existing entry has different contentType, diverge
        if (MemorySyncHelper.hasContentTypeMismatch(latestEpochEntries, entry.getContentType())) {
            // ContentType changed - create new epoch with all incoming content
            Long nextEpoch = MemorySyncHelper.nextEpoch(latestEpoch);
            CreateEntryRequest toAppend =
                    MemorySyncHelper.withEpochAndContent(entry, nextEpoch, incomingContent);
            List<EntryDto> appended =
                    appendAgentEntries(userId, conversationId, List.of(toAppend), clientId);
            // Update cache with new epoch entries
            updateCacheWithLatestEntries(conversationId, clientId);
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
            updateCacheWithLatestEntries(conversationId, clientId);
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
        updateCacheWithLatestEntries(conversationId, clientId);
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
        List<MongoEntry> entriesToVectorize = new ArrayList<>();

        for (IndexEntryRequest req : entries) {
            String conversationId = req.getConversationId();
            String entryId = req.getEntryId();

            MongoEntry entry = entryRepository.findById(entryId);
            if (entry == null) {
                throw new ResourceNotFoundException("entry", entryId);
            }
            if (!entry.conversationId.equals(conversationId)) {
                throw new ResourceNotFoundException("entry", entryId);
            }
            if (entry.channel != Channel.HISTORY) {
                throw new IllegalArgumentException("Only history channel entries can be indexed");
            }

            entry.indexedContent = req.getIndexedContent();
            entryRepository.persistOrUpdate(entry);
            entriesToVectorize.add(entry);
            indexed++;
        }

        // Attempt synchronous vector store indexing
        if (shouldVectorize()) {
            boolean anyFailed = false;
            for (MongoEntry entry : entriesToVectorize) {
                try {
                    vectorizeEntry(entry);
                    entry.indexedAt = Instant.now();
                    entryRepository.persistOrUpdate(entry);
                } catch (Exception e) {
                    LOG.warnf(e, "Failed to vectorize entry %s", entry.id);
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

    private void vectorizeEntry(MongoEntry entry) {
        String text = entry.indexedContent;
        if (text == null || text.isBlank()) {
            return;
        }
        VectorStore store = vectorStoreSelector.getVectorStore();
        float[] embedding = embeddingService.embed(text);
        if (embedding == null || embedding.length == 0) {
            return;
        }
        store.upsertTranscriptEmbedding(
                entry.conversationGroupId, entry.conversationId, entry.id, embedding);
    }

    private boolean shouldVectorize() {
        VectorStore store = vectorStoreSelector.getVectorStore();
        return store != null && store.isEnabled() && embeddingService.isEnabled();
    }

    @Override
    public UnindexedEntriesResponse listUnindexedEntries(int limit, String cursor) {
        // Query entries where channel = HISTORY AND indexed_content IS NULL
        Bson filter =
                Filters.and(
                        Filters.eq("channel", Channel.HISTORY.name()),
                        Filters.eq("indexedContent", null));

        // Handle cursor-based pagination
        if (cursor != null && !cursor.isBlank()) {
            try {
                String decoded =
                        new String(
                                java.util.Base64.getDecoder().decode(cursor),
                                StandardCharsets.UTF_8);
                Instant cursorTime = Instant.from(ISO_FORMATTER.parse(decoded));
                filter = Filters.and(filter, Filters.gt("createdAt", cursorTime));
            } catch (Exception e) {
                // Invalid cursor, ignore
            }
        }

        List<MongoEntry> results =
                entryRepository.find(filter).stream()
                        .sorted(Comparator.comparing(e -> e.createdAt))
                        .limit(limit + 1)
                        .collect(Collectors.toList());

        // Determine next cursor
        String nextCursor = null;
        if (results.size() > limit) {
            results = results.subList(0, limit);
            if (!results.isEmpty()) {
                MongoEntry last = results.get(results.size() - 1);
                String timestamp = ISO_FORMATTER.format(last.createdAt.atOffset(ZoneOffset.UTC));
                nextCursor =
                        java.util.Base64.getEncoder()
                                .encodeToString(timestamp.getBytes(StandardCharsets.UTF_8));
            }
        }

        // Convert to DTOs
        List<UnindexedEntry> data =
                results.stream()
                        .map(e -> new UnindexedEntry(e.conversationId, toEntryDto(e)))
                        .collect(Collectors.toList());

        return new UnindexedEntriesResponse(data, nextCursor);
    }

    @Override
    public List<EntryDto> findEntriesPendingVectorIndexing(int limit) {
        // Query entries where indexed_content IS NOT NULL AND indexed_at IS NULL
        Bson filter =
                Filters.and(Filters.ne("indexedContent", null), Filters.eq("indexedAt", null));

        return entryRepository.find(filter).stream()
                .sorted(Comparator.comparing(e -> e.createdAt))
                .limit(limit)
                .map(this::toEntryDto)
                .collect(Collectors.toList());
    }

    @Override
    @Transactional
    public void setIndexedAt(String entryId, OffsetDateTime indexedAt) {
        MongoEntry entry = entryRepository.findById(entryId);
        if (entry != null) {
            entry.indexedAt = indexedAt.toInstant();
            entryRepository.persistOrUpdate(entry);
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
    public SearchResultsDto searchEntries(String userId, SearchEntriesRequest request) {
        SearchResultsDto result = new SearchResultsDto();
        result.setResults(Collections.emptyList());
        result.setNextCursor(null);

        if (request.getQuery() == null || request.getQuery().isBlank()) {
            return result;
        }

        Set<String> groupIds =
                membershipRepository.list("userId", userId).stream()
                        .map(m -> m.conversationGroupId)
                        .collect(Collectors.toSet());
        if (groupIds.isEmpty()) {
            return result;
        }

        List<MongoConversation> conversations =
                conversationRepository.find("conversationGroupId in ?1", groupIds).stream()
                        .filter(c -> c.deletedAt == null)
                        .collect(Collectors.toList());
        Map<String, MongoConversation> conversationMap =
                conversations.stream().collect(Collectors.toMap(c -> c.id, c -> c));
        Set<String> userConversationIds = conversationMap.keySet();

        if (userConversationIds.isEmpty()) {
            return result;
        }

        String query = request.getQuery().toLowerCase();
        int limit = request.getLimit() != null ? request.getLimit() : 20;

        // Parse after cursor if present
        String afterEntryId = request.getAfter();

        List<MongoEntry> candidates =
                entryRepository.find("conversationId in ?1", userConversationIds).list().stream()
                        .sorted(
                                (a, b) -> {
                                    // Sort by createdAt desc, then id desc
                                    int cmp = b.createdAt.compareTo(a.createdAt);
                                    if (cmp != 0) return cmp;
                                    return b.id.compareTo(a.id);
                                })
                        .collect(Collectors.toList());

        // Skip entries until we find the cursor
        boolean skipMode = afterEntryId != null && !afterEntryId.isBlank();
        List<SearchResultDto> resultsList = new ArrayList<>();

        for (MongoEntry m : candidates) {
            if (skipMode) {
                if (m.id.equals(afterEntryId)) {
                    skipMode = false;
                }
                continue;
            }

            List<Object> content = decryptContent(m.content);
            if (content == null || content.isEmpty()) {
                continue;
            }
            String text = extractSearchText(content);
            String indexedContent = m.indexedContent;
            boolean matchesContent = text != null && text.toLowerCase().contains(query);
            boolean matchesIndexed =
                    indexedContent != null && indexedContent.toLowerCase().contains(query);
            if (!matchesContent && !matchesIndexed) {
                continue;
            }
            // Prefer indexed content for highlights if it matched
            String highlightSource = matchesIndexed ? indexedContent : text;

            SearchResultDto dto = new SearchResultDto();
            dto.setConversationId(m.conversationId);
            MongoConversation conv = conversationMap.get(m.conversationId);
            dto.setConversationTitle(conv != null ? decryptTitle(conv.title) : null);
            dto.setEntryId(m.id);
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
        if (entity.deletedAt != null) {
            dto.setDeletedAt(formatInstant(entity.deletedAt));
        }
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
        if (entity.deletedAt != null) {
            dto.setDeletedAt(formatInstant(entity.deletedAt));
        }
        dto.setConversationGroupId(entity.conversationGroupId);
        dto.setForkedAtEntryId(entity.forkedAtEntryId);
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

    private EntryDto toEntryDto(MongoEntry entity) {
        List<Object> content =
                entity.decodedContent != null
                        ? entity.decodedContent
                        : decryptContent(entity.content);
        return toEntryDto(entity, content);
    }

    private EntryDto toEntryDto(MongoEntry entity, List<Object> content) {
        EntryDto dto = new EntryDto();
        dto.setId(entity.id);
        dto.setConversationId(entity.conversationId);
        dto.setUserId(entity.userId);
        dto.setChannel(entity.channel);
        dto.setEpoch(entity.epoch);
        dto.setContentType(entity.contentType);
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

    private String resolveGroupId(String conversationId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null || conversation.deletedAt != null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        return conversation.conversationGroupId;
    }

    private void softDeleteConversationGroup(String conversationGroupId, String actorUserId) {
        Instant now = Instant.now();

        // Log and hard delete memberships BEFORE soft-deleting the group
        List<MongoConversationMembership> memberships =
                membershipRepository.listForConversationGroup(conversationGroupId);
        for (MongoConversationMembership m : memberships) {
            membershipAuditLogger.logRemove(
                    actorUserId, conversationGroupId, m.userId, m.accessLevel);
            membershipRepository.deleteById(m.id);
        }

        // Mark conversation group as deleted
        MongoConversationGroup group = conversationGroupRepository.findById(conversationGroupId);
        if (group != null && group.deletedAt == null) {
            group.deletedAt = now;
            conversationGroupRepository.update(group);
        }

        // Mark all conversations as deleted
        List<MongoConversation> conversations =
                conversationRepository.find("conversationGroupId", conversationGroupId).stream()
                        .filter(c -> c.deletedAt == null)
                        .collect(Collectors.toList());
        for (MongoConversation c : conversations) {
            c.deletedAt = now;
            conversationRepository.update(c);
        }

        // Hard delete ownership transfers
        ownershipTransferRepository.deleteByConversationGroup(conversationGroupId);
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

    // Admin methods

    @Override
    public List<ConversationSummaryDto> adminListConversations(AdminConversationQuery query) {
        Document filter = new Document();

        if (query.getUserId() != null && !query.getUserId().isBlank()) {
            filter.append("ownerUserId", query.getUserId());
        }

        // Handle mode-specific filtering
        ConversationListMode mode =
                query.getMode() != null ? query.getMode() : ConversationListMode.LATEST_FORK;
        if (mode == ConversationListMode.ROOTS) {
            filter.append("forkedAtEntryId", null);
            filter.append("forkedAtConversationId", null);
        }

        if (query.isOnlyDeleted()) {
            Document deletedFilter = new Document("$ne", null);
            if (query.getDeletedAfter() != null) {
                deletedFilter = new Document("$ne", null);
                filter.append(
                        "deletedAt",
                        new Document("$ne", null)
                                .append("$gte", query.getDeletedAfter().toInstant()));
            }
            if (query.getDeletedBefore() != null) {
                filter.append(
                        "deletedAt",
                        filter.containsKey("deletedAt")
                                ? ((Document) filter.get("deletedAt"))
                                        .append("$lt", query.getDeletedBefore().toInstant())
                                : new Document("$ne", null)
                                        .append("$lt", query.getDeletedBefore().toInstant()));
            }
            if (!filter.containsKey("deletedAt")) {
                filter.append("deletedAt", new Document("$ne", null));
            }
        } else if (!query.isIncludeDeleted()) {
            filter.append("deletedAt", null);
        } else {
            // includeDeleted=true with date filters
            if (query.getDeletedAfter() != null || query.getDeletedBefore() != null) {
                List<Document> orClauses = new ArrayList<>();
                orClauses.add(new Document("deletedAt", null));
                Document deletedDateFilter = new Document("$ne", null);
                if (query.getDeletedAfter() != null) {
                    deletedDateFilter.append("$gte", query.getDeletedAfter().toInstant());
                }
                if (query.getDeletedBefore() != null) {
                    deletedDateFilter.append("$lt", query.getDeletedBefore().toInstant());
                }
                orClauses.add(new Document("deletedAt", deletedDateFilter));
                filter.append("$or", orClauses);
            }
        }

        int limit = query.getLimit() > 0 ? query.getLimit() : 100;
        Document sort = new Document("updatedAt", -1);
        List<MongoConversation> conversations =
                conversationRepository.find(filter, sort).page(0, limit).list();

        // For non-deleted queries, also filter out conversations whose group is deleted
        if (!query.isIncludeDeleted() && !query.isOnlyDeleted()) {
            conversations =
                    conversations.stream()
                            .filter(
                                    c -> {
                                        MongoConversationGroup group =
                                                conversationGroupRepository.findById(
                                                        c.conversationGroupId);
                                        return group == null || group.deletedAt == null;
                                    })
                            .collect(Collectors.toList());
        }

        // For LATEST_FORK mode, filter to only the most recently updated conversation per group
        if (mode == ConversationListMode.LATEST_FORK) {
            Map<String, MongoConversation> latestByGroup = new HashMap<>();
            for (MongoConversation candidate : conversations) {
                String groupId = candidate.conversationGroupId;
                MongoConversation current = latestByGroup.get(groupId);
                if (current == null || candidate.updatedAt.isAfter(current.updatedAt)) {
                    latestByGroup.put(groupId, candidate);
                }
            }
            return latestByGroup.values().stream()
                    .map(c -> toConversationSummaryDto(c, AccessLevel.OWNER, null))
                    .sorted(Comparator.comparing(ConversationSummaryDto::getUpdatedAt).reversed())
                    .limit(limit)
                    .collect(Collectors.toList());
        }

        return conversations.stream()
                .map(c -> toConversationSummaryDto(c, AccessLevel.OWNER, null))
                .collect(Collectors.toList());
    }

    @Override
    public Optional<ConversationDto> adminGetConversation(String conversationId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            return Optional.empty();
        }
        return Optional.of(toConversationDto(conversation, AccessLevel.OWNER, null));
    }

    @Override
    @Transactional
    public void adminDeleteConversation(String conversationId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;
        softDeleteConversationGroup(groupId, "admin");
    }

    @Override
    @Transactional
    public void adminRestoreConversation(String conversationId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;
        MongoConversationGroup group = conversationGroupRepository.findById(groupId);
        if (group == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        if (group.deletedAt == null) {
            throw new ResourceConflictException(
                    "conversation", conversationId, "Conversation is not deleted");
        }

        // Restore conversation group
        group.deletedAt = null;
        conversationGroupRepository.update(group);

        // Restore all conversations in the group
        List<MongoConversation> conversations =
                conversationRepository.find("conversationGroupId", groupId).list();
        for (MongoConversation c : conversations) {
            c.deletedAt = null;
            conversationRepository.update(c);
        }

        // Note: Memberships are hard-deleted when a conversation is deleted,
        // so they cannot be restored. The owner must re-share with other users.
    }

    @Override
    public PagedEntries adminGetEntries(String conversationId, AdminMessageQuery query) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }

        List<MongoEntry> allEntries = entryRepository.find("conversationId", conversationId).list();

        // Sort by createdAt first so cursor-based filtering works correctly
        allEntries.sort(Comparator.comparing(m -> m.createdAt));

        // Find the cursor entry's createdAt if afterEntryId is provided
        Instant cursorCreatedAt = null;
        String cursorId = null;
        if (query.getAfterEntryId() != null && !query.getAfterEntryId().isBlank()) {
            cursorId = query.getAfterEntryId();
            for (MongoEntry m : allEntries) {
                if (m.id.equals(cursorId)) {
                    cursorCreatedAt = m.createdAt;
                    break;
                }
            }
        }

        List<MongoEntry> filtered = new ArrayList<>();
        for (MongoEntry m : allEntries) {
            if (query.getChannel() != null && m.channel != query.getChannel()) {
                continue;
            }
            // Skip entries at or before the cursor position
            if (cursorCreatedAt != null) {
                if (m.createdAt.isBefore(cursorCreatedAt)) {
                    continue;
                }
                if (m.createdAt.equals(cursorCreatedAt) && m.id.compareTo(cursorId) <= 0) {
                    continue;
                }
            }
            filtered.add(m);
        }

        int limit = query.getLimit() > 0 ? query.getLimit() : 50;
        List<MongoEntry> limited = filtered.stream().limit(limit).collect(Collectors.toList());

        List<EntryDto> entryDtos =
                limited.stream().map(this::toEntryDto).collect(Collectors.toList());

        String nextCursor = null;
        if (limited.size() == limit && filtered.size() > limit) {
            nextCursor = limited.get(limited.size() - 1).id;
        }

        PagedEntries result = new PagedEntries();
        result.setConversationId(conversationId);
        result.setEntries(entryDtos);
        result.setNextCursor(nextCursor);
        return result;
    }

    @Override
    public List<ConversationMembershipDto> adminListMemberships(String conversationId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;
        List<MongoConversationMembership> memberships =
                membershipRepository.listForConversationGroup(groupId);
        return memberships.stream().map(this::toMembershipDto).collect(Collectors.toList());
    }

    @Override
    public List<ConversationForkSummaryDto> adminListForks(String conversationId) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;

        // Admin can see all forks including deleted ones
        List<MongoConversation> candidates =
                conversationRepository.find("conversationGroupId", groupId).stream()
                        .sorted(
                                Comparator.comparing(
                                                (MongoConversation c) ->
                                                        c.forkedAtEntryId != null ? 1 : 0)
                                        .thenComparing(
                                                Comparator.comparing(
                                                        (MongoConversation c) -> c.updatedAt,
                                                        Comparator.reverseOrder())))
                        .collect(Collectors.toList());
        List<ConversationForkSummaryDto> results = new ArrayList<>();
        for (MongoConversation candidate : candidates) {
            ConversationForkSummaryDto dto = new ConversationForkSummaryDto();
            dto.setConversationId(candidate.id);
            dto.setConversationGroupId(groupId);
            dto.setForkedAtEntryId(candidate.forkedAtEntryId);
            dto.setForkedAtConversationId(candidate.forkedAtConversationId);
            dto.setTitle(decryptTitle(candidate.title));
            dto.setCreatedAt(formatInstant(candidate.createdAt));
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

        List<MongoConversation> allConversations = conversationRepository.listAll();
        Map<String, MongoConversation> conversationMap =
                allConversations.stream()
                        .filter(
                                c -> {
                                    // Filter by userId
                                    if (query.getUserId() != null
                                            && !query.getUserId().isBlank()
                                            && !query.getUserId().equals(c.ownerUserId)) {
                                        return false;
                                    }
                                    // Filter by deleted status
                                    if (!query.isIncludeDeleted() && c.deletedAt != null) {
                                        return false;
                                    }
                                    return true;
                                })
                        .collect(Collectors.toMap(c -> c.id, c -> c));

        Set<String> conversationIds = conversationMap.keySet();

        if (conversationIds.isEmpty()) {
            return result;
        }

        String searchQuery = query.getQuery().toLowerCase();
        int limit = query.getLimit() != null ? query.getLimit() : 20;

        // Parse after cursor if present
        String afterEntryId = query.getAfter();

        List<MongoEntry> candidates =
                entryRepository.find("conversationId in ?1", conversationIds).list().stream()
                        .sorted(
                                (a, b) -> {
                                    // Sort by createdAt desc, then id desc
                                    int cmp = b.createdAt.compareTo(a.createdAt);
                                    if (cmp != 0) return cmp;
                                    return b.id.compareTo(a.id);
                                })
                        .collect(Collectors.toList());

        // Skip entries until we find the cursor
        boolean skipMode = afterEntryId != null && !afterEntryId.isBlank();
        List<SearchResultDto> resultsList = new ArrayList<>();

        for (MongoEntry m : candidates) {
            if (skipMode) {
                if (m.id.equals(afterEntryId)) {
                    skipMode = false;
                }
                continue;
            }

            List<Object> content = decryptContent(m.content);
            if (content == null || content.isEmpty()) {
                continue;
            }
            String text = extractSearchText(content);
            if (text == null || !text.toLowerCase().contains(searchQuery)) {
                continue;
            }

            SearchResultDto dto = new SearchResultDto();
            dto.setConversationId(m.conversationId);
            MongoConversation conv = conversationMap.get(m.conversationId);
            dto.setConversationTitle(conv != null ? decryptTitle(conv.title) : null);
            dto.setEntryId(m.id);
            dto.setEntry(toEntryDto(m, content));
            dto.setScore(1.0);
            dto.setHighlights(extractHighlight(text, searchQuery));
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
        Instant cutoffInstant = cutoff.toInstant();
        return getConversationGroupCollection()
                .find(
                        Filters.and(
                                Filters.ne("deletedAt", null),
                                Filters.lt("deletedAt", cutoffInstant)))
                .limit(limit)
                .map(doc -> doc.getString("_id"))
                .into(new ArrayList<>());
    }

    @Override
    public long countEvictableGroups(OffsetDateTime cutoff) {
        Instant cutoffInstant = cutoff.toInstant();
        return getConversationGroupCollection()
                .countDocuments(
                        Filters.and(
                                Filters.ne("deletedAt", null),
                                Filters.lt("deletedAt", cutoffInstant)));
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

        // 2. Delete from data store (explicit cascade - MongoDB has no ON DELETE CASCADE)
        Bson filter = Filters.in("conversationGroupId", groupIds);
        Bson groupFilter = Filters.in("_id", groupIds);

        // Delete children first, then parents
        getEntryCollection().deleteMany(filter);
        getConversationCollection().deleteMany(filter);
        getMembershipCollection().deleteMany(filter);
        getOwnershipTransferCollection().deleteMany(filter);
        getConversationGroupCollection().deleteMany(groupFilter);
    }

    @Override
    @Transactional
    public List<EpochKey> findEvictableEpochs(OffsetDateTime cutoff, int limit) {
        Instant cutoffInstant = cutoff.toInstant();

        // MongoDB aggregation pipeline to find evictable epochs
        List<Document> pipeline =
                List.of(
                        // Match memory channel entries with epoch
                        new Document(
                                "$match",
                                new Document()
                                        .append("channel", "MEMORY")
                                        .append("epoch", new Document("$ne", null))),

                        // Group by (conversationId, clientId, epoch) to get last updated time
                        new Document(
                                "$group",
                                new Document()
                                        .append(
                                                "_id",
                                                new Document()
                                                        .append("conversationId", "$conversationId")
                                                        .append("clientId", "$clientId")
                                                        .append("epoch", "$epoch"))
                                        .append("lastUpdated", new Document("$max", "$createdAt"))),

                        // Group by (conversationId, clientId) to find latest epoch
                        new Document(
                                "$group",
                                new Document()
                                        .append(
                                                "_id",
                                                new Document()
                                                        .append(
                                                                "conversationId",
                                                                "$_id.conversationId")
                                                        .append("clientId", "$_id.clientId"))
                                        .append(
                                                "epochs",
                                                new Document(
                                                        "$push",
                                                        new Document()
                                                                .append("epoch", "$_id.epoch")
                                                                .append(
                                                                        "lastUpdated",
                                                                        "$lastUpdated")))
                                        .append("latestEpoch", new Document("$max", "$_id.epoch"))),

                        // Unwind epochs array
                        new Document("$unwind", "$epochs"),

                        // Filter non-latest epochs past cutoff
                        new Document(
                                "$match",
                                new Document(
                                        "$expr",
                                        new Document(
                                                "$and",
                                                List.of(
                                                        new Document(
                                                                "$lt",
                                                                List.of(
                                                                        "$epochs.epoch",
                                                                        "$latestEpoch")),
                                                        new Document(
                                                                "$lt",
                                                                List.of(
                                                                        "$epochs.lastUpdated",
                                                                        cutoffInstant)))))),

                        // Project to output format
                        new Document(
                                "$project",
                                new Document()
                                        .append("conversationId", "$_id.conversationId")
                                        .append("clientId", "$_id.clientId")
                                        .append("epoch", "$epochs.epoch")),

                        // Limit results
                        new Document("$limit", limit));

        List<EpochKey> result = new ArrayList<>();
        for (Document doc : getEntryCollection().aggregate(pipeline)) {
            result.add(
                    new EpochKey(
                            UUID.fromString(doc.getString("conversationId")),
                            doc.getString("clientId"),
                            doc.getLong("epoch")));
        }
        return result;
    }

    @Override
    public long countEvictableEpochEntries(OffsetDateTime cutoff) {
        Instant cutoffInstant = cutoff.toInstant();

        // First find evictable epochs, then count entries
        List<Document> pipeline =
                List.of(
                        // Match memory channel entries with epoch
                        new Document(
                                "$match",
                                new Document()
                                        .append("channel", "MEMORY")
                                        .append("epoch", new Document("$ne", null))),

                        // Group by (conversationId, clientId, epoch)
                        new Document(
                                "$group",
                                new Document()
                                        .append(
                                                "_id",
                                                new Document()
                                                        .append("conversationId", "$conversationId")
                                                        .append("clientId", "$clientId")
                                                        .append("epoch", "$epoch"))
                                        .append("lastUpdated", new Document("$max", "$createdAt"))
                                        .append("count", new Document("$sum", 1))),

                        // Group by (conversationId, clientId) to find latest epoch
                        new Document(
                                "$group",
                                new Document()
                                        .append(
                                                "_id",
                                                new Document()
                                                        .append(
                                                                "conversationId",
                                                                "$_id.conversationId")
                                                        .append("clientId", "$_id.clientId"))
                                        .append(
                                                "epochs",
                                                new Document(
                                                        "$push",
                                                        new Document()
                                                                .append("epoch", "$_id.epoch")
                                                                .append(
                                                                        "lastUpdated",
                                                                        "$lastUpdated")
                                                                .append("count", "$count")))
                                        .append("latestEpoch", new Document("$max", "$_id.epoch"))),

                        // Unwind epochs array
                        new Document("$unwind", "$epochs"),

                        // Filter non-latest epochs past cutoff
                        new Document(
                                "$match",
                                new Document(
                                        "$expr",
                                        new Document(
                                                "$and",
                                                List.of(
                                                        new Document(
                                                                "$lt",
                                                                List.of(
                                                                        "$epochs.epoch",
                                                                        "$latestEpoch")),
                                                        new Document(
                                                                "$lt",
                                                                List.of(
                                                                        "$epochs.lastUpdated",
                                                                        cutoffInstant)))))),

                        // Sum all counts
                        new Document(
                                "$group",
                                new Document()
                                        .append("_id", null)
                                        .append("total", new Document("$sum", "$epochs.count"))));

        Document result = getEntryCollection().aggregate(pipeline).first();
        if (result == null) {
            return 0;
        }
        Number total = result.get("total", Number.class);
        return total != null ? total.longValue() : 0;
    }

    @Override
    @Transactional
    public int deleteEntriesForEpochs(List<EpochKey> epochs) {
        if (epochs.isEmpty()) {
            return 0;
        }

        // Build OR filter for all epoch keys
        List<Bson> epochFilters =
                epochs.stream()
                        .map(
                                key ->
                                        Filters.and(
                                                Filters.eq(
                                                        "conversationId",
                                                        key.conversationId().toString()),
                                                Filters.eq("clientId", key.clientId()),
                                                Filters.eq("epoch", key.epoch()),
                                                Filters.eq("channel", "MEMORY")))
                        .toList();

        Bson filter = Filters.or(epochFilters);

        // 1. Find entry IDs for vector store cleanup
        List<String> entryIds =
                getEntryCollection()
                        .find(filter)
                        .map(doc -> doc.getString("_id"))
                        .into(new ArrayList<>());

        // 2. Queue vector store cleanup tasks
        for (String entryId : entryIds) {
            taskRepository.createTask("vector_store_delete_entry", Map.of("entryId", entryId));
        }

        // 3. Delete entries
        return (int) getEntryCollection().deleteMany(filter).getDeletedCount();
    }
}
