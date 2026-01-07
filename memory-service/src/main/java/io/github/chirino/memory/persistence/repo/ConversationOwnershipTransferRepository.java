package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.ConversationOwnershipTransferEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.UUID;

@ApplicationScoped
public class ConversationOwnershipTransferRepository
        implements PanacheRepositoryBase<ConversationOwnershipTransferEntity, UUID> {}
