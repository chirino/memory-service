package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.ConversationGroupEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.UUID;

@ApplicationScoped
public class ConversationGroupRepository
        implements PanacheRepositoryBase<ConversationGroupEntity, UUID> {}
