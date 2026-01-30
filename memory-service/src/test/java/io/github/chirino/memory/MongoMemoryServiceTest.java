package io.github.chirino.memory;

import static org.hamcrest.MatcherAssert.assertThat;
import static org.hamcrest.Matchers.greaterThan;
import static org.hamcrest.Matchers.is;

import com.mongodb.client.MongoClient;
import io.github.chirino.memory.mongo.model.MongoConversationOwnershipTransfer;
import io.github.chirino.memory.mongo.repo.MongoConversationOwnershipTransferRepository;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import jakarta.inject.Inject;
import java.util.List;
import org.hamcrest.Matchers;
import org.junit.jupiter.api.Test;

@QuarkusTest
@TestProfile(MongoRedisTestProfile.class)
class MongoMemoryServiceTest extends AbstractMemoryServiceTest {

    @Inject MongoClient mongoClient;

    @Inject MongoConversationOwnershipTransferRepository ownershipTransferRepository;

    @Override
    protected void verifyOwnershipTransferPersisted(
            String conversationId, String fromUserId, String toUserId) {
        List<MongoConversationOwnershipTransfer> transfers = ownershipTransferRepository.listAll();
        assertThat(transfers, Matchers.not(Matchers.empty()));

        boolean found =
                transfers.stream()
                        .anyMatch(
                                t ->
                                        t.conversationGroupId.equals(conversationId)
                                                && t.fromUserId.equals(fromUserId)
                                                && t.toUserId.equals(toUserId));
        assertThat(found, is(true));
    }

    @Test
    void mongo_liquibase_noop_changelog_runs() {
        long changelogCollections =
                mongoClient
                        .getDatabase("memory_service")
                        .listCollectionNames()
                        .into(new java.util.ArrayList<>())
                        .stream()
                        .filter(name -> name.toLowerCase().contains("databasechangelog"))
                        .count();

        assertThat(changelogCollections, greaterThan(0L));
    }
}
