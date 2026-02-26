package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.mongo.model.MongoTeam;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;

@ApplicationScoped
public class MongoTeamRepository implements PanacheMongoRepositoryBase<MongoTeam, String> {

    public Optional<MongoTeam> findActiveById(String id) {
        MongoTeam team = findById(id);
        if (team == null || team.deletedAt != null) {
            return Optional.empty();
        }
        return Optional.of(team);
    }

    public List<MongoTeam> listForOrg(String organizationId) {
        return find("organizationId = ?1 and deletedAt is null", organizationId).list();
    }

    public Optional<MongoTeam> findByOrgAndSlug(String organizationId, String slug) {
        return find("organizationId = ?1 and slug = ?2 and deletedAt is null", organizationId, slug)
                .firstResultOptional();
    }
}
