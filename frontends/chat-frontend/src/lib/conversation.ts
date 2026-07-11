export const NEW_CONVERSATION_ID = "new";

export function isNewConversationId(conversationId: string | null | undefined): boolean {
  return conversationId === NEW_CONVERSATION_ID;
}

export function generateConversationId(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `session-${Date.now()}`;
}

/**
 * Information about a fork option that branches at a specific entry.
 */
export interface ForkOption {
  /** The conversation ID of the forked conversation */
  conversationId: string;
  /** The conversation ID from which this fork originated */
  forkedAtConversationId?: string | null;
  /** The entry ID where the fork branches (the first parent entry excluded by the fork) */
  forkedAtEntryId?: string | null;
  /** When the fork was created */
  createdAt?: string | null;
  /** Preview text/label for the fork */
  label?: string;
  /** Whether this option is active in the requested conversation. */
  active?: boolean;
}
