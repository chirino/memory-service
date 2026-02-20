package io.github.chirino.memory.vector;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.doReturn;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.spy;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import com.google.common.util.concurrent.Futures;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.qdrant.client.QdrantClient;
import io.qdrant.client.grpc.Collections.CollectionOperationResponse;
import io.qdrant.client.grpc.Collections.Distance;
import io.qdrant.client.grpc.Collections.VectorParams;
import java.time.Duration;
import java.util.Optional;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

class QdrantVectorMigrationTest {

    @Test
    void skips_when_migration_disabled() {
        QdrantClient client = mock(QdrantClient.class);
        QdrantVectorMigration migration = createMigration(client);
        migration.migrateAtStart = false;

        migration.onStart(null);

        verify(client, never()).collectionExistsAsync(any(), any());
    }

    @Test
    void skips_when_store_is_not_qdrant() {
        QdrantClient client = mock(QdrantClient.class);
        QdrantVectorMigration migration = createMigration(client);
        migration.vectorStoreType = "pgvector";

        migration.onStart(null);

        verify(client, never()).collectionExistsAsync(any(), any());
    }

    @Test
    void creates_collection_when_missing() {
        QdrantClient client = mock(QdrantClient.class);
        when(client.collectionExistsAsync(eq("memory_segments"), any()))
                .thenReturn(Futures.immediateFuture(false));
        when(client.createCollectionAsync(eq("memory_segments"), any(VectorParams.class), any()))
                .thenReturn(Futures.immediateFuture(mock(CollectionOperationResponse.class)));

        QdrantVectorMigration migration = createMigration(client);

        migration.onStart(null);

        ArgumentCaptor<VectorParams> paramsCaptor = ArgumentCaptor.forClass(VectorParams.class);
        verify(client).createCollectionAsync(eq("memory_segments"), paramsCaptor.capture(), any());
        VectorParams vectorParams = paramsCaptor.getValue();
        assertEquals(384, vectorParams.getSize());
        assertEquals(Distance.Cosine, vectorParams.getDistance());
    }

    @Test
    void does_not_create_when_collection_exists() {
        QdrantClient client = mock(QdrantClient.class);
        when(client.collectionExistsAsync(eq("memory_segments"), any()))
                .thenReturn(Futures.immediateFuture(true));

        QdrantVectorMigration migration = createMigration(client);

        migration.onStart(null);

        verify(client, never()).createCollectionAsync(any(), any(VectorParams.class), any());
    }

    @Test
    void tolerates_concurrent_already_exists_errors() {
        QdrantClient client = mock(QdrantClient.class);
        when(client.collectionExistsAsync(eq("memory_segments"), any()))
                .thenReturn(Futures.immediateFuture(false));
        when(client.createCollectionAsync(eq("memory_segments"), any(VectorParams.class), any()))
                .thenReturn(
                        Futures.immediateFailedFuture(
                                new StatusRuntimeException(Status.ALREADY_EXISTS)));

        QdrantVectorMigration migration = createMigration(client);

        assertDoesNotThrow(() -> migration.onStart(null));
    }

    @Test
    void fails_when_embedding_dimensions_are_invalid() {
        QdrantClient client = mock(QdrantClient.class);
        QdrantVectorMigration migration = createMigration(client);
        when(migration.embeddingService.dimensions()).thenReturn(0);

        IllegalStateException exception =
                assertThrows(IllegalStateException.class, () -> migration.onStart(null));
        assertTrue(exception.getMessage().contains("positive embedding dimension"));
    }

    private static QdrantVectorMigration createMigration(QdrantClient client) {
        QdrantVectorMigration migration = spy(new QdrantVectorMigration());
        migration.migrateAtStart = true;
        migration.vectorStoreType = "qdrant";
        migration.qdrantHost = "localhost";
        migration.qdrantPort = 6334;
        migration.qdrantCollectionName = "memory_segments";
        migration.qdrantApiKey = Optional.empty();
        migration.qdrantUseTls = false;
        migration.startupTimeout = Duration.ofSeconds(2);

        migration.embeddingService = mock(EmbeddingService.class);
        when(migration.embeddingService.dimensions()).thenReturn(384);
        doReturn(client).when(migration).createClient();
        return migration;
    }
}
