package io.github.chirino.memory.security;

import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class AdminRoleResolver {

    @ConfigProperty(name = "memory-service.roles.admin.oidc.role")
    Optional<String> adminOidcRole;

    @ConfigProperty(name = "memory-service.roles.auditor.oidc.role")
    Optional<String> auditorOidcRole;

    @ConfigProperty(name = "memory-service.roles.admin.users")
    Optional<List<String>> adminUsers;

    @ConfigProperty(name = "memory-service.roles.auditor.users")
    Optional<List<String>> auditorUsers;

    @ConfigProperty(name = "memory-service.roles.admin.clients")
    Optional<List<String>> adminClients;

    @ConfigProperty(name = "memory-service.roles.auditor.clients")
    Optional<List<String>> auditorClients;

    @ConfigProperty(name = "memory-service.roles.indexer.oidc.role")
    Optional<String> indexerOidcRole;

    @ConfigProperty(name = "memory-service.roles.indexer.users")
    Optional<List<String>> indexerUsers;

    @ConfigProperty(name = "memory-service.roles.indexer.clients")
    Optional<List<String>> indexerClients;

    public boolean hasAdminRole(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        // OIDC role check
        if (adminOidcRole.isPresent() && identity.hasRole(adminOidcRole.get())) {
            return true;
        }
        // User-based check
        String userId = identity.getPrincipal().getName();
        if (adminUsers.isPresent() && adminUsers.get().contains(userId)) {
            return true;
        }
        // Client-based check
        if (apiKeyContext != null
                && apiKeyContext.hasValidApiKey()
                && adminClients.isPresent()
                && adminClients.get().contains(apiKeyContext.getClientId())) {
            return true;
        }
        return false;
    }

    public boolean hasAuditorRole(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        // Admin implies auditor
        if (hasAdminRole(identity, apiKeyContext)) {
            return true;
        }
        // OIDC role check
        if (auditorOidcRole.isPresent() && identity.hasRole(auditorOidcRole.get())) {
            return true;
        }
        // User-based check
        String userId = identity.getPrincipal().getName();
        if (auditorUsers.isPresent() && auditorUsers.get().contains(userId)) {
            return true;
        }
        // Client-based check
        if (apiKeyContext != null
                && apiKeyContext.hasValidApiKey()
                && auditorClients.isPresent()
                && auditorClients.get().contains(apiKeyContext.getClientId())) {
            return true;
        }
        return false;
    }

    public void requireAdmin(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        if (!hasAdminRole(identity, apiKeyContext)) {
            throw new io.github.chirino.memory.store.AccessDeniedException(
                    "Admin role required for this operation");
        }
    }

    public void requireAuditor(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        if (!hasAuditorRole(identity, apiKeyContext)) {
            throw new io.github.chirino.memory.store.AccessDeniedException(
                    "Auditor or admin role required for this operation");
        }
    }

    public boolean hasIndexerRole(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        // Admin implies indexer
        if (hasAdminRole(identity, apiKeyContext)) {
            return true;
        }
        // OIDC role check
        if (indexerOidcRole.isPresent() && identity.hasRole(indexerOidcRole.get())) {
            return true;
        }
        // User-based check
        String userId = identity.getPrincipal().getName();
        if (indexerUsers.isPresent() && indexerUsers.get().contains(userId)) {
            return true;
        }
        // Client-based check
        if (apiKeyContext != null
                && apiKeyContext.hasValidApiKey()
                && indexerClients.isPresent()
                && indexerClients.get().contains(apiKeyContext.getClientId())) {
            return true;
        }
        return false;
    }

    public void requireIndexer(SecurityIdentity identity, ApiKeyContext apiKeyContext) {
        if (!hasIndexerRole(identity, apiKeyContext)) {
            throw new io.github.chirino.memory.store.AccessDeniedException(
                    "Indexer or admin role required for this operation");
        }
    }
}
