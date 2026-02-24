package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.mongo.model.MongoTeamMember;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;

@ApplicationScoped
public class MongoTeamMemberRepository
        implements PanacheMongoRepositoryBase<MongoTeamMember, String> {

    public List<MongoTeamMember> listForTeam(String teamId) {
        return find("teamId", teamId).list();
    }

    public List<MongoTeamMember> listForUser(String userId) {
        return find("userId", userId).list();
    }

    public Optional<MongoTeamMember> findMembership(String teamId, String userId) {
        String id = teamId + ":" + userId;
        return Optional.ofNullable(findById(id));
    }

    public boolean isMember(String teamId, String userId) {
        return findMembership(teamId, userId).isPresent();
    }
}
