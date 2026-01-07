export const NEW_CONVERSATION_ID = "new";

export function isNewConversationId(conversationId: string | null | undefined): boolean {
  return conversationId === NEW_CONVERSATION_ID;
}

export function generateConversationId(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `session-${Date.now()}`;
}
