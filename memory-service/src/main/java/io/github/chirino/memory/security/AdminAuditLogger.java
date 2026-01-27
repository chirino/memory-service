package io.github.chirino.memory.security;

import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.Map;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@ApplicationScoped
public class AdminAuditLogger {

    private static final Logger ADMIN_AUDIT =
            Logger.getLogger("io.github.chirino.memory.admin.audit");

    @Inject AdminRoleResolver roleResolver;

    @ConfigProperty(name = "memory-service.admin.require-justification", defaultValue = "false")
    boolean requireJustification;

    public void logRead(
            String action,
            Map<String, Object> params,
            String justification,
            SecurityIdentity identity,
            ApiKeyContext apiKeyContext) {
        validateJustification(justification);
        String userId = identity.getPrincipal().getName();
        String role = determineRole(identity, apiKeyContext);
        String clientId =
                apiKeyContext != null && apiKeyContext.hasValidApiKey()
                        ? apiKeyContext.getClientId()
                        : null;
        String justificationText =
                justification != null && !justification.isBlank() ? justification : "<none>";
        String clientInfo = clientId != null ? " client=" + clientId : "";
        ADMIN_AUDIT.infof(
                "ADMIN_READ user=%s role=%s%s action=%s params=%s justification=\"%s\"",
                userId, role, clientInfo, action, params, justificationText);
    }

    public void logWrite(
            String action,
            String target,
            String justification,
            SecurityIdentity identity,
            ApiKeyContext apiKeyContext) {
        validateJustification(justification);
        String userId = identity.getPrincipal().getName();
        String role = determineRole(identity, apiKeyContext);
        String clientId =
                apiKeyContext != null && apiKeyContext.hasValidApiKey()
                        ? apiKeyContext.getClientId()
                        : null;
        String justificationText =
                justification != null && !justification.isBlank() ? justification : "<none>";
        String clientInfo = clientId != null ? " client=" + clientId : "";
        ADMIN_AUDIT.infof(
                "ADMIN_WRITE user=%s role=%s%s action=%s target=%s justification=\"%s\"",
                userId, role, clientInfo, action, target, justificationText);
    }

    private void validateJustification(String justification) {
        if (requireJustification && (justification == null || justification.isBlank())) {
            throw new JustificationRequiredException();
        }
    }

    private String determineRole(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        if (roleResolver.hasAdminRole(identity, apiKeyContext)) {
            return "admin";
        } else if (roleResolver.hasAuditorRole(identity, apiKeyContext)) {
            return "auditor";
        }
        return "unknown";
    }
}
