package io.github.chirino.memory.persistence.entity;

import io.github.chirino.memory.model.AccessLevel;
import jakarta.persistence.Column;
import jakarta.persistence.Embeddable;
import jakarta.persistence.EmbeddedId;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.ManyToOne;
import jakarta.persistence.MapsId;
import jakarta.persistence.PrePersist;
import jakarta.persistence.Table;
import java.io.Serializable;
import java.time.OffsetDateTime;
import java.util.Objects;
import java.util.UUID;

@Entity
@Table(name = "conversation_memberships")
public class ConversationMembershipEntity {

    @EmbeddedId private ConversationMembershipId id;

    @ManyToOne
    @MapsId("conversationGroupId")
    @JoinColumn(name = "conversation_group_id", nullable = false)
    private ConversationGroupEntity conversationGroup;

    @Enumerated(EnumType.STRING)
    @Column(name = "access_level", nullable = false)
    private AccessLevel accessLevel;

    @Column(name = "created_at", nullable = false)
    private OffsetDateTime createdAt;

    @Column(name = "deleted_at")
    private OffsetDateTime deletedAt;

    public ConversationMembershipId getId() {
        return id;
    }

    public void setId(ConversationMembershipId id) {
        this.id = id;
    }

    public ConversationGroupEntity getConversationGroup() {
        return conversationGroup;
    }

    public void setConversationGroup(ConversationGroupEntity conversationGroup) {
        this.conversationGroup = conversationGroup;
    }

    public AccessLevel getAccessLevel() {
        return accessLevel;
    }

    public void setAccessLevel(AccessLevel accessLevel) {
        this.accessLevel = accessLevel;
    }

    public OffsetDateTime getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(OffsetDateTime createdAt) {
        this.createdAt = createdAt;
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
        if (createdAt == null) {
            createdAt = OffsetDateTime.now();
        }
    }

    @Embeddable
    public static class ConversationMembershipId implements Serializable {

        @Column(name = "conversation_group_id")
        private UUID conversationGroupId;

        @Column(name = "user_id")
        private String userId;

        public ConversationMembershipId() {}

        public ConversationMembershipId(UUID conversationGroupId, String userId) {
            this.conversationGroupId = conversationGroupId;
            this.userId = userId;
        }

        public UUID getConversationGroupId() {
            return conversationGroupId;
        }

        public void setConversationGroupId(UUID conversationGroupId) {
            this.conversationGroupId = conversationGroupId;
        }

        public String getUserId() {
            return userId;
        }

        public void setUserId(String userId) {
            this.userId = userId;
        }

        @Override
        public boolean equals(Object o) {
            if (this == o) {
                return true;
            }
            if (!(o instanceof ConversationMembershipId)) {
                return false;
            }
            ConversationMembershipId that = (ConversationMembershipId) o;
            return Objects.equals(conversationGroupId, that.conversationGroupId)
                    && Objects.equals(userId, that.userId);
        }

        @Override
        public int hashCode() {
            return Objects.hash(conversationGroupId, userId);
        }
    }
}
