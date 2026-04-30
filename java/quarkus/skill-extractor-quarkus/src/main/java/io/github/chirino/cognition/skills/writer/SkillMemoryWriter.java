package io.github.chirino.cognition.skills.writer;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;
import io.github.chirino.cognition.skills.cluster.ClusterIR;
import io.github.chirino.cognition.skills.config.SkillExtractorConfig;
import io.github.chirino.cognition.skills.verifier.VerifiedSkill;
import io.quarkus.logging.Log;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.time.OffsetDateTime;
import java.util.List;
import java.util.Locale;
import java.util.Map;

@ApplicationScoped
public class SkillMemoryWriter {

    private static final String RUNTIME_ID = "skill-extractor-v1";
    private static final int RUNTIME_VERSION = 1;

    private final SkillExtractorConfig config;
    private final HttpClient httpClient;
    private final ObjectMapper mapper;

    @Inject
    public SkillMemoryWriter(SkillExtractorConfig config) {
        this.config = config;
        this.httpClient = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(30)).build();
        this.mapper = new ObjectMapper().registerModule(new JavaTimeModule());
    }

    public void writeSkill(ClusterIR cluster, VerifiedSkill skill) {
        try {
            var namespace = List.of("user", cluster.userId(), "cognition.v1", "skills");
            var key = "skill:" + cluster.id() + ":" + slugify(skill.title());

            var value =
                    Map.of(
                            "kind", "skill",
                            "type", skill.type(),
                            "title", skill.title(),
                            "description", skill.description(),
                            "steps", skill.steps() != null ? skill.steps() : List.of(),
                            "conditions", skill.conditions() != null ? skill.conditions() : "",
                            "confidence", skill.confidence(),
                            "provenance",
                                    Map.of(
                                            "cluster_id",
                                            cluster.id(),
                                            "entry_ids",
                                            skill.sourceEntryIds() != null
                                                    ? skill.sourceEntryIds()
                                                    : List.of()),
                            "runtime",
                                    Map.of(
                                            "id", RUNTIME_ID,
                                            "version", RUNTIME_VERSION),
                            "observed_at", OffsetDateTime.now().toString());

            var indexPayload =
                    Map.of(
                            "title", skill.title().toLowerCase(Locale.ROOT),
                            "description", skill.description().toLowerCase(Locale.ROOT));

            var body =
                    Map.of(
                            "namespace", namespace,
                            "key", key,
                            "value", value,
                            "index", indexPayload);

            var uri = URI.create(config.memoryService().baseUrl() + "/v1/memories");
            var request =
                    HttpRequest.newBuilder(uri)
                            .header("X-API-Key", config.memoryService().apiKey())
                            .header("X-Client-Id", config.memoryService().clientId())
                            .header("Content-Type", "application/json")
                            .timeout(Duration.ofSeconds(30))
                            .PUT(
                                    HttpRequest.BodyPublishers.ofString(
                                            mapper.writeValueAsString(body)))
                            .build();

            var response = httpClient.send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() >= 200 && response.statusCode() < 300) {
                Log.debugf("Wrote skill '%s' for cluster %s", skill.title(), cluster.id());
            } else {
                Log.warnf(
                        "Failed to write skill '%s': HTTP %d - %s",
                        skill.title(), response.statusCode(), response.body());
            }
        } catch (Exception e) {
            Log.errorf(e, "Error writing skill '%s' for cluster %s", skill.title(), cluster.id());
        }
    }

    static String slugify(String title) {
        return title.toLowerCase(Locale.ROOT).replaceAll("[^a-z0-9]+", "-").replaceAll("^-|-$", "");
    }
}
