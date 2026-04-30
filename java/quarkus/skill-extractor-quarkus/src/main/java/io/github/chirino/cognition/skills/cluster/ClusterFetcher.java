package io.github.chirino.cognition.skills.cluster;

import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;
import io.github.chirino.cognition.skills.config.SkillExtractorConfig;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.time.OffsetDateTime;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

@ApplicationScoped
public class ClusterFetcher {

    private final SkillExtractorConfig config;
    private final HttpClient httpClient;
    private final ObjectMapper mapper;

    @Inject
    public ClusterFetcher(SkillExtractorConfig config) {
        this.config = config;
        this.httpClient = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(30)).build();
        this.mapper =
                new ObjectMapper()
                        .registerModule(new JavaTimeModule())
                        .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);
    }

    public List<ClusterSummary> listClusters() throws Exception {
        var uri = URI.create(config.memoryService().baseUrl() + "/admin/v1/knowledge/clusters");
        var request = newRequest(uri).GET().build();
        var response = httpClient.send(request, HttpResponse.BodyHandlers.ofString());
        if (response.statusCode() != 200) {
            throw new RuntimeException("Failed to list clusters: HTTP " + response.statusCode());
        }
        var wrapper = mapper.readValue(response.body(), ClusterListResponse.class);
        return wrapper.clusters != null ? wrapper.clusters : List.of();
    }

    public ClusterIR fetchClusterDetail(String clusterId, int representativeCount)
            throws Exception {
        var uri =
                URI.create(
                        config.memoryService().baseUrl()
                                + "/admin/v1/knowledge/clusters/"
                                + clusterId
                                + "?representative_count="
                                + representativeCount);
        var request = newRequest(uri).GET().build();
        var response = httpClient.send(request, HttpResponse.BodyHandlers.ofString());
        if (response.statusCode() == 404) {
            return null;
        }
        if (response.statusCode() != 200) {
            throw new RuntimeException(
                    "Failed to fetch cluster " + clusterId + ": HTTP " + response.statusCode());
        }
        var detail = mapper.readValue(response.body(), ClusterDetailResponse.class);
        return new ClusterIR(
                detail.id,
                detail.userId,
                detail.label,
                detail.keywords != null ? detail.keywords : List.of(),
                detail.memberCount,
                detail.trend,
                detail.representativeTexts != null ? detail.representativeTexts : Map.of(),
                detail.updatedAt);
    }

    public List<ClusterIR> fetchChangedClusters(OffsetDateTime since, int maxEntries)
            throws Exception {
        var summaries = listClusters();
        var changed = new ArrayList<ClusterIR>();
        for (var s : summaries) {
            if (s.memberCount < config.extraction().minClusterMembers()) {
                continue;
            }
            if (since != null && s.updatedAt != null && !s.updatedAt.isAfter(since)) {
                continue;
            }
            var detail = fetchClusterDetail(s.id, maxEntries);
            if (detail != null) {
                changed.add(detail);
            }
        }
        return changed;
    }

    private HttpRequest.Builder newRequest(URI uri) {
        return HttpRequest.newBuilder(uri)
                .header("X-API-Key", config.memoryService().apiKey())
                .header("X-Client-Id", config.memoryService().clientId())
                .header("Content-Type", "application/json")
                .timeout(Duration.ofSeconds(60));
    }

    // Response DTOs for JSON deserialization

    public record ClusterSummary(
            String id,
            @JsonProperty("user_id") String userId,
            String label,
            List<String> keywords,
            @JsonProperty("member_count") int memberCount,
            String trend,
            @JsonProperty("source_type") String sourceType,
            @JsonProperty("updated_at") OffsetDateTime updatedAt) {}

    record ClusterListResponse(List<ClusterSummary> clusters) {}

    record ClusterDetailResponse(
            String id,
            @JsonProperty("user_id") String userId,
            String label,
            List<String> keywords,
            @JsonProperty("member_count") int memberCount,
            String trend,
            @JsonProperty("source_type") String sourceType,
            @JsonProperty("representative_texts") Map<String, String> representativeTexts,
            @JsonProperty("updated_at") OffsetDateTime updatedAt) {}
}
