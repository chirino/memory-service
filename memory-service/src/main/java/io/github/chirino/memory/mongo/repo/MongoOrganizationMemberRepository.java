package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.mongo.model.MongoOrganizationMember;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;

@ApplicationScoped
public class MongoOrganizationMemberRepository
        implements PanacheMongoRepositoryBase<MongoOrganizationMember, String> {

    public List<MongoOrganizationMember> listForOrg(String organizationId) {
        return find("organizationId", organizationId).list();
    }

    public List<MongoOrganizationMember> listForUser(String userId) {
        return find("userId", userId).list();
    }

    public Optional<MongoOrganizationMember> findMembership(String organizationId, String userId) {
        String id = organizationId + ":" + userId;
        return Optional.ofNullable(findById(id));
    }

    public Optional<String> findRole(String organizationId, String userId) {
        return findMembership(organizationId, userId).map(m -> m.role);
    }

    public boolean isMember(String organizationId, String userId) {
        return findMembership(organizationId, userId).isPresent();
    }
}
