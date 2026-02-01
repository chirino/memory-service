package io.github.chirino.memory.mongo.repo;

import com.mongodb.client.MongoClient;
import com.mongodb.client.MongoCollection;
import com.mongodb.client.model.Aggregates;
import com.mongodb.client.model.Filters;
import com.mongodb.client.model.Projections;
import com.mongodb.client.model.Sorts;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import org.bson.Document;
import org.bson.conversions.Bson;
import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * Repository for MongoDB full-text search using text indexes.
 *
 * <p>Provides fast keyword search with stemming and scoring as a fallback when vector search is
 * unavailable or returns no results.
 */
@ApplicationScoped
public class MongoFullTextSearchRepository {

    @Inject MongoClient mongoClient;

    @ConfigProperty(name = "quarkus.mongodb.database", defaultValue = "memory_service")
    String databaseName;

    private MongoCollection<Document> getEntriesCollection() {
        return mongoClient.getDatabase(databaseName).getCollection("entries");
    }

    private MongoCollection<Document> getConversationsCollection() {
        return mongoClient.getDatabase(databaseName).getCollection("conversations");
    }

    private MongoCollection<Document> getMembershipsCollection() {
        return mongoClient.getDatabase(databaseName).getCollection("conversation_memberships");
    }

    /**
     * Full-text search on indexedContent with access control.
     *
     * @param userId the user ID for access control
     * @param query the search query
     * @param limit maximum results
     * @param groupByConversation when true, returns best match per conversation
     * @return search results with scores
     */
    public List<FullTextSearchResult> search(
            String userId, String query, int limit, boolean groupByConversation) {

        // Get accessible conversation group IDs for user
        Set<String> accessibleGroupIds = getAccessibleGroupIds(userId);
        if (accessibleGroupIds.isEmpty()) {
            return List.of();
        }

        // Get non-deleted conversations in those groups
        Set<String> accessibleConversationIds = getAccessibleConversationIds(accessibleGroupIds);
        if (accessibleConversationIds.isEmpty()) {
            return List.of();
        }

        // Build text search query
        Bson textSearch = Filters.text(query);
        Bson accessFilter = Filters.in("conversationId", accessibleConversationIds);
        Bson combinedFilter = Filters.and(textSearch, accessFilter);

        List<Bson> pipeline = new ArrayList<>();

        // Match with text search and access control
        pipeline.add(Aggregates.match(combinedFilter));

        // Project text score
        pipeline.add(
                Aggregates.project(
                        Projections.fields(
                                Projections.include("_id", "conversationId", "indexedContent"),
                                Projections.metaTextScore("score"))));

        if (groupByConversation) {
            // Group by conversation, keep best match
            pipeline.add(
                    Aggregates.sort(
                            Sorts.orderBy(Sorts.metaTextScore("score"), Sorts.descending("_id"))));
            pipeline.add(
                    new Document(
                            "$group",
                            new Document("_id", "$conversationId")
                                    .append("entryId", new Document("$first", "$_id"))
                                    .append("score", new Document("$first", "$score"))
                                    .append(
                                            "indexedContent",
                                            new Document("$first", "$indexedContent"))));
            pipeline.add(Aggregates.sort(Sorts.descending("score")));
        } else {
            // Sort by score descending
            pipeline.add(Aggregates.sort(Sorts.metaTextScore("score")));
        }

        pipeline.add(Aggregates.limit(limit));

        List<FullTextSearchResult> results = new ArrayList<>();
        for (Document doc : getEntriesCollection().aggregate(pipeline)) {
            String entryId;
            String conversationId;
            double score;
            String indexedContent;

            if (groupByConversation) {
                entryId = doc.getString("entryId");
                conversationId = doc.getString("_id");
                score = doc.getDouble("score");
                indexedContent = doc.getString("indexedContent");
            } else {
                entryId = doc.getString("_id");
                conversationId = doc.getString("conversationId");
                score = doc.getDouble("score");
                indexedContent = doc.getString("indexedContent");
            }

            // Generate highlight from indexed content
            String highlight = extractHighlight(indexedContent, query);

            results.add(new FullTextSearchResult(entryId, conversationId, score, highlight));
        }

        return results;
    }

    private Set<String> getAccessibleGroupIds(String userId) {
        Set<String> groupIds = new java.util.HashSet<>();
        for (Document doc : getMembershipsCollection().find(Filters.eq("userId", userId))) {
            String groupId = doc.getString("conversationGroupId");
            if (groupId != null) {
                groupIds.add(groupId);
            }
        }
        return groupIds;
    }

    private Set<String> getAccessibleConversationIds(Set<String> groupIds) {
        Set<String> conversationIds = new java.util.HashSet<>();
        Bson filter =
                Filters.and(
                        Filters.in("conversationGroupId", groupIds), Filters.eq("deletedAt", null));
        for (Document doc : getConversationsCollection().find(filter)) {
            String conversationId = doc.getString("_id");
            if (conversationId != null) {
                conversationIds.add(conversationId);
            }
        }
        return conversationIds;
    }

    private String extractHighlight(String text, String query) {
        if (text == null || text.isBlank()) {
            return null;
        }

        // Find the position of query terms and extract surrounding context
        String lowerText = text.toLowerCase();
        String lowerQuery = query.toLowerCase();
        String[] queryTerms = lowerQuery.split("\\s+");

        int bestPos = -1;
        for (String term : queryTerms) {
            int pos = lowerText.indexOf(term);
            if (pos >= 0 && (bestPos < 0 || pos < bestPos)) {
                bestPos = pos;
            }
        }

        if (bestPos < 0) {
            // No match found, return first 200 chars
            int maxLength = 200;
            if (text.length() <= maxLength) {
                return text;
            }
            return text.substring(0, maxLength) + "...";
        }

        // Extract context around the match
        int start = Math.max(0, bestPos - 50);
        int end = Math.min(text.length(), bestPos + 150);

        String highlight = text.substring(start, end);
        if (start > 0) {
            highlight = "..." + highlight;
        }
        if (end < text.length()) {
            highlight = highlight + "...";
        }

        return highlight;
    }

    /**
     * Admin full-text search on indexedContent without membership filtering.
     *
     * @param query the search query
     * @param limit maximum results
     * @param groupByConversation when true, returns best match per conversation
     * @param userId optional filter by conversation owner
     * @param includeDeleted whether to include soft-deleted conversations
     * @return search results with scores
     */
    public List<FullTextSearchResult> adminSearch(
            String query,
            int limit,
            boolean groupByConversation,
            String userId,
            boolean includeDeleted) {

        // Get all conversation IDs matching the filters
        Set<String> conversationIds = getConversationIdsForAdmin(userId, includeDeleted);
        if (conversationIds.isEmpty()) {
            return List.of();
        }

        // Build text search query
        Bson textSearch = Filters.text(query);
        Bson accessFilter = Filters.in("conversationId", conversationIds);
        Bson combinedFilter = Filters.and(textSearch, accessFilter);

        List<Bson> pipeline = new ArrayList<>();

        // Match with text search and conversation filter
        pipeline.add(Aggregates.match(combinedFilter));

        // Project text score
        pipeline.add(
                Aggregates.project(
                        Projections.fields(
                                Projections.include("_id", "conversationId", "indexedContent"),
                                Projections.metaTextScore("score"))));

        if (groupByConversation) {
            // Group by conversation, keep best match
            pipeline.add(
                    Aggregates.sort(
                            Sorts.orderBy(Sorts.metaTextScore("score"), Sorts.descending("_id"))));
            pipeline.add(
                    new Document(
                            "$group",
                            new Document("_id", "$conversationId")
                                    .append("entryId", new Document("$first", "$_id"))
                                    .append("score", new Document("$first", "$score"))
                                    .append(
                                            "indexedContent",
                                            new Document("$first", "$indexedContent"))));
            pipeline.add(Aggregates.sort(Sorts.descending("score")));
        } else {
            // Sort by score descending
            pipeline.add(Aggregates.sort(Sorts.metaTextScore("score")));
        }

        pipeline.add(Aggregates.limit(limit));

        List<FullTextSearchResult> results = new ArrayList<>();
        for (Document doc : getEntriesCollection().aggregate(pipeline)) {
            String entryId;
            String conversationId;
            double score;
            String indexedContent;

            if (groupByConversation) {
                entryId = doc.getString("entryId");
                conversationId = doc.getString("_id");
                score = doc.getDouble("score");
                indexedContent = doc.getString("indexedContent");
            } else {
                entryId = doc.getString("_id");
                conversationId = doc.getString("conversationId");
                score = doc.getDouble("score");
                indexedContent = doc.getString("indexedContent");
            }

            // Generate highlight from indexed content
            String highlight = extractHighlight(indexedContent, query);

            results.add(new FullTextSearchResult(entryId, conversationId, score, highlight));
        }

        return results;
    }

    private Set<String> getConversationIdsForAdmin(String userId, boolean includeDeleted) {
        Set<String> conversationIds = new java.util.HashSet<>();

        List<Bson> filters = new ArrayList<>();
        if (userId != null && !userId.isBlank()) {
            filters.add(Filters.eq("ownerUserId", userId));
        }
        if (!includeDeleted) {
            filters.add(Filters.eq("deletedAt", null));
        }

        Bson filter = filters.isEmpty() ? new Document() : Filters.and(filters);
        for (Document doc : getConversationsCollection().find(filter)) {
            String conversationId = doc.getString("_id");
            if (conversationId != null) {
                conversationIds.add(conversationId);
            }
        }
        return conversationIds;
    }

    /** Result from full-text search. */
    public record FullTextSearchResult(
            String entryId, String conversationId, double score, String highlight) {}
}
