import type { Entry } from "@/api/generated/types.gen";
import { CopyButton } from "@/components/ui/copy-button";
import { getLlmContextEntries } from "./LlmContext";
import { JsonHighlight, formatJson } from "./JsonHighlight";

export interface LlmContextRendererProps {
  /**
   * The context entries that form this visual group (consecutive context entries
   * following a user history entry).
   */
  groupEntries: Entry[];
  /**
   * All entries in the conversation list (used to find context entries from
   * earlier groups that share the same epoch, since an epoch can span groups).
   */
  allEntries: Entry[];
}

/**
 * Renders the LLM context view for a group of context entries.
 *
 * Algorithm:
 * 1. Take the epoch of the last context entry in the group (the "dominant" epoch).
 * 2. Scan allEntries from the start up to and including the last group entry,
 *    collecting every context-channel entry that shares that epoch.
 * 3. Flatten all their content arrays into a single message list.
 * 4. Render the combined message list as JSON.
 *
 * This handles epochs that span multiple groups (e.g. tool-call round-trips that
 * append new context entries before the agent's final reply).
 */
export function LlmContextRenderer({ groupEntries, allEntries }: LlmContextRendererProps) {
  if (groupEntries.length === 0) {
    return <div className="py-2 text-sm italic text-muted-foreground">No context entries in this group.</div>;
  }

  const epochEntries = getLlmContextEntries(groupEntries, allEntries);
  const epoch = groupEntries[groupEntries.length - 1]?.epoch;

  if (epochEntries.length === 0) {
    return (
      <div className="py-2 text-sm italic text-muted-foreground">
        No context entries found for epoch {epoch ?? "(unset)"}.
      </div>
    );
  }

  // Aggregate messages by concatenating all epoch entry content arrays in order.
  // Each entry contributes incremental messages (e.g. entry 1: [SYSTEM, USER],
  // entry 2: [AI response]). The final context is the full sequence.
  const messages: unknown[] = [];
  for (const epochEntry of epochEntries) {
    const content = epochEntry.content as unknown[];
    if (Array.isArray(content)) {
      messages.push(...content);
    }
  }

  if (messages.length === 0) {
    return <div className="py-2 text-sm italic text-muted-foreground">Context entries have no messages.</div>;
  }

  return (
    <div className="console-code group relative overflow-x-auto rounded-lg p-3">
      <CopyButton
        value={formatJson(messages)}
        iconSize={3.5}
        className="absolute right-0 top-0 z-10 opacity-0 transition-opacity group-hover:opacity-100"
      />
      <JsonHighlight value={messages} className="overflow-x-auto whitespace-pre-wrap font-mono text-sm" />
    </div>
  );
}

// Made with Bob
