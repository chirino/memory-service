import type { EntryWithForkPoint } from "@/hooks";

export type ChannelFilter = "all" | "history" | "context" | "journal";

export type RenderItem =
  | { type: "entry"; entry: EntryWithForkPoint }
  | { type: "context-group"; entries: EntryWithForkPoint[] };

/**
 * Splits the entry list into individual entry cards and context-group blocks.
 * A context group is a run of consecutive context entries after at least one
 * non-context entry.
 */
export function buildRenderItems(entries: EntryWithForkPoint[]): RenderItem[] {
  const items: RenderItem[] = [];
  let contextRun: EntryWithForkPoint[] = [];
  let seenNonContext = false;

  const flushContextRun = () => {
    if (contextRun.length > 0) {
      items.push({ type: "context-group", entries: contextRun });
      contextRun = [];
    }
  };

  for (const entry of entries) {
    if (entry.channel === "context" && seenNonContext) {
      contextRun.push(entry);
    } else {
      flushContextRun();
      if (entry.channel !== "context") seenNonContext = true;
      items.push({ type: "entry", entry });
    }
  }
  flushContextRun();
  return items;
}

export function filterRenderItemsByChannel(items: RenderItem[], channelFilter: ChannelFilter): RenderItem[] {
  if (channelFilter === "all") {
    return items;
  }

  const filtered: RenderItem[] = [];
  for (const item of items) {
    if (item.type === "entry") {
      if (item.entry.channel === channelFilter) {
        filtered.push(channelFilter === "context" ? { type: "context-group", entries: [item.entry] } : item);
      }
      continue;
    }

    if (channelFilter === "context") {
      filtered.push(item);
    }
  }
  return filtered;
}
