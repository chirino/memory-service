import type { Entry, ConversationForkSummary } from "../client/types.gen";

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
}

/**
 * An entry with optional fork information showing where conversations branch.
 */
export interface EntryAndForkInfo {
  /** The entry data */
  entry: Entry;
  /** Forks that branch at this entry (i.e., this entry is the first one excluded in those forks) */
  forks?: ForkOption[];
}

/**
 * Helper interface for visualizing conversation forks.
 * Created by `createForkView` from entries and forks API responses.
 */
export interface ForkView {
  /** Returns all unique conversation IDs present in the entries */
  conversationIds(): string[];
  /**
   * Returns entries for a specific conversation with fork information attached.
   * Includes entries from ancestor conversations up to each fork point,
   * providing the full conversation history as seen from this conversation.
   */
  entries(conversationId: string): EntryAndForkInfo[];
}

/**
 * Creates a ForkView to help visualize conversation forks.
 *
 * This function processes the results from the `/entries` and `/forks` API calls
 * and provides methods to easily access entries grouped by conversation with
 * fork branch points annotated.
 *
 * Fork semantics: When a fork has `forkedAtEntryId = X`, it means the fork
 * excludes X and everything after it from the parent conversation.
 * Therefore, X itself is where the fork "branches" and gets the fork annotation.
 * Blank-slate forks use `forkedAtEntryId = null` and are rendered before
 * the first visible entry.
 *
 * @param entries - Array of entries (typically from `/entries?forks=all`)
 * @param forks - Array of fork summaries (from `/forks`)
 * @returns A ForkView object with methods to access entries and fork info
 *
 * @example
 * ```typescript
 * const entries = await listConversationEntries({ conversationId, forks: 'all' });
 * const forks = await listConversationForks({ conversationId });
 * const view = createForkView(entries.data, forks.data);
 *
 * // Get all conversation IDs
 * const convIds = view.conversationIds();
 *
 * // Get entries for a specific conversation with fork info
 * for (const item of view.entries(convIds[0])) {
 *   console.log(item.entry.id);
 *   if (item.forks) {
 *     console.log(`  Has ${item.forks.length} fork(s)`);
 *   }
 * }
 * ```
 */
export function createForkView(entries: Entry[], forks: ConversationForkSummary[]): ForkView {
  const forksByEntryId = new Map<string, ForkOption[]>();
  const forksByConversationId = new Map<string, ConversationForkSummary>();
  for (const fork of forks) {
    const key = fork.forkedAtEntryId || "";
    if (!forksByEntryId.has(key)) {
      forksByEntryId.set(key, []);
    }
    if (fork.conversationId) {
      forksByConversationId.set(fork.conversationId, fork);
    }
  }

  const entriesByConversation = new Map<string, Entry[]>();
  for (const entry of entries) {
    const convId = entry.conversationId;
    if (!entriesByConversation.has(convId)) {
      entriesByConversation.set(convId, []);
    }
    entriesByConversation.get(convId)!.push(entry);
  }

  for (const [conversationId, forkSummary] of forksByConversationId) {
    // Skip root conversations — they are not forks
    if (!forkSummary.forkedAtConversationId) {
      continue;
    }

    const convEntries = entriesByConversation.get(conversationId);
    if (!convEntries || convEntries.length === 0) {
      continue;
    }

    const forkEntries = forksByEntryId.get(forkSummary.forkedAtEntryId || "");
    if (!forkEntries) {
      continue;
    }

    // Add child fork as an option at the fork point
    const firstEntry = convEntries[0];
    const content = firstEntry.content[0] as { text?: string } | undefined;
    forkEntries.push({
      forkedAtConversationId: forkSummary.forkedAtConversationId,
      forkedAtEntryId: forkSummary.forkedAtEntryId,
      conversationId,
      createdAt: firstEntry.createdAt,
      label: content?.text,
    });

    // Add parent conversation as a sibling option so the user can switch back
    const parentConvId = forkSummary.forkedAtConversationId;
    if (!forkEntries.some((f) => f.conversationId === parentConvId)) {
      const parentEntries = entriesByConversation.get(parentConvId);
      const forkPointEntry = parentEntries?.find((e) => e.id === forkSummary.forkedAtEntryId);
      const labelEntry = forkPointEntry ?? parentEntries?.[0];
      const parentContent = labelEntry?.content[0] as { text?: string } | undefined;
      forkEntries.push({
        forkedAtConversationId: null,
        forkedAtEntryId: forkSummary.forkedAtEntryId,
        conversationId: parentConvId,
        createdAt: labelEntry?.createdAt,
        label: parentContent?.text,
      });
    }
  }

  /**
   * Recursively get entries for a conversation, including ancestor entries.
   * @param conversationId - The conversation to get entries for
   * @param untilEntryId - If provided, include entries before this entry ID.
   *                       If null/undefined, include all entries from this conversation.
   */
  function getEntries(conversationId: string, untilEntryId?: string | null): Entry[] {
    const result: Entry[] = [];

    // First, recursively get parent entries if this conversation is a fork
    const meta = forksByConversationId.get(conversationId);
    if (meta?.forkedAtConversationId) {
      result.push(...getEntries(meta.forkedAtConversationId, meta.forkedAtEntryId));
    }

    // Then add entries from this conversation
    const convEntries = entriesByConversation.get(conversationId) ?? [];
    for (const entry of convEntries) {
      if (entry.id === untilEntryId) {
        break;
      }
      result.push(entry);
    }

    return result;
  }

  return {
    conversationIds(): string[] {
      return Array.from(entriesByConversation.keys()).sort();
    },

    entries(conversationId: string): EntryAndForkInfo[] {
      const combinedEntries = getEntries(conversationId);

      // When the fork point entry is excluded from combinedEntries (exclusive-stop
      // semantics), its fork annotations are orphaned. Attach them to the first
      // entry owned by this conversation (the divergence point).
      const meta = forksByConversationId.get(conversationId);
      const forkPointEntryId = meta?.forkedAtEntryId;
      const combinedIds = new Set(combinedEntries.map((e) => e.id));
      const orphanedForkPointId = forkPointEntryId && !combinedIds.has(forkPointEntryId) ? forkPointEntryId : null;
      const orphanTargetIndex = orphanedForkPointId
        ? Math.max(
            0,
            combinedEntries.findIndex((e) => e.conversationId === conversationId),
          )
        : -1;

      return combinedEntries.map((entry, index) => {
        const orphanedForks =
          orphanedForkPointId && index === orphanTargetIndex ? (forksByEntryId.get(orphanedForkPointId) ?? []) : [];
        const entryForks = forksByEntryId.get(entry.id) ?? [];
        const blankSlateForks = index === 0 ? (forksByEntryId.get("") ?? []) : [];
        const allForks = [...blankSlateForks, ...orphanedForks, ...entryForks];
        return {
          entry,
          forks: allForks.length > 0 ? allForks : undefined,
        };
      });
    },
  };
}
