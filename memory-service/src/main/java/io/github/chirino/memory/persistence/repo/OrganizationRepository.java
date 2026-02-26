package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.OrganizationEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class OrganizationRepository implements PanacheRepositoryBase<OrganizationEntity, UUID> {

    public Optional<OrganizationEntity> findActiveById(UUID id) {
        return find("id = ?1 AND deletedAt IS NULL", id).firstResultOptional();
    }

    public Optional<OrganizationEntity> findBySlug(String slug) {
        return find("slug = ?1 AND deletedAt IS NULL", slug).firstResultOptional();
    }
}
