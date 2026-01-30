package io.github.chirino.memory.persistence.entity;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.ManyToOne;
import jakarta.persistence.PrePersist;
import jakarta.persistence.Table;
import java.time.OffsetDateTime;
import java.util.UUID;

/**
 * Represents a pending ownership transfer request.
 * Transfers are always "pending" while they exist; accepted/rejected transfers are hard deleted.
 */
@Entity
@Table(name = "conversation_ownership_transfers")
public class ConversationOwnershipTransferEntity {

    @Id
    @Column(name = "id", nullable = false, updatable = false)
    private UUID id;

    @ManyToOne
    @JoinColumn(name = "conversation_group_id", nullable = false)
    private ConversationGroupEntity conversationGroup;

    @Column(name = "from_user_id", nullable = false)
    private String fromUserId;

    @Column(name = "to_user_id", nullable = false)
    private String toUserId;

    @Column(name = "created_at", nullable = false)
    private OffsetDateTime createdAt;

    public UUID getId() {
        return id;
    }

    public void setId(UUID id) {
        this.id = id;
    }

    public ConversationGroupEntity getConversationGroup() {
        return conversationGroup;
    }

    public void setConversationGroup(ConversationGroupEntity conversationGroup) {
        this.conversationGroup = conversationGroup;
    }

    public String getFromUserId() {
        return fromUserId;
    }

    public void setFromUserId(String fromUserId) {
        this.fromUserId = fromUserId;
    }

    public String getToUserId() {
        return toUserId;
    }

    public void setToUserId(String toUserId) {
        this.toUserId = toUserId;
    }

    public OffsetDateTime getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(OffsetDateTime createdAt) {
        this.createdAt = createdAt;
    }

    @PrePersist
    public void prePersist() {
        OffsetDateTime now = OffsetDateTime.now();
        if (id == null) {
            id = UUID.randomUUID();
        }
        if (createdAt == null) {
            createdAt = now;
        }
    }
}
