package io.github.chirino.memory.persistence.entity;

import io.github.chirino.memory.model.AccessLevel;
import jakarta.persistence.AttributeConverter;
import jakarta.persistence.Converter;

@Converter(autoApply = true)
public class AccessLevelConverter implements AttributeConverter<AccessLevel, String> {

    @Override
    public String convertToDatabaseColumn(AccessLevel attribute) {
        return attribute == null ? null : attribute.name().toLowerCase();
    }

    @Override
    public AccessLevel convertToEntityAttribute(String dbData) {
        return dbData == null ? null : AccessLevel.valueOf(dbData.toUpperCase());
    }
}
