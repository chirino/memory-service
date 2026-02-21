package io.github.chirino.memory.vector;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.qdrant.client.QdrantClient;
import io.qdrant.client.QdrantGrpcClient;
import io.qdrant.client.grpc.Collections.Distance;
import io.qdrant.client.grpc.Collections.VectorParams;
import io.quarkus.runtime.StartupEvent;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.Optional;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@ApplicationScoped
public class QdrantVectorMigration {

    private static final Logger LOG = Logger.getLogger(QdrantVectorMigration.class);

    @ConfigProperty(name = "memory-service.vector.migrate-at-start", defaultValue = "true")
    boolean migrateAtStart;

    @ConfigProperty(name = "memory-service.vector.store.type", defaultValue = "none")
    String vectorStoreType;

    @ConfigProperty(name = "memory-service.vector.qdrant.host", defaultValue = "localhost")
    String qdrantHost;

    @ConfigProperty(name = "memory-service.vector.qdrant.port", defaultValue = "6334")
    int qdrantPort;

    @ConfigProperty(name = "memory-service.vector.qdrant.api-key")
    Optional<String> qdrantApiKey;

    @ConfigProperty(name = "memory-service.vector.qdrant.use-tls", defaultValue = "false")
    boolean qdrantUseTls;

    @ConfigProperty(name = "memory-service.vector.qdrant.startup-timeout", defaultValue = "PT30S")
    Duration startupTimeout;

    @Inject EmbeddingService embeddingService;
    @Inject QdrantCollectionNameResolver collectionNameResolver;

    void onStart(@Observes StartupEvent ignored) {
        if (!migrateAtStart) {
            return;
        }
        if (!isQdrantStore(vectorStoreType)) {
            return;
        }
        migrateQdrantCollection();
    }

    void migrateQdrantCollection() {
        int dimensions = embeddingService.dimensions();
        if (dimensions <= 0) {
            throw new IllegalStateException(
                    "Qdrant migration requires a positive embedding dimension. Current value: "
                            + dimensions
                            + ". Check memory-service.embedding.type configuration.");
        }

        String collectionName = collectionNameResolver.resolveCollectionName();

        try (QdrantClient client = createClient()) {
            if (collectionExists(client, collectionName)) {
                LOG.infof("Qdrant collection '%s' already exists", collectionName);
                return;
            }

            VectorParams vectorParams =
                    VectorParams.newBuilder()
                            .setSize(dimensions)
                            .setDistance(Distance.Cosine)
                            .build();
            createCollection(client, collectionName, vectorParams);
            LOG.infof(
                    "Created Qdrant collection '%s' with size=%d distance=COSINE",
                    collectionName, dimensions);
        }
    }

    protected QdrantClient createClient() {
        QdrantGrpcClient.Builder builder =
                QdrantGrpcClient.newBuilder(qdrantHost, qdrantPort, qdrantUseTls);
        qdrantApiKey.filter(key -> !key.isBlank()).ifPresent(builder::withApiKey);
        return new QdrantClient(builder.build());
    }

    private boolean collectionExists(QdrantClient client, String collectionName) {
        try {
            return client.collectionExistsAsync(collectionName, startupTimeout)
                    .get(startupTimeout.toMillis(), TimeUnit.MILLISECONDS);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("Interrupted while checking Qdrant collection", e);
        } catch (ExecutionException | TimeoutException e) {
            throw new IllegalStateException("Failed to check Qdrant collection existence", e);
        }
    }

    private void createCollection(
            QdrantClient client, String collectionName, VectorParams vectorParams) {
        try {
            client.createCollectionAsync(collectionName, vectorParams, startupTimeout)
                    .get(startupTimeout.toMillis(), TimeUnit.MILLISECONDS);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("Interrupted while creating Qdrant collection", e);
        } catch (ExecutionException e) {
            if (isAlreadyExists(e)) {
                LOG.infof("Qdrant collection '%s' was created concurrently", collectionName);
                return;
            }
            throw new IllegalStateException("Failed to create Qdrant collection", e);
        } catch (TimeoutException e) {
            throw new IllegalStateException("Timed out creating Qdrant collection", e);
        }
    }

    private static boolean isQdrantStore(String value) {
        return value != null && "qdrant".equals(value.trim().toLowerCase());
    }

    private static boolean isAlreadyExists(Throwable error) {
        Throwable current = error;
        while (current != null) {
            if (current instanceof StatusRuntimeException statusException
                    && statusException.getStatus().getCode() == Status.Code.ALREADY_EXISTS) {
                return true;
            }
            String message = current.getMessage();
            if (message != null && message.toLowerCase().contains("already exists")) {
                return true;
            }
            current = current.getCause();
        }
        return false;
    }
}
