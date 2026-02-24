package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.mongo.model.MongoOrganization;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.Optional;

@ApplicationScoped
public class MongoOrganizationRepository
        implements PanacheMongoRepositoryBase<MongoOrganization, String> {

    public Optional<MongoOrganization> findActiveById(String id) {
        MongoOrganization org = findById(id);
        if (org == null || org.deletedAt != null) {
            return Optional.empty();
        }
        return Optional.of(org);
    }

    public Optional<MongoOrganization> findBySlug(String slug) {
        return find("slug = ?1 and deletedAt is null", slug).firstResultOptional();
    }
}
