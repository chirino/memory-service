package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.OrganizationMemberEntity;
import io.github.chirino.memory.persistence.entity.OrganizationMemberEntity.OrganizationMemberId;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class OrganizationMemberRepository
        implements PanacheRepositoryBase<OrganizationMemberEntity, OrganizationMemberId> {

    public List<OrganizationMemberEntity> listForOrg(UUID organizationId) {
        return find("id.organizationId = ?1 AND organization.deletedAt IS NULL", organizationId)
                .list();
    }

    public List<OrganizationMemberEntity> listForUser(String userId) {
        return find("id.userId = ?1 AND organization.deletedAt IS NULL", userId).list();
    }

    public Optional<OrganizationMemberEntity> findMembership(UUID organizationId, String userId) {
        return find(
                        "id.organizationId = ?1 AND id.userId = ?2 AND organization.deletedAt IS"
                                + " NULL",
                        organizationId,
                        userId)
                .firstResultOptional();
    }

    public Optional<String> findRole(UUID organizationId, String userId) {
        return findMembership(organizationId, userId).map(OrganizationMemberEntity::getRole);
    }

    public boolean isMember(UUID organizationId, String userId) {
        return findMembership(organizationId, userId).isPresent();
    }
}
