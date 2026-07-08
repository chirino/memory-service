import type { Entry } from "@/api/generated/types.gen";

/**
 * Returns all context-channel entries from `allEntries[0..lastGroupEntry]`
 * whose epoch matches the last entry in the visual group.
 */
export function getLlmContextEntries(groupEntries: Entry[], allEntries: Entry[]): Entry[] {
  if (groupEntries.length === 0) {
    return [];
  }

  const lastGroupEntry = groupEntries[groupEntries.length - 1];
  const lastGroupEntryIndex = allEntries.findIndex((entry) => entry.id === lastGroupEntry.id);
  return collectContextEntriesByEpoch(allEntries, lastGroupEntryIndex, lastGroupEntry.epoch);
}

function collectContextEntriesByEpoch(entries: Entry[], upToIndex: number, epoch: number | undefined): Entry[] {
  const result: Entry[] = [];
  for (let i = 0; i <= upToIndex; i++) {
    const entry = entries[i];
    if (entry.channel === "context" && entry.epoch === epoch) {
      result.push(entry);
    }
  }
  return result;
}

// Made with Bob
