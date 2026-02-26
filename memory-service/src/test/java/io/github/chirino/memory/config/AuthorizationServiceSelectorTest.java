package io.github.chirino.memory.config;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;

import io.github.chirino.memory.security.AuthorizationService;
import io.github.chirino.memory.security.LocalAuthorizationService;
import io.github.chirino.memory.security.SpiceDbAuthorizationService;
import org.junit.jupiter.api.Test;

class AuthorizationServiceSelectorTest {

    private AuthorizationServiceSelector createSelector() {
        AuthorizationServiceSelector selector = new AuthorizationServiceSelector();
        selector.localAuthzService = TestInstance.of(new LocalAuthorizationService());
        selector.spiceDbAuthzService = TestInstance.of(new SpiceDbAuthorizationService());
        return selector;
    }

    @Test
    void defaults_to_local() {
        AuthorizationServiceSelector selector = createSelector();
        selector.authzType = "local";

        AuthorizationService selected = selector.getAuthorizationService();
        assertInstanceOf(LocalAuthorizationService.class, selected);
    }

    @Test
    void defaults_to_local_when_null() {
        AuthorizationServiceSelector selector = createSelector();
        selector.authzType = null;

        AuthorizationService selected = selector.getAuthorizationService();
        assertInstanceOf(LocalAuthorizationService.class, selected);
    }

    @Test
    void selects_spicedb() {
        AuthorizationServiceSelector selector = createSelector();
        selector.authzType = "spicedb";

        AuthorizationService selected = selector.getAuthorizationService();
        assertInstanceOf(SpiceDbAuthorizationService.class, selected);
    }

    @Test
    void selects_spicedb_case_insensitive() {
        AuthorizationServiceSelector selector = createSelector();
        selector.authzType = "SpiceDB";

        AuthorizationService selected = selector.getAuthorizationService();
        assertInstanceOf(SpiceDbAuthorizationService.class, selected);
    }

    @Test
    void unknown_type_falls_back_to_local() {
        AuthorizationServiceSelector selector = createSelector();
        selector.authzType = "unknown";

        AuthorizationService selected = selector.getAuthorizationService();
        assertInstanceOf(LocalAuthorizationService.class, selected);
    }
}
