package io.github.chirino.memory.security;

import static org.junit.jupiter.api.Assertions.assertEquals;

import io.github.chirino.memory.model.AccessLevel;
import org.junit.jupiter.api.Test;

class SpiceDbAuthorizationServiceTest {

    @Test
    void access_level_maps_to_correct_permission() {
        assertEquals(
                "can_own", SpiceDbAuthorizationService.accessLevelToPermission(AccessLevel.OWNER));
        assertEquals(
                "can_manage",
                SpiceDbAuthorizationService.accessLevelToPermission(AccessLevel.MANAGER));
        assertEquals(
                "can_write",
                SpiceDbAuthorizationService.accessLevelToPermission(AccessLevel.WRITER));
        assertEquals(
                "can_read",
                SpiceDbAuthorizationService.accessLevelToPermission(AccessLevel.READER));
    }

    @Test
    void access_level_maps_to_correct_relation() {
        assertEquals("owner", SpiceDbAuthorizationService.accessLevelToRelation(AccessLevel.OWNER));
        assertEquals(
                "manager", SpiceDbAuthorizationService.accessLevelToRelation(AccessLevel.MANAGER));
        assertEquals(
                "writer", SpiceDbAuthorizationService.accessLevelToRelation(AccessLevel.WRITER));
        assertEquals(
                "reader", SpiceDbAuthorizationService.accessLevelToRelation(AccessLevel.READER));
    }
}
