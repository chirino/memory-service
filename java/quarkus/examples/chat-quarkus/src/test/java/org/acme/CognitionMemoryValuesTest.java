package org.acme;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import jakarta.ws.rs.BadRequestException;
import jakarta.ws.rs.WebApplicationException;
import jakarta.ws.rs.core.Response;
import java.util.List;
import org.junit.jupiter.api.Test;

class CognitionMemoryValuesTest {

    @Test
    void normalizesDeduplicatesAndDropsBlankInputs() {
        List<String> inputs =
                CognitionMemoryValues.normalizeInputs(
                        List.of(
                                "  Prefer iterative code  ",
                                "",
                                "Prefer   iterative code",
                                "Use Java"),
                        10,
                        100);

        assertEquals(List.of("Prefer iterative code", "Use Java"), inputs);
    }

    @Test
    void rejectsNonStringInputs() {
        assertThrows(
                BadRequestException.class,
                () -> CognitionMemoryValues.normalizeInputs(List.of("valid", 7), 10, 100));
    }

    @Test
    void rejectsTooManyInputsAfterDeduplication() {
        assertThrows(
                BadRequestException.class,
                () -> CognitionMemoryValues.normalizeInputs(List.of("a", "b"), 1, 100));
    }

    @Test
    void namespaceEndsWithUsesFinalSegmentOnly() {
        assertTrue(
                CognitionMemoryValues.namespaceEndsWith(
                        List.of("user", "bob", "cognition.v1", "profile_input"), "profile_input"));
    }

    @Test
    void detectsNestedHttpNotFoundException() {
        RuntimeException wrapped =
                new RuntimeException(new WebApplicationException(Response.status(404).build()));

        assertTrue(CognitionMemoryValues.isHttpNotFound(wrapped));
    }
}
