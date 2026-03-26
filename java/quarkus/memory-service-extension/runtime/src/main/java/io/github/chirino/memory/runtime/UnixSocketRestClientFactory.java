package io.github.chirino.memory.runtime;

import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.api.SearchApi;
import io.github.chirino.memory.client.api.SharingApi;
import io.github.chirino.memory.client.model.UpdateConversationRequest;
import io.github.chirino.memory.runtime.UnixSocketHttpClient.HttpResponseData;
import jakarta.ws.rs.core.Response;
import java.io.IOException;
import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.UUID;

final class UnixSocketRestClientFactory {

    private UnixSocketRestClientFactory() {}

    @SuppressWarnings("unchecked")
    static <T> T create(
            Class<T> apiClass,
            String unixSocketPath,
            ObjectMapper objectMapper,
            String apiKey,
            String bearerToken) {
        UnixSocketHttpClient client =
                new UnixSocketHttpClient(unixSocketPath, objectMapper, apiKey, bearerToken);
        InvocationHandler handler = new Handler(apiClass, client, objectMapper);
        return (T)
                Proxy.newProxyInstance(
                        apiClass.getClassLoader(), new Class<?>[] {apiClass}, handler);
    }

    private static final class Handler implements InvocationHandler {
        private final Class<?> apiClass;
        private final UnixSocketHttpClient client;
        private final ObjectMapper objectMapper;

        private Handler(Class<?> apiClass, UnixSocketHttpClient client, ObjectMapper objectMapper) {
            this.apiClass = apiClass;
            this.client = client;
            this.objectMapper = objectMapper;
        }

        @Override
        public Object invoke(Object proxy, Method method, Object[] args) throws Throwable {
            if (method.getDeclaringClass() == Object.class) {
                return switch (method.getName()) {
                    case "toString" -> apiClass.getSimpleName() + "UnixSocketClient";
                    case "hashCode" -> System.identityHashCode(proxy);
                    case "equals" -> proxy == args[0];
                    default -> method.invoke(this, args);
                };
            }
            if (apiClass == ConversationsApi.class) {
                return invokeConversations(method, args);
            }
            if (apiClass == SharingApi.class) {
                return invokeSharing(method, args);
            }
            if (apiClass == SearchApi.class) {
                return invokeSearch(method, args);
            }
            throw new IllegalArgumentException(
                    "Unsupported unix socket api: " + apiClass.getName());
        }

        private Object invokeConversations(Method method, Object[] args) throws IOException {
            return switch (method.getName()) {
                case "appendConversationEntry" ->
                        json(
                                "POST",
                                path("/v1/conversations/%s/entries", args[0]),
                                null,
                                args[1],
                                method);
                case "createConversation" ->
                        json("POST", "/v1/conversations", null, args[0], method);
                case "deleteConversation" ->
                        json(
                                "PATCH",
                                path("/v1/conversations/%s", args[0]),
                                null,
                                new UpdateConversationRequest().archived(Boolean.TRUE),
                                method);
                case "deleteConversationResponse" ->
                        response(
                                "DELETE",
                                path("/v1/conversations/%s/response", args[0]),
                                null,
                                null);
                case "getConversation" ->
                        json("GET", path("/v1/conversations/%s", args[0]), null, null, method);
                case "listConversationEntries" ->
                        json(
                                "GET",
                                path("/v1/conversations/%s/entries", args[0]),
                                query(
                                        "afterCursor", args[1],
                                        "limit", args[2],
                                        "channel", args[3],
                                        "epoch", args[4],
                                        "forks", args[5]),
                                null,
                                method);
                case "listConversationChildren" ->
                        json(
                                "GET",
                                path("/v1/conversations/%s/children", args[0]),
                                query("afterCursor", args[1], "limit", args[2]),
                                null,
                                method);
                case "listConversationForks" ->
                        json(
                                "GET",
                                path("/v1/conversations/%s/forks", args[0]),
                                query("afterCursor", args[1], "limit", args[2]),
                                null,
                                method);
                case "listConversations" ->
                        json(
                                "GET",
                                "/v1/conversations",
                                query(
                                        "mode", args[0],
                                        "ancestry", args[1],
                                        "afterCursor", args[2],
                                        "limit", args[3],
                                        "query", args[4],
                                        "archived", args[5]),
                                null,
                                method);
                case "syncConversationContext" ->
                        json(
                                "POST",
                                path("/v1/conversations/%s/entries/sync", args[0]),
                                null,
                                args[1],
                                method);
                case "updateConversation" ->
                        json("PATCH", path("/v1/conversations/%s", args[0]), null, args[1], method);
                default ->
                        throw new IllegalArgumentException(
                                "Unsupported conversations method: " + method.getName());
            };
        }

        private Object invokeSharing(Method method, Object[] args) throws IOException {
            return switch (method.getName()) {
                case "acceptTransfer" ->
                        response(
                                "POST",
                                path("/v1/ownership-transfers/%s/accept", args[0]),
                                null,
                                null);
                case "createOwnershipTransfer" ->
                        json("POST", "/v1/ownership-transfers", null, args[0], method);
                case "deleteConversationMembership" ->
                        response(
                                "DELETE",
                                path("/v1/conversations/%s/memberships/%s", args[0], args[1]),
                                null,
                                null);
                case "deleteTransfer" ->
                        response("DELETE", path("/v1/ownership-transfers/%s", args[0]), null, null);
                case "getTransfer" ->
                        json(
                                "GET",
                                path("/v1/ownership-transfers/%s", args[0]),
                                null,
                                null,
                                method);
                case "listConversationMemberships" ->
                        json(
                                "GET",
                                path("/v1/conversations/%s/memberships", args[0]),
                                query("afterCursor", args[1], "limit", args[2]),
                                null,
                                method);
                case "listPendingTransfers" ->
                        json(
                                "GET",
                                "/v1/ownership-transfers",
                                query("role", args[0], "afterCursor", args[1], "limit", args[2]),
                                null,
                                method);
                case "shareConversation" ->
                        json(
                                "POST",
                                path("/v1/conversations/%s/memberships", args[0]),
                                null,
                                args[1],
                                method);
                case "updateConversationMembership" ->
                        json(
                                "PATCH",
                                path("/v1/conversations/%s/memberships/%s", args[0], args[1]),
                                null,
                                args[2],
                                method);
                default ->
                        throw new IllegalArgumentException(
                                "Unsupported sharing method: " + method.getName());
            };
        }

        private Object invokeSearch(Method method, Object[] args) throws IOException {
            return switch (method.getName()) {
                case "indexConversations" ->
                        json("POST", "/v1/conversations/index", null, args[0], method);
                case "listUnindexedEntries" ->
                        json(
                                "GET",
                                "/v1/conversations/unindexed",
                                query("limit", args[0], "afterCursor", args[1]),
                                null,
                                method);
                case "searchConversations" ->
                        json("POST", "/v1/conversations/search", null, args[0], method);
                default ->
                        throw new IllegalArgumentException(
                                "Unsupported search method: " + method.getName());
            };
        }

        private Object json(
                String httpMethod, String path, Map<String, ?> query, Object body, Method method)
                throws IOException {
            HttpResponseData response = client.exchange(httpMethod, path, query, body);
            client.throwForError(response);
            JavaType javaType =
                    objectMapper.getTypeFactory().constructType(method.getGenericReturnType());
            return client.readJson(response, javaType);
        }

        private Response response(String httpMethod, String path, Map<String, ?> query, Object body)
                throws IOException {
            HttpResponseData response = client.exchange(httpMethod, path, query, body);
            if (response.statusCode() >= 400) {
                client.throwForError(response);
            }
            return client.toJaxrsResponse(response);
        }

        private static String path(String template, Object... args) {
            Object[] encoded = new Object[args.length];
            for (int i = 0; i < args.length; i++) {
                encoded[i] =
                        args[i] instanceof UUID uuid ? uuid.toString() : String.valueOf(args[i]);
            }
            return String.format(template, encoded);
        }

        private static Map<String, Object> query(Object... keyValues) {
            Map<String, Object> query = new LinkedHashMap<>();
            for (int i = 0; i + 1 < keyValues.length; i += 2) {
                Object value = keyValues[i + 1];
                if (value == null) {
                    continue;
                }
                query.put(
                        String.valueOf(keyValues[i]),
                        value instanceof UUID uuid ? uuid.toString() : value);
            }
            return query;
        }
    }
}
