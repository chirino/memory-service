/** A conversation continuation displayed at a visible fork point. */
export interface ForkOption {
  conversationId: string;
  forkedAtConversationId?: string | null;
  /** Entry displayed for this continuation at the fork point. */
  entryId?: string | null;
  createdAt?: string | null;
  label?: string;
  title?: string;
  isActive?: boolean;
}
