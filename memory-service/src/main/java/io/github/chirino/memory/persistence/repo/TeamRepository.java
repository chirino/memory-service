package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.TeamEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class TeamRepository implements PanacheRepositoryBase<TeamEntity, UUID> {

    public Optional<TeamEntity> findActiveById(UUID id) {
        return find("id = ?1 AND deletedAt IS NULL", id).firstResultOptional();
    }

    public List<TeamEntity> listForOrg(UUID organizationId) {
        return find(
                        "organization.id = ?1 AND deletedAt IS NULL AND organization.deletedAt IS"
                                + " NULL",
                        organizationId)
                .list();
    }

    public Optional<TeamEntity> findByOrgAndSlug(UUID organizationId, String slug) {
        return find(
                        "organization.id = ?1 AND slug = ?2 AND deletedAt IS NULL AND"
                                + " organization.deletedAt IS NULL",
                        organizationId,
                        slug)
                .firstResultOptional();
    }
}
