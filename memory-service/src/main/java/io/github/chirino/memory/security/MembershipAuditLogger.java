package io.github.chirino.memory.security;

import io.github.chirino.memory.model.AccessLevel;
import jakarta.enterprise.context.ApplicationScoped;
import org.jboss.logging.Logger;

@ApplicationScoped
public class MembershipAuditLogger {

    private static final Logger AUDIT_LOG =
            Logger.getLogger("io.github.chirino.memory.membership.audit");

    /** Log a membership addition. */
    public void logAdd(
            String actorUserId,
            String conversationId,
            String targetUserId,
            AccessLevel accessLevel) {
        AUDIT_LOG.infof(
                "MEMBERSHIP_CHANGE actor=%s action=add conversation=%s target=%s accessLevel=%s",
                actorUserId, conversationId, targetUserId, accessLevel);
    }

    /** Log a membership update. */
    public void logUpdate(
            String actorUserId,
            String conversationId,
            String targetUserId,
            AccessLevel fromLevel,
            AccessLevel toLevel) {
        AUDIT_LOG.infof(
                "MEMBERSHIP_CHANGE actor=%s action=update conversation=%s target=%s fromLevel=%s"
                        + " toLevel=%s",
                actorUserId, conversationId, targetUserId, fromLevel, toLevel);
    }

    /** Log a membership removal. */
    public void logRemove(
            String actorUserId,
            String conversationId,
            String targetUserId,
            AccessLevel accessLevel) {
        AUDIT_LOG.infof(
                "MEMBERSHIP_CHANGE actor=%s action=remove conversation=%s target=%s accessLevel=%s",
                actorUserId, conversationId, targetUserId, accessLevel);
    }

    /** Log an ownership transfer. */
    public void logOwnershipTransfer(
            String actorUserId, String conversationId, String fromOwner, String toOwner) {
        AUDIT_LOG.infof(
                "MEMBERSHIP_CHANGE actor=%s action=transfer_ownership conversation=%s target=%s"
                        + " fromOwner=%s",
                actorUserId, conversationId, toOwner, fromOwner);
    }
}
