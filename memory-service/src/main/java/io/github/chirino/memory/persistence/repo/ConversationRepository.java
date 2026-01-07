package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.ConversationEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.UUID;

@ApplicationScoped
public class ConversationRepository implements PanacheRepositoryBase<ConversationEntity, UUID> {

    public List<ConversationEntity> listByOwner(String ownerUserId, int limit) {
        return find("ownerUserId = ?1 order by createdAt desc", ownerUserId).page(0, limit).list();
    }
}
