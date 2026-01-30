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
import io.github.chirino.memory.api.dto.IndexTranscriptRequest;
import io.github.chirino.memory.api.dto.OwnershipTransferDto;
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
        softDeleteConversationGroup(groupId);
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
        m.contentType = "message";
        m.conversationGroupId = c.conversationGroupId;
        m.decodedContent = List.of(Map.of("type", "text", "text", request.getContent()));
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
        if (c == null || c.deletedAt != null) {
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
        MongoConversationMembership membership =
                membershipRepository.findMembership(groupId, memberUserId).orElse(null);
        if (membership != null) {
            membership.deletedAt = Instant.now();
            membershipRepository.update(membership);
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
    public SyncResult syncAgentEntries(
            String userId,
            String conversationId,
            List<CreateEntryRequest> entries,
            String clientId) {
        validateSyncEntries(entries);
        Long latestEpoch = entryRepository.findLatestMemoryEpoch(conversationId, clientId);
        List<EntryDto> latestEpochEntries =
                latestEpoch != null
                        ? entryRepository
                                .listMemoryEntriesByEpoch(conversationId, latestEpoch, clientId)
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
                throw new IllegalArgumentException("sync entries must target memory channel");
            }
        }
    }

    @Override
    @Transactional
    public EntryDto indexTranscript(IndexTranscriptRequest request, String clientId) {
        String conversationId = request.getConversationId();
        MongoConversation c = conversationRepository.findById(conversationId);
        if (c == null) {
            throw new ResourceNotFoundException("conversation", conversationId);
        }
        MongoEntry transcriptEntry = new MongoEntry();
        transcriptEntry.id = UUID.randomUUID().toString();
        transcriptEntry.conversationId = conversationId;
        transcriptEntry.userId = null;
        transcriptEntry.clientId = clientId;
        transcriptEntry.channel = Channel.TRANSCRIPT;
        transcriptEntry.epoch = null;
        transcriptEntry.contentType = "transcript";
        transcriptEntry.decodedContent = buildTranscriptContent(request);
        transcriptEntry.content = encryptContent(transcriptEntry.decodedContent);
        transcriptEntry.conversationGroupId = c.conversationGroupId;
        transcriptEntry.createdAt = Instant.now();
        entryRepository.persist(transcriptEntry);

        boolean conversationUpdated = false;
        if (request.getTitle() != null && !request.getTitle().isBlank()) {
            c.title = encryptTitle(request.getTitle());
            conversationUpdated = true;
        }
        if (shouldVectorize()) {
            vectorizeTranscript(c, transcriptEntry, request);
        } else if (conversationUpdated) {
            conversationRepository.persistOrUpdate(c);
        }

        return toEntryDto(transcriptEntry);
    }

    private List<Object> buildTranscriptContent(IndexTranscriptRequest request) {
        if (request == null) {
            return Collections.emptyList();
        }
        Map<String, Object> block = new HashMap<>();
        block.put("type", "transcript");
        block.put("text", request.getTranscript());
        if (request.getUntilEntryId() != null) {
            block.put("untilEntryId", request.getUntilEntryId());
        }
        return List.of(block);
    }

    private boolean shouldVectorize() {
        VectorStore store = vectorStoreSelector.getVectorStore();
        return store != null && store.isEnabled() && embeddingService.isEnabled();
    }

    private void vectorizeTranscript(
            MongoConversation conversation,
            MongoEntry transcriptEntry,
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
            store.upsertTranscriptEmbedding(conversation.id, transcriptEntry.id, embedding);
            conversation.vectorizedAt = Instant.now();
            conversationRepository.persistOrUpdate(conversation);
        } catch (Exception e) {
            LOG.warnf(e, "Failed to vectorize transcript for conversationId=%s", conversation.id);
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
    public List<SearchResultDto> searchEntries(String userId, SearchEntriesRequest request) {
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
                conversationRepository.find("conversationGroupId in ?1", groupIds).stream()
                        .filter(c -> c.deletedAt == null)
                        .collect(Collectors.toList());
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

        List<MongoEntry> candidates =
                entryRepository.find("conversationId in ?1", targetConversationIds).list();

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
                            dto.setEntry(toEntryDto(m, content));
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

    private void softDeleteConversationGroup(String conversationGroupId) {
        Instant now = Instant.now();

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

        // Mark all memberships as deleted
        List<MongoConversationMembership> memberships =
                membershipRepository.listForConversationGroup(conversationGroupId);
        for (MongoConversationMembership m : memberships) {
            m.deletedAt = now;
            membershipRepository.update(m);
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
        softDeleteConversationGroup(groupId);
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

        // Restore all memberships
        List<MongoConversationMembership> memberships =
                membershipRepository.listForConversationGroup(groupId);
        for (MongoConversationMembership m : memberships) {
            m.deletedAt = null;
            membershipRepository.update(m);
        }
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
    public List<SearchResultDto> adminSearchEntries(AdminSearchQuery query) {
        if (query.getQuery() == null || query.getQuery().isBlank()) {
            return Collections.emptyList();
        }

        List<MongoConversation> allConversations = conversationRepository.listAll();
        Set<String> conversationIds =
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
                        .map(c -> c.id)
                        .collect(Collectors.toSet());

        if (query.getConversationIds() != null && !query.getConversationIds().isEmpty()) {
            Set<String> requested = Set.copyOf(query.getConversationIds());
            conversationIds.retainAll(requested);
        }

        if (conversationIds.isEmpty()) {
            return Collections.emptyList();
        }

        String searchQuery = query.getQuery().toLowerCase();
        int limit = query.getTopK() != null ? query.getTopK() : 20;

        List<MongoEntry> candidates =
                entryRepository.find("conversationId in ?1", conversationIds).list();

        return candidates.stream()
                .map(
                        m -> {
                            List<Object> content = decryptContent(m.content);
                            if (content == null || content.isEmpty()) {
                                return null;
                            }
                            String text = extractSearchText(content);
                            if (text == null || !text.toLowerCase().contains(searchQuery)) {
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
    public long countEvictableMemberships(OffsetDateTime cutoff) {
        Instant cutoffInstant = cutoff.toInstant();
        return getMembershipCollection()
                .countDocuments(
                        Filters.and(
                                Filters.ne("deletedAt", null),
                                Filters.lt("deletedAt", cutoffInstant)));
    }

    @Override
    @Transactional
    public int hardDeleteMembershipsBatch(OffsetDateTime cutoff, int limit) {
        Instant cutoffInstant = cutoff.toInstant();
        Bson filter =
                Filters.and(Filters.ne("deletedAt", null), Filters.lt("deletedAt", cutoffInstant));

        // Find and delete in batches - MongoDB doesn't have FOR UPDATE SKIP LOCKED,
        // but deleteMany is idempotent (deleting already-deleted is a no-op)
        List<Document> batch =
                getMembershipCollection().find(filter).limit(limit).into(new ArrayList<>());

        if (batch.isEmpty()) {
            return 0;
        }

        // Collect the _id values (which are strings like "conversationId:userId")
        List<String> ids = new ArrayList<>();
        for (Document doc : batch) {
            Object id = doc.get("_id");
            if (id != null) {
                ids.add(id.toString());
            }
        }

        if (ids.isEmpty()) {
            return 0;
        }

        // Delete by exact _id match to avoid cross-product issues
        Bson deleteFilter = Filters.in("_id", ids);
        return (int) getMembershipCollection().deleteMany(deleteFilter).getDeletedCount();
    }
}
