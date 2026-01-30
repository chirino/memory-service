package io.github.chirino.memory;

import static org.hamcrest.MatcherAssert.assertThat;
import static org.hamcrest.Matchers.is;

import io.github.chirino.memory.persistence.entity.ConversationOwnershipTransferEntity;
import io.github.chirino.memory.persistence.repo.ConversationOwnershipTransferRepository;
import io.quarkus.test.junit.QuarkusTest;
import jakarta.inject.Inject;
import java.sql.Connection;
import java.sql.ResultSet;
import java.sql.Statement;
import java.util.List;
import javax.sql.DataSource;
import org.hamcrest.Matchers;
import org.junit.jupiter.api.Test;

@QuarkusTest
class PostgresqlMemoryServiceTest extends AbstractMemoryServiceTest {

    @Inject ConversationOwnershipTransferRepository ownershipTransferRepository;

    @Inject DataSource dataSource;

    @Override
    protected void verifyOwnershipTransferPersisted(
            String conversationId, String fromUserId, String toUserId) {
        List<ConversationOwnershipTransferEntity> transfers = ownershipTransferRepository.listAll();
        assertThat(transfers, Matchers.not(Matchers.empty()));

        boolean found =
                transfers.stream()
                        .anyMatch(
                                t ->
                                        t.getConversationGroup()
                                                        .getId()
                                                        .toString()
                                                        .equals(conversationId)
                                                && t.getFromUserId().equals(fromUserId)
                                                && t.getToUserId().equals(toUserId));
        assertThat(found, is(true));
    }

    @Test
    void liquibase_applies_initial_schema() throws Exception {
        try (Connection connection = dataSource.getConnection();
                Statement stmt = connection.createStatement()) {
            // databasechangelog table should exist
            try (ResultSet rs =
                    connection.getMetaData().getTables(null, null, "databasechangelog", null)) {
                org.junit.jupiter.api.Assertions.assertTrue(rs.next());
            }

            // conversations table should exist and be queryable
            try (ResultSet rs = stmt.executeQuery("select count(*) from conversations")) {
                org.junit.jupiter.api.Assertions.assertTrue(rs.next());
            }
        }
    }
}
