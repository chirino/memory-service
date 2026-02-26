package io.github.chirino.memory.persistence.entity;

import jakarta.persistence.Column;
import jakarta.persistence.Embeddable;
import jakarta.persistence.EmbeddedId;
import jakarta.persistence.Entity;
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
@Table(name = "organization_members")
public class OrganizationMemberEntity {

    @EmbeddedId private OrganizationMemberId id;

    @ManyToOne
    @MapsId("organizationId")
    @JoinColumn(name = "organization_id", nullable = false)
    private OrganizationEntity organization;

    @Column(name = "role", nullable = false)
    private String role;

    @Column(name = "created_at", nullable = false)
    private OffsetDateTime createdAt;

    public OrganizationMemberId getId() {
        return id;
    }

    public void setId(OrganizationMemberId id) {
        this.id = id;
    }

    public OrganizationEntity getOrganization() {
        return organization;
    }

    public void setOrganization(OrganizationEntity organization) {
        this.organization = organization;
    }

    public String getRole() {
        return role;
    }

    public void setRole(String role) {
        this.role = role;
    }

    public OffsetDateTime getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(OffsetDateTime createdAt) {
        this.createdAt = createdAt;
    }

    @PrePersist
    public void prePersist() {
        if (createdAt == null) {
            createdAt = OffsetDateTime.now();
        }
    }

    @Embeddable
    public static class OrganizationMemberId implements Serializable {

        @Column(name = "organization_id")
        private UUID organizationId;

        @Column(name = "user_id")
        private String userId;

        public OrganizationMemberId() {}

        public OrganizationMemberId(UUID organizationId, String userId) {
            this.organizationId = organizationId;
            this.userId = userId;
        }

        public UUID getOrganizationId() {
            return organizationId;
        }

        public void setOrganizationId(UUID organizationId) {
            this.organizationId = organizationId;
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
            if (!(o instanceof OrganizationMemberId that)) {
                return false;
            }
            return Objects.equals(organizationId, that.organizationId)
                    && Objects.equals(userId, that.userId);
        }

        @Override
        public int hashCode() {
            return Objects.hash(organizationId, userId);
        }
    }
}
