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
import java.util.Objects;
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

    @Inject io.github.chirino.memory.attachment.AttachmentStoreSelector attachmentStoreSelector;

    @Inject io.github.chirino.memory.attachment.FileStoreSelector fileStoreSelector;

    @Inject io.github.chirino.memory.attachment.AttachmentDeletionService attachmentDeletionService;

    @Inject io.github.chirino.memory.security.MembershipAuditLogger membershipAuditLogger;

    private MemoryEntriesCache memoryEntriesCache;

    /**
     * Represents a conversation in the fork ancestry chain. The forkedAtEntryId indicates the entry
     * in THIS conversation where we should stop including entries (fork point). For the target
     * conversation, forkedAtEntryId is null, meaning include all entries.
     */
    private record ForkAncestor(String conversationId, String forkedAtEntryId) {}

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
            c.metadata = Collections.emptyMap();
            Instant now = Instant.now();
            c.createdAt = now;
            c.updatedAt = now;

            if (request.getForkedAtConversationId() != null
                    && request.getForkedAtEntryId() != null) {
                // Fork auto-creation: join parent's group instead of creating a new one
                setupForkConversation(
                        c,
                        userId,
                        request.getForkedAtConversationId(),
                        request.getForkedAtEntryId(),
                        inferTitleFromUserEntry(request));
                conversationRepository.persist(c);
                // No membership creation for forks — user already has membership via parent group
            } else {
                // Root conversation auto-creation
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
                c.title = encryptTitle(inferTitleFromUserEntry(request));
                conversationRepository.persist(c);
                membershipRepository.createMembership(
                        c.conversationGroupId, userId, AccessLevel.OWNER);
            }
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
        Map<String, Object> block = new HashMap<>();
        block.put("role", "USER");
        if (request.getContent() != null) {
            block.put("text", request.getContent());
        }
        if (request.getAttachments() != null && !request.getAttachments().isEmpty()) {
            block.put("attachments", request.getAttachments());
        }
        m.decodedContent = List.of(block);
        m.content = encryptContent(m.decodedContent);
        m.indexedContent = request.getContent();
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

    /**
     * Sets up a conversation document as a fork of an existing conversation.
     * The fork joins the parent's conversation group and sets fork pointers.
     * The forkedAtEntryId on the document is resolved to the entry BEFORE the target,
     * so "fork at entry X" means "include entries before X, exclude X and after".
     */
    private void setupForkConversation(
            MongoConversation forkDoc,
            String userId,
            String parentConversationId,
            String forkAtEntryId,
            String inferredTitle) {
        MongoConversation original = conversationRepository.findById(parentConversationId);
        if (original == null || original.deletedAt != null) {
            throw new ResourceNotFoundException("conversation", parentConversationId);
        }
        String groupId = original.conversationGroupId;
        ensureHasAccess(groupId, userId, AccessLevel.WRITER);

        MongoEntry target =
                entryRepository
                        .findByIdOptional(forkAtEntryId)
                        .orElseThrow(() -> new ResourceNotFoundException("entry", forkAtEntryId));
        if (!parentConversationId.equals(target.conversationId)) {
            throw new ResourceNotFoundException("entry", forkAtEntryId);
        }
        if (target.channel != Channel.HISTORY) {
            throw new AccessDeniedException("Forking is only allowed for history entries");
        }

        // Find the previous entry of ANY channel before the target entry.
        // forkedAtEntryId is set to this previous entry — all parent entries up to and including
        // forkedAtEntryId are visible to the fork.
        MongoEntry previous =
                entryRepository
                        .find(
                                "conversationId = ?1 and (createdAt < ?2 or"
                                        + " (createdAt = ?2 and id < ?3))",
                                io.quarkus.panache.common.Sort.by("createdAt")
                                        .descending()
                                        .and("id")
                                        .descending(),
                                parentConversationId,
                                target.createdAt,
                                target.id)
                        .firstResult();

        forkDoc.ownerUserId = original.ownerUserId;
        forkDoc.title =
                encryptTitle(inferredTitle != null ? inferredTitle : decryptTitle(original.title));
        forkDoc.conversationGroupId = original.conversationGroupId;
        forkDoc.forkedAtConversationId = original.id;
        forkDoc.forkedAtEntryId = previous != null ? previous.id : null;
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
            String clientId,
            boolean allForks) {
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null || conversation.deletedAt != null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        String groupId = conversation.conversationGroupId;
        ensureHasAccess(groupId, userId, AccessLevel.READER);

        LOG.infof(
                "getEntries: conversationId=%s, groupId=%s, channel=%s, clientId=%s, allForks=%s",
                conversationId, groupId, channel, clientId, allForks);

        // When allForks=true, bypass fork ancestry and return all entries in the group
        if (allForks) {
            List<MongoEntry> entries =
                    entryRepository.listByConversationGroup(
                            groupId, channel, channel == Channel.MEMORY ? clientId : null);
            List<MongoEntry> paginatedEntries = applyPagination(entries, afterEntryId, limit);
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
        return getEntriesWithForkSupport(conversation, afterEntryId, limit, channel, clientId);
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
     * @param targetConversation the conversation to build ancestry for
     * @return list of ancestors from root (first) to target (last)
     */
    private List<ForkAncestor> buildAncestryStack(MongoConversation targetConversation) {
        String groupId = targetConversation.conversationGroupId;

        // Single query: get all conversations in the group
        List<MongoConversation> allConversations =
                conversationRepository
                        .find("conversationGroupId = ?1 and deletedAt is null", groupId)
                        .list();

        // Build lookup map
        Map<String, MongoConversation> byId =
                allConversations.stream().collect(Collectors.toMap(c -> c.id, c -> c));

        // Build ancestry chain by traversing from target to root
        List<ForkAncestor> stack = new ArrayList<>();
        MongoConversation current = targetConversation;
        String forkPointFromChild = null; // Target conversation includes all its entries

        while (current != null) {
            // Add current conversation with the fork point limit from its child
            stack.add(new ForkAncestor(current.id, forkPointFromChild));

            // For the next iteration (parent), the fork point comes from current's metadata
            forkPointFromChild = current.forkedAtEntryId;
            String parentId = current.forkedAtConversationId;
            current = (parentId != null) ? byId.get(parentId) : null;
        }

        Collections.reverse(stack); // Root first
        LOG.infof(
                "buildAncestryStack: targetConversation=%s, stack size=%d, stack=%s",
                targetConversation.id,
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
     */
    private boolean isFork(MongoConversation conversation) {
        return conversation.forkedAtConversationId != null;
    }

    /**
     * Shared implementation for fork-aware entry retrieval. Used by both user and admin APIs.
     */
    private PagedEntries getEntriesWithForkSupport(
            MongoConversation conversation,
            String afterEntryId,
            int limit,
            Channel channel,
            String clientId) {
        String conversationId = conversation.id;

        // Build ancestry stack for fork-aware retrieval
        List<ForkAncestor> ancestry = buildAncestryStack(conversation);
        String groupId = conversation.conversationGroupId;

        // Query ALL entries in the group (need all channels for fork point tracking)
        List<MongoEntry> allEntries = entryRepository.listByConversationGroup(groupId, null, null);
        LOG.infof(
                "getEntriesWithForkSupport: fetched %d entries from group %s",
                allEntries.size(), groupId);

        // Filter entries based on ancestry chain
        List<MongoEntry> filteredEntries = filterEntriesByAncestry(allEntries, ancestry);
        LOG.infof(
                "getEntriesWithForkSupport: after ancestry filter, %d entries",
                filteredEntries.size());

        // Post-filter by channel and clientId (after ancestry is resolved)
        if (channel != null) {
            filteredEntries = filteredEntries.stream().filter(e -> e.channel == channel).toList();
        }
        if (channel == Channel.MEMORY && clientId != null) {
            filteredEntries =
                    filteredEntries.stream().filter(e -> clientId.equals(e.clientId)).toList();
        }

        // Apply pagination
        List<MongoEntry> paginatedEntries = applyPagination(filteredEntries, afterEntryId, limit);

        return buildPagedEntries(conversationId, paginatedEntries, limit);
    }

    /**
     * Filters entries based on the fork ancestry chain.
     */
    private List<MongoEntry> filterEntriesByAncestry(
            List<MongoEntry> allEntries, List<ForkAncestor> ancestry) {
        if (ancestry.isEmpty()) {
            return allEntries;
        }

        List<MongoEntry> result = new ArrayList<>();
        int ancestorIndex = 0;
        ForkAncestor current = ancestry.get(ancestorIndex);
        boolean isTargetConversation = (ancestorIndex == ancestry.size() - 1);

        for (MongoEntry entry : allEntries) {
            String entryConversationId = entry.conversationId;

            if (entryConversationId.equals(current.conversationId())) {
                if (isTargetConversation) {
                    result.add(entry);
                } else {
                    result.add(entry);
                    if (current.forkedAtEntryId() != null
                            && entry.id.equals(current.forkedAtEntryId())) {
                        ancestorIndex++;
                        if (ancestorIndex < ancestry.size()) {
                            current = ancestry.get(ancestorIndex);
                            isTargetConversation = (ancestorIndex == ancestry.size() - 1);
                        }
                    }
                }
            }
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
     */
    private PagedEntries getMemoryEntriesWithForkSupport(
            MongoConversation conversation,
            String afterEntryId,
            int limit,
            MemoryEpochFilter epochFilter,
            String clientId) {
        String conversationId = conversation.id;

        // For non-forked conversations, use existing efficient queries
        if (!isFork(conversation)) {
            LOG.infof(
                    "getMemoryEntriesWithForkSupport: conversation %s is not a fork, using direct"
                            + " query",
                    conversationId);
            List<MongoEntry> entries =
                    fetchMemoryEntries(conversationId, afterEntryId, limit, epochFilter, clientId);
            return buildPagedEntries(conversationId, entries, limit);
        }

        // Build ancestry stack for fork-aware retrieval
        List<ForkAncestor> ancestry = buildAncestryStack(conversation);
        String groupId = conversation.conversationGroupId;

        // Query ALL entries in the group (need all channels for fork point tracking)
        // Don't filter by clientId in query - filter in Java after fork traversal
        List<MongoEntry> allEntries = entryRepository.listByConversationGroup(groupId, null, null);
        LOG.infof(
                "getMemoryEntriesWithForkSupport: fetched %d entries from group %s",
                allEntries.size(), groupId);

        // Filter entries based on ancestry and epoch
        List<MongoEntry> filteredEntries =
                filterMemoryEntriesWithEpoch(allEntries, ancestry, clientId, epochFilter);
        LOG.infof(
                "getMemoryEntriesWithForkSupport: after epoch filter, %d MEMORY entries",
                filteredEntries.size());

        // Apply pagination
        List<MongoEntry> paginatedEntries = applyPagination(filteredEntries, afterEntryId, limit);

        return buildPagedEntries(conversationId, paginatedEntries, limit);
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
     */
    private List<MongoEntry> filterMemoryEntriesWithEpoch(
            List<MongoEntry> allEntries,
            List<ForkAncestor> ancestry,
            String clientId,
            MemoryEpochFilter epochFilter) {

        if (ancestry.isEmpty()) {
            // No ancestry, return all MEMORY entries matching clientId and epoch
            return filterByEpochOnly(allEntries, clientId, epochFilter);
        }

        MemoryEpochFilter filter = epochFilter != null ? epochFilter : MemoryEpochFilter.latest();
        List<MongoEntry> result = new ArrayList<>();
        long maxEpochSeen = 0;
        int ancestorIndex = 0;
        ForkAncestor currentAncestor = ancestry.get(ancestorIndex);
        boolean isTargetConversation = (ancestorIndex == ancestry.size() - 1);

        for (MongoEntry entry : allEntries) {
            String entryConversationId = entry.conversationId;

            // Skip entries not in current ancestor conversation
            if (!entryConversationId.equals(currentAncestor.conversationId())) {
                continue;
            }

            // Process MEMORY entries with matching clientId for the result
            if (entry.channel == Channel.MEMORY) {
                if (clientId != null && clientId.equals(entry.clientId)) {
                    long entryEpoch = entry.epoch != null ? entry.epoch : 0L;

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
                    && entry.id.equals(currentAncestor.forkedAtEntryId())) {
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
    private List<MongoEntry> filterByEpochOnly(
            List<MongoEntry> allEntries, String clientId, MemoryEpochFilter epochFilter) {
        MemoryEpochFilter filter = epochFilter != null ? epochFilter : MemoryEpochFilter.latest();
        List<MongoEntry> result = new ArrayList<>();
        long maxEpochSeen = 0;

        for (MongoEntry entry : allEntries) {
            if (entry.channel != Channel.MEMORY) {
                continue;
            }
            if (clientId != null && !clientId.equals(entry.clientId)) {
                continue;
            }

            long entryEpoch = entry.epoch != null ? entry.epoch : 0L;

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
     */
    private List<MongoEntry> applyPagination(
            List<MongoEntry> entries, String afterEntryId, int limit) {
        int startIndex = 0;
        if (afterEntryId != null) {
            for (int i = 0; i < entries.size(); i++) {
                if (entries.get(i).id.equals(afterEntryId)) {
                    startIndex = i + 1;
                    break;
                }
            }
        }
        return entries.stream().skip(startIndex).limit(limit).toList();
    }

    /**
     * Builds a PagedEntries response from entry entities.
     */
    private PagedEntries buildPagedEntries(
            String conversationId, List<MongoEntry> entries, int limit) {
        PagedEntries page = new PagedEntries();
        page.setConversationId(conversationId);
        List<EntryDto> dtos = entries.stream().map(this::toEntryDto).toList();
        page.setEntries(dtos);
        String nextCursor =
                dtos.size() == limit && !dtos.isEmpty() ? dtos.get(dtos.size() - 1).getId() : null;
        page.setNextCursor(nextCursor);
        return page;
    }

    @Override
    @Transactional
    public List<EntryDto> appendAgentEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId,
            Long epoch) {
        MongoConversation c = conversationRepository.findById(conversationId);
        if (c != null && c.deletedAt != null) {
            c = null; // Treat soft-deleted as non-existent for auto-create
        }

        // Auto-create conversation if it doesn't exist (optimized for 95% case where it exists)
        if (c == null) {
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

            c = new MongoConversation();
            c.id = conversationId;
            c.metadata = Collections.emptyMap();
            Instant now = Instant.now();
            c.createdAt = now;
            c.updatedAt = now;

            if (forkConvId != null && forkEntryId != null) {
                // Fork auto-creation: join parent's group instead of creating a new one
                setupForkConversation(
                        c, userId, forkConvId, forkEntryId, inferTitleFromEntries(entries));
                conversationRepository.persist(c);
                // No membership creation for forks — user already has membership via parent group
            } else {
                // Root conversation auto-creation
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
                c.title = encryptTitle(inferTitleFromEntries(entries));
                conversationRepository.persist(c);
                membershipRepository.createMembership(
                        c.conversationGroupId, userId, AccessLevel.OWNER);
            }
        } else {
            String groupId = c.conversationGroupId;
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
            List<MongoEntry> latestEntries =
                    entryRepository.listMemoryEntriesAtLatestEpoch(conversationId, clientId);
            if (latestEntries.isEmpty()) {
                effectiveEpoch = 1L; // Initial epoch starts at 1
            } else {
                effectiveEpoch = latestEntries.get(0).epoch;
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

        Instant latestHistoryTimestamp = null;
        List<EntryDto> created = new ArrayList<>(entries.size());
        for (CreateEntryRequest req : entries) {
            MongoEntry m = new MongoEntry();
            m.id = UUID.randomUUID().toString();
            m.conversationId = conversationId;
            m.userId = req.getUserId();
            m.clientId = clientId;
            Channel channel =
                    req.getChannel() != null
                            ? Channel.fromString(req.getChannel().value())
                            : Channel.MEMORY;
            m.channel = channel;

            // Set epoch based on channel type
            // INVARIANT: Memory channel entries must ALWAYS have a non-null epoch.
            // History channel entries always have null epoch.
            if (channel == Channel.MEMORY) {
                m.epoch = effectiveEpoch;
            } else {
                m.epoch = null;
            }

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
        // Note: MongoDB writes are immediately visible (no flush needed unlike Hibernate ORM)

        // Invalidate/update cache if MEMORY entries were created
        if (hasMemoryEntries && clientId != null) {
            LOG.infof(
                    "appendAgentEntries: updating cache for conversationId=%s, clientId=%s",
                    conversationId, clientId);
            updateCacheWithLatestEntries(conversationId, clientId);
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

        // Load conversation to check if it's a fork
        MongoConversation conversation = conversationRepository.findById(conversationId);
        if (conversation == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }

        List<EntryDto> latestEpochEntries;
        Long latestEpoch;

        if (isFork(conversation)) {
            // Fork-aware retrieval: include inherited parent entries for content comparison.
            // Without this, prefix detection fails because the fork has no entries of its own
            // initially, causing all incoming messages to be bundled into a single entry.
            List<ForkAncestor> ancestry = buildAncestryStack(conversation);
            String groupId = conversation.conversationGroupId;
            List<MongoEntry> allEntries =
                    entryRepository.listByConversationGroup(groupId, null, null);
            List<MongoEntry> filteredEntries =
                    filterMemoryEntriesWithEpoch(
                            allEntries, ancestry, clientId, MemoryEpochFilter.latest());
            latestEpochEntries =
                    filteredEntries.stream().map(this::toEntryDto).collect(Collectors.toList());
            latestEpoch =
                    filteredEntries.stream()
                            .map(e -> e.epoch)
                            .filter(Objects::nonNull)
                            .max(Long::compare)
                            .orElse(null);
        } else {
            // Combined method: finds max epoch and lists entries
            List<MongoEntry> latestEpochEntityList =
                    entryRepository.listMemoryEntriesAtLatestEpoch(conversationId, clientId);
            latestEpochEntries =
                    latestEpochEntityList.stream()
                            .map(this::toEntryDto)
                            .collect(Collectors.toList());
            // Extract epoch from entries (all entries in the list share the same epoch)
            latestEpoch =
                    latestEpochEntityList.isEmpty() ? null : latestEpochEntityList.get(0).epoch;
        }

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
            CreateEntryRequest toAppend = MemorySyncHelper.withContent(entry, incomingContent);
            List<EntryDto> appended =
                    appendAgentEntries(
                            userId, conversationId, List.of(toAppend), clientId, nextEpoch);
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
            // Use latestEpoch if available, otherwise this is the first entry so use initial epoch
            Long epochToUse = latestEpoch != null ? latestEpoch : 1L;
            CreateEntryRequest deltaEntry = MemorySyncHelper.withContent(entry, deltaContent);
            List<EntryDto> appended =
                    appendAgentEntries(
                            userId, conversationId, List.of(deltaEntry), clientId, epochToUse);
            // Update cache with appended entry
            updateCacheWithLatestEntries(conversationId, clientId);
            result.setEntry(appended.isEmpty() ? null : appended.get(0));
            result.setEpochIncremented(false);
            result.setNoOp(false);
            return result;
        }

        // Content diverged - create new epoch with all incoming content
        Long nextEpoch = MemorySyncHelper.nextEpoch(latestEpoch);
        CreateEntryRequest toAppend = MemorySyncHelper.withContent(entry, incomingContent);
        List<EntryDto> appended =
                appendAgentEntries(userId, conversationId, List.of(toAppend), clientId, nextEpoch);
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
        dto.setClientId(entity.clientId);
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

        // Wrap matched query in **query** markers
        String result = snippet.toString().trim();
        result = result.replaceAll("(?i)(" + java.util.regex.Pattern.quote(query) + ")", "**$1**");
        return result;
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

        int limit = query.getLimit() > 0 ? query.getLimit() : 50;
        String afterEntryId = query.getAfterEntryId();
        Channel channel = query.getChannel();
        boolean allForks = query.isAllForks();

        LOG.infof(
                "adminGetEntries: conversationId=%s, forkedAtConversationId=%s,"
                        + " forkedAtEntryId=%s, channel=%s, limit=%d, afterEntryId=%s, allForks=%s",
                conversationId,
                conversation.forkedAtConversationId,
                conversation.forkedAtEntryId,
                channel,
                limit,
                afterEntryId,
                allForks);

        // When allForks=true, bypass fork ancestry and return all entries in the group
        if (allForks) {
            String groupId = conversation.conversationGroupId;
            List<MongoEntry> entries =
                    entryRepository.listByConversationGroup(groupId, channel, null);
            List<MongoEntry> paginatedEntries = applyPagination(entries, afterEntryId, limit);
            LOG.infof(
                    "adminGetEntries: found %d entries for conversationId=%s, allForks=true",
                    paginatedEntries.size(), conversationId);
            return buildPagedEntries(conversationId, paginatedEntries, limit);
        }

        // Use fork-aware retrieval (clientId is null for admin API)
        return getEntriesWithForkSupport(conversation, afterEntryId, limit, channel, null);
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

        // 2. Delete FileStore blobs and attachment records for entries in these groups
        //    (with ref-count safety for shared storage keys)
        try {
            var attachmentStore = attachmentStoreSelector.getStore();
            List<String> entryIds = new java.util.ArrayList<>();
            for (Document doc :
                    getEntryCollection()
                            .find(Filters.in("conversationGroupId", groupIds))
                            .projection(new Document("_id", 1))) {
                entryIds.add(doc.getString("_id"));
            }
            if (!entryIds.isEmpty()) {
                var attachments = attachmentStore.findByEntryIds(entryIds);
                attachmentDeletionService.deleteAttachments(attachments);
            }
        } catch (Exception e) {
            LOG.warnf("Failed to cleanup attachments for groups %s: %s", groupIds, e.getMessage());
        }

        // 3. Delete from data store (explicit cascade - MongoDB has no ON DELETE CASCADE)
        Bson filter = Filters.in("conversationGroupId", groupIds);
        Bson groupFilter = Filters.in("_id", groupIds);

        // Delete children first, then parents
        getEntryCollection().deleteMany(filter);
        getConversationCollection().deleteMany(filter);
        getMembershipCollection().deleteMany(filter);
        getOwnershipTransferCollection().deleteMany(filter);
        getConversationGroupCollection().deleteMany(groupFilter);
    }
}
