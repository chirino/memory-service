package io.github.chirino.memory.persistence.entity;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.ManyToOne;
import jakarta.persistence.PrePersist;
import jakarta.persistence.PreUpdate;
import jakarta.persistence.Table;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.UUID;
import org.hibernate.annotations.JdbcTypeCode;
import org.hibernate.type.SqlTypes;

@Entity
@Table(name = "conversations")
public class ConversationEntity {

    @Id
    @Column(name = "id", nullable = false, updatable = false)
    private UUID id;

    @JdbcTypeCode(SqlTypes.BINARY)
    @Column(name = "title", columnDefinition = "bytea")
    private byte[] title;

    @Column(name = "owner_user_id", nullable = false)
    private String ownerUserId;

    @JdbcTypeCode(SqlTypes.JSON)
    @Column(name = "metadata", nullable = false)
    private Map<String, Object> metadata;

    @ManyToOne
    @JoinColumn(name = "conversation_group_id", nullable = false)
    private ConversationGroupEntity conversationGroup;

    @Column(name = "forked_at_message_id")
    private UUID forkedAtMessageId;

    @Column(name = "forked_at_conversation_id")
    private UUID forkedAtConversationId;

    @Column(name = "created_at", nullable = false)
    private OffsetDateTime createdAt;

    @Column(name = "updated_at", nullable = false)
    private OffsetDateTime updatedAt;

    @Column(name = "vectorized_at")
    private OffsetDateTime vectorizedAt;

    @Column(name = "deleted_at")
    private OffsetDateTime deletedAt;

    public UUID getId() {
        return id;
    }

    public void setId(UUID id) {
        this.id = id;
    }

    public byte[] getTitle() {
        return title;
    }

    public void setTitle(byte[] title) {
        this.title = title;
    }

    public String getOwnerUserId() {
        return ownerUserId;
    }

    public void setOwnerUserId(String ownerUserId) {
        this.ownerUserId = ownerUserId;
    }

    public Map<String, Object> getMetadata() {
        return metadata;
    }

    public void setMetadata(Map<String, Object> metadata) {
        this.metadata = metadata;
    }

    public ConversationGroupEntity getConversationGroup() {
        return conversationGroup;
    }

    public void setConversationGroup(ConversationGroupEntity conversationGroup) {
        this.conversationGroup = conversationGroup;
    }

    public UUID getForkedAtMessageId() {
        return forkedAtMessageId;
    }

    public void setForkedAtMessageId(UUID forkedAtMessageId) {
        this.forkedAtMessageId = forkedAtMessageId;
    }

    public UUID getForkedAtConversationId() {
        return forkedAtConversationId;
    }

    public void setForkedAtConversationId(UUID forkedAtConversationId) {
        this.forkedAtConversationId = forkedAtConversationId;
    }

    public OffsetDateTime getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(OffsetDateTime createdAt) {
        this.createdAt = createdAt;
    }

    public OffsetDateTime getUpdatedAt() {
        return updatedAt;
    }

    public void setUpdatedAt(OffsetDateTime updatedAt) {
        this.updatedAt = updatedAt;
    }

    public OffsetDateTime getVectorizedAt() {
        return vectorizedAt;
    }

    public void setVectorizedAt(OffsetDateTime vectorizedAt) {
        this.vectorizedAt = vectorizedAt;
    }

    public OffsetDateTime getDeletedAt() {
        return deletedAt;
    }

    public void setDeletedAt(OffsetDateTime deletedAt) {
        this.deletedAt = deletedAt;
    }

    public boolean isDeleted() {
        return deletedAt != null;
    }

    @PrePersist
    public void prePersist() {
        OffsetDateTime now = OffsetDateTime.now();
        if (createdAt == null) {
            createdAt = now;
        }
        if (updatedAt == null) {
            updatedAt = now;
        }
        if (id == null) {
            id = UUID.randomUUID();
        }
    }

    @PreUpdate
    public void preUpdate() {
        updatedAt = OffsetDateTime.now();
    }
}
