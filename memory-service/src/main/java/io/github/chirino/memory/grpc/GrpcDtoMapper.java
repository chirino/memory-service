package io.github.chirino.memory.grpc;

import static io.github.chirino.memory.grpc.UuidUtils.stringToByteString;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.BooleanNode;
import com.fasterxml.jackson.databind.node.NullNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.fasterxml.jackson.databind.node.TextNode;
import com.google.protobuf.ListValue;
import com.google.protobuf.NullValue;
import com.google.protobuf.Struct;
import com.google.protobuf.Value;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.grpc.v1.Conversation;
import io.github.chirino.memory.grpc.v1.ConversationForkSummary;
import io.github.chirino.memory.grpc.v1.ConversationMembership;
import io.github.chirino.memory.grpc.v1.ConversationSummary;
import io.github.chirino.memory.grpc.v1.Entry;
import io.github.chirino.memory.grpc.v1.SearchResult;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.model.Channel;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

public final class GrpcDtoMapper {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private GrpcDtoMapper() {}

    public static ConversationSummary toProto(ConversationSummaryDto dto) {
        if (dto == null) {
            return null;
        }
        return ConversationSummary.newBuilder()
                .setId(stringToByteString(dto.getId()))
                .setTitle(dto.getTitle() == null ? "" : dto.getTitle())
                .setOwnerUserId(dto.getOwnerUserId() == null ? "" : dto.getOwnerUserId())
                .setCreatedAt(dto.getCreatedAt() == null ? "" : dto.getCreatedAt())
                .setUpdatedAt(dto.getUpdatedAt() == null ? "" : dto.getUpdatedAt())
                .setLastMessagePreview(
                        dto.getLastMessagePreview() == null ? "" : dto.getLastMessagePreview())
                .setAccessLevel(accessLevelToProto(dto.getAccessLevel()))
                .build();
    }

    public static Conversation toProto(ConversationDto dto) {
        if (dto == null) {
            return null;
        }
        return Conversation.newBuilder()
                .setId(stringToByteString(dto.getId()))
                .setTitle(dto.getTitle() == null ? "" : dto.getTitle())
                .setOwnerUserId(dto.getOwnerUserId() == null ? "" : dto.getOwnerUserId())
                .setCreatedAt(dto.getCreatedAt() == null ? "" : dto.getCreatedAt())
                .setUpdatedAt(dto.getUpdatedAt() == null ? "" : dto.getUpdatedAt())
                .setLastMessagePreview(
                        dto.getLastMessagePreview() == null ? "" : dto.getLastMessagePreview())
                .setAccessLevel(accessLevelToProto(dto.getAccessLevel()))
                // conversation_group_id is not exposed in API responses
                .setForkedAtEntryId(stringToByteString(dto.getForkedAtEntryId()))
                .setForkedAtConversationId(stringToByteString(dto.getForkedAtConversationId()))
                .build();
    }

    public static ConversationMembership toProto(
            ConversationMembershipDto dto, String conversationId) {
        if (dto == null) {
            return null;
        }
        return ConversationMembership.newBuilder()
                .setConversationId(stringToByteString(conversationId))
                .setUserId(dto.getUserId() == null ? "" : dto.getUserId())
                .setAccessLevel(accessLevelToProto(dto.getAccessLevel()))
                .setCreatedAt(dto.getCreatedAt() == null ? "" : dto.getCreatedAt())
                .build();
    }

    public static ConversationForkSummary toProto(ConversationForkSummaryDto dto) {
        if (dto == null) {
            return null;
        }
        return ConversationForkSummary.newBuilder()
                .setConversationId(stringToByteString(dto.getConversationId()))
                // conversation_group_id is not exposed in API responses
                .setForkedAtEntryId(stringToByteString(dto.getForkedAtEntryId()))
                .setForkedAtConversationId(stringToByteString(dto.getForkedAtConversationId()))
                .setTitle(dto.getTitle() == null ? "" : dto.getTitle())
                .setCreatedAt(dto.getCreatedAt() == null ? "" : dto.getCreatedAt())
                .build();
    }

    public static Entry toProto(EntryDto dto) {
        if (dto == null) {
            return null;
        }
        Entry.Builder builder =
                Entry.newBuilder()
                        .setId(stringToByteString(dto.getId()))
                        .setConversationId(stringToByteString(dto.getConversationId()))
                        .setUserId(dto.getUserId() == null ? "" : dto.getUserId())
                        .setChannel(toProtoChannel(dto.getChannel()))
                        .setContentType(dto.getContentType() == null ? "" : dto.getContentType())
                        .addAllContent(toValues(dto.getContent()))
                        .setCreatedAt(dto.getCreatedAt() == null ? "" : dto.getCreatedAt());
        if (dto.getEpoch() != null) {
            builder.setEpoch(dto.getEpoch());
        }
        return builder.build();
    }

    public static SearchResult toProto(SearchResultDto dto) {
        if (dto == null) {
            return null;
        }
        return SearchResult.newBuilder()
                .setEntry(toProto(dto.getEntry()))
                .setScore((float) dto.getScore())
                .setHighlights(dto.getHighlights() == null ? "" : dto.getHighlights())
                .build();
    }

    public static List<Value> toValues(List<Object> objects) {
        if (objects == null || objects.isEmpty()) {
            return Collections.emptyList();
        }
        return objects.stream().map(GrpcDtoMapper::objectToValue).collect(Collectors.toList());
    }

    public static List<Object> fromValues(List<Value> values) {
        if (values == null || values.isEmpty()) {
            return Collections.emptyList();
        }
        return values.stream().map(GrpcDtoMapper::valueToObject).collect(Collectors.toList());
    }

    public static Struct mapToStruct(Map<String, Object> data) {
        if (data == null || data.isEmpty()) {
            return null;
        }
        Struct.Builder builder = Struct.newBuilder();
        for (Map.Entry<String, Object> entry : data.entrySet()) {
            builder.putFields(entry.getKey(), objectToValue(entry.getValue()));
        }
        return builder.build();
    }

    public static Map<String, Object> structToMap(Struct struct) {
        if (struct == null || struct.getFieldsCount() == 0) {
            return null;
        }
        Map<String, Object> result = new LinkedHashMap<>();
        struct.getFieldsMap().forEach((key, value) -> result.put(key, valueToObject(value)));
        return result;
    }

    public static AccessLevel accessLevelFromProto(
            io.github.chirino.memory.grpc.v1.AccessLevel accessLevel) {
        if (accessLevel == null) {
            return null;
        }
        return switch (accessLevel) {
            case OWNER -> AccessLevel.OWNER;
            case MANAGER -> AccessLevel.MANAGER;
            case WRITER -> AccessLevel.WRITER;
            case READER -> AccessLevel.READER;
            default -> null;
        };
    }

    public static io.github.chirino.memory.grpc.v1.AccessLevel accessLevelToProto(
            AccessLevel accessLevel) {
        if (accessLevel == null) {
            return io.github.chirino.memory.grpc.v1.AccessLevel.ACCESS_LEVEL_UNSPECIFIED;
        }
        return switch (accessLevel) {
            case OWNER -> io.github.chirino.memory.grpc.v1.AccessLevel.OWNER;
            case MANAGER -> io.github.chirino.memory.grpc.v1.AccessLevel.MANAGER;
            case WRITER -> io.github.chirino.memory.grpc.v1.AccessLevel.WRITER;
            case READER -> io.github.chirino.memory.grpc.v1.AccessLevel.READER;
        };
    }

    public static io.github.chirino.memory.grpc.v1.Channel toProtoChannel(Channel channel) {
        if (channel == null) {
            return io.github.chirino.memory.grpc.v1.Channel.CHANNEL_UNSPECIFIED;
        }
        return switch (channel) {
            case HISTORY -> io.github.chirino.memory.grpc.v1.Channel.HISTORY;
            case MEMORY -> io.github.chirino.memory.grpc.v1.Channel.MEMORY;
            case TRANSCRIPT -> io.github.chirino.memory.grpc.v1.Channel.TRANSCRIPT;
        };
    }

    public static Channel fromProtoChannel(io.github.chirino.memory.grpc.v1.Channel channel) {
        if (channel == null) {
            return null;
        }
        return switch (channel) {
            case HISTORY -> Channel.HISTORY;
            case MEMORY -> Channel.MEMORY;
            case TRANSCRIPT -> Channel.TRANSCRIPT;
            default -> null;
        };
    }

    public static CreateEntryRequest.ChannelEnum toCreateEntryChannel(Channel channel) {
        if (channel == null) {
            return null;
        }
        return switch (channel) {
            case HISTORY -> CreateEntryRequest.ChannelEnum.HISTORY;
            case MEMORY -> CreateEntryRequest.ChannelEnum.MEMORY;
            case TRANSCRIPT -> CreateEntryRequest.ChannelEnum.TRANSCRIPT;
        };
    }

    private static Value objectToValue(Object object) {
        if (object == null) {
            return Value.newBuilder().setNullValue(NullValue.NULL_VALUE).build();
        }
        JsonNode node = OBJECT_MAPPER.valueToTree(object);
        return jsonNodeToValue(node);
    }

    private static Object valueToObject(Value value) {
        if (value == null) {
            return null;
        }
        JsonNode node = valueToJsonNode(value);
        try {
            return OBJECT_MAPPER.treeToValue(node, Object.class);
        } catch (JsonProcessingException e) {
            throw new IllegalStateException("Failed to decode value", e);
        }
    }

    private static JsonNode valueToJsonNode(Value value) {
        if (value == null) {
            return NullNode.instance;
        }
        switch (value.getKindCase()) {
            case NULL_VALUE:
                return NullNode.instance;
            case BOOL_VALUE:
                return BooleanNode.valueOf(value.getBoolValue());
            case NUMBER_VALUE:
                return OBJECT_MAPPER.getNodeFactory().numberNode(value.getNumberValue());
            case STRING_VALUE:
                return TextNode.valueOf(value.getStringValue());
            case STRUCT_VALUE:
                {
                    ObjectNode objectNode = OBJECT_MAPPER.createObjectNode();
                    value.getStructValue()
                            .getFieldsMap()
                            .forEach(
                                    (key, nestedValue) ->
                                            objectNode.set(key, valueToJsonNode(nestedValue)));
                    return objectNode;
                }
            case LIST_VALUE:
                {
                    ArrayNode arrayNode = OBJECT_MAPPER.createArrayNode();
                    value.getListValue()
                            .getValuesList()
                            .forEach(v -> arrayNode.add(valueToJsonNode(v)));
                    return arrayNode;
                }
            default:
                return NullNode.instance;
        }
    }

    private static Value jsonNodeToValue(JsonNode node) {
        if (node == null || node.isNull()) {
            return Value.newBuilder().setNullValue(NullValue.NULL_VALUE).build();
        }
        if (node.isObject()) {
            Struct.Builder struct = Struct.newBuilder();
            node.properties()
                    .forEach(
                            entry ->
                                    struct.putFields(
                                            entry.getKey(), jsonNodeToValue(entry.getValue())));
            return Value.newBuilder().setStructValue(struct).build();
        }
        if (node.isArray()) {
            ListValue.Builder list = ListValue.newBuilder();
            node.forEach(child -> list.addValues(jsonNodeToValue(child)));
            return Value.newBuilder().setListValue(list).build();
        }
        if (node.isTextual()) {
            return Value.newBuilder().setStringValue(node.textValue()).build();
        }
        if (node.isBoolean()) {
            return Value.newBuilder().setBoolValue(node.booleanValue()).build();
        }
        if (node.isNumber()) {
            return Value.newBuilder().setNumberValue(node.doubleValue()).build();
        }
        return Value.newBuilder().setNullValue(NullValue.NULL_VALUE).build();
    }
}
