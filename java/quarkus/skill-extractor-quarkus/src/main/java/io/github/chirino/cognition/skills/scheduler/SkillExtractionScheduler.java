package io.github.chirino.cognition.skills.scheduler;

import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;
import io.github.chirino.cognition.skills.cluster.ClusterFetcher;
import io.github.chirino.cognition.skills.cluster.ClusterIR;
import io.github.chirino.cognition.skills.config.SkillExtractorConfig;
import io.github.chirino.cognition.skills.extractor.SkillExtractorService;
import io.github.chirino.cognition.skills.verifier.SkillVerifierService;
import io.github.chirino.cognition.skills.writer.SkillMemoryWriter;
import io.quarkus.logging.Log;
import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.stream.Collectors;

@ApplicationScoped
public class SkillExtractionScheduler {

    private static final String WORKER_ID = "skill-extractor";

    @Inject SkillExtractorConfig config;

    @Inject ClusterFetcher clusterFetcher;

    @Inject SkillExtractorService extractorService;

    @Inject SkillVerifierService verifierService;

    @Inject SkillMemoryWriter memoryWriter;

    private final HttpClient httpClient =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(30)).build();
    private final ObjectMapper mapper =
            new ObjectMapper()
                    .registerModule(new JavaTimeModule())
                    .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);

    @Scheduled(
            cron = "${skill.extraction.schedule}",
            concurrentExecution = Scheduled.ConcurrentExecution.SKIP)
    void extractSkills() {
        if (!config.extraction().enabled()) {
            return;
        }

        try {
            var checkpoint = loadCheckpoint();
            var clusters =
                    clusterFetcher.fetchChangedClusters(
                            checkpoint, config.extraction().maxEntriesPerCluster());

            if (clusters.isEmpty()) {
                Log.debug("No changed clusters to process");
                return;
            }

            Log.infof("Processing %d changed clusters", clusters.size());
            OffsetDateTime latestUpdate = checkpoint;

            for (var cluster : clusters) {
                try {
                    processCluster(cluster);
                    if (cluster.updatedAt() != null
                            && (latestUpdate == null
                                    || cluster.updatedAt().isAfter(latestUpdate))) {
                        latestUpdate = cluster.updatedAt();
                    }
                } catch (Exception e) {
                    Log.errorf(e, "Failed to process cluster %s", cluster.id());
                }
            }

            if (latestUpdate != null) {
                saveCheckpoint(latestUpdate);
            }

            Log.infof("Skill extraction cycle complete: %d clusters processed", clusters.size());
        } catch (Exception e) {
            Log.error("Skill extraction cycle failed", e);
        }
    }

    void processCluster(ClusterIR cluster) {
        if (cluster.representativeTexts().isEmpty()) {
            Log.debugf("Cluster %s has no representative texts, skipping", cluster.id());
            return;
        }

        var textsFormatted =
                cluster.representativeTexts().entrySet().stream()
                        .map(e -> "Entry " + e.getKey() + ":\n" + e.getValue())
                        .collect(Collectors.joining("\n\n"));

        var extractionResult =
                extractorService.extractSkills(
                        cluster.label(),
                        String.join(", ", cluster.keywords()),
                        cluster.trend(),
                        cluster.memberCount(),
                        textsFormatted);

        if (extractionResult == null
                || extractionResult.skills() == null
                || extractionResult.skills().isEmpty()) {
            Log.debugf("No skills extracted from cluster %s", cluster.id());
            return;
        }

        var extracted = extractionResult.skills();
        if (extracted.size() > config.extraction().maxSkillsPerCluster()) {
            extracted = extracted.subList(0, config.extraction().maxSkillsPerCluster());
        }

        var verificationInput =
                mapper.valueToTree(
                        Map.of(
                                "cluster_id", cluster.id(),
                                "cluster_label", cluster.label(),
                                "extracted_skills", extracted,
                                "evidence_entry_ids", cluster.representativeTexts().keySet(),
                                "evidence_texts", textsFormatted));

        var verificationResult = verifierService.verify(verificationInput.toString());

        if (verificationResult == null || verificationResult.skills() == null) {
            Log.debugf("No skills verified for cluster %s", cluster.id());
            return;
        }

        for (var skill : verificationResult.skills()) {
            memoryWriter.writeSkill(cluster, skill);
        }

        Log.infof(
                "Cluster %s: extracted %d, verified %d skills",
                cluster.id(), extracted.size(), verificationResult.skills().size());
    }

    private OffsetDateTime loadCheckpoint() {
        try {
            var uri =
                    URI.create(
                            config.memoryService().baseUrl()
                                    + "/v1/admin/checkpoints/"
                                    + WORKER_ID);
            var request =
                    HttpRequest.newBuilder(uri)
                            .header("X-API-Key", config.memoryService().apiKey())
                            .header("X-Client-Id", config.memoryService().clientId())
                            .GET()
                            .timeout(Duration.ofSeconds(10))
                            .build();
            var response = httpClient.send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() == 200) {
                var node = mapper.readTree(response.body());
                var value = node.path("value");
                if (value.has("lastProcessedAt")) {
                    return OffsetDateTime.parse(value.get("lastProcessedAt").asText());
                }
            }
        } catch (Exception e) {
            Log.debug("No checkpoint found, starting fresh", e);
        }
        return null;
    }

    private void saveCheckpoint(OffsetDateTime lastProcessedAt) {
        try {
            var body =
                    Map.of(
                            "value",
                            Map.of(
                                    "lastProcessedAt",
                                    lastProcessedAt.toString(),
                                    "runtimeId",
                                    WORKER_ID));
            var uri =
                    URI.create(
                            config.memoryService().baseUrl()
                                    + "/v1/admin/checkpoints/"
                                    + WORKER_ID);
            var request =
                    HttpRequest.newBuilder(uri)
                            .header("X-API-Key", config.memoryService().apiKey())
                            .header("X-Client-Id", config.memoryService().clientId())
                            .header("Content-Type", "application/json")
                            .PUT(
                                    HttpRequest.BodyPublishers.ofString(
                                            mapper.writeValueAsString(body)))
                            .timeout(Duration.ofSeconds(10))
                            .build();
            httpClient.send(request, HttpResponse.BodyHandlers.ofString());
        } catch (Exception e) {
            Log.warn("Failed to save checkpoint", e);
        }
    }
}
