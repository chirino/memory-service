package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.TeamMemberEntity;
import io.github.chirino.memory.persistence.entity.TeamMemberEntity.TeamMemberId;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

@ApplicationScoped
public class TeamMemberRepository implements PanacheRepositoryBase<TeamMemberEntity, TeamMemberId> {

    public List<TeamMemberEntity> listForTeam(UUID teamId) {
        return find("id.teamId = ?1 AND team.deletedAt IS NULL", teamId).list();
    }

    public List<TeamMemberEntity> listForUser(String userId) {
        return find("id.userId = ?1 AND team.deletedAt IS NULL", userId).list();
    }

    public Optional<TeamMemberEntity> findMembership(UUID teamId, String userId) {
        return find("id.teamId = ?1 AND id.userId = ?2 AND team.deletedAt IS NULL", teamId, userId)
                .firstResultOptional();
    }

    public boolean isMember(UUID teamId, String userId) {
        return findMembership(teamId, userId).isPresent();
    }
}
