import type { ChannelFilter } from "./entry-render-items";

export interface ForkNavigationSearch {
  entryId?: string;
  channel?: ChannelFilter;
}

export function buildForkNavigationSearch(
  continuationEntryId: string | null | undefined,
  channelFilter?: ChannelFilter,
): ForkNavigationSearch {
  const search: ForkNavigationSearch = {};
  if (channelFilter && channelFilter !== "all" && channelFilter !== "history") {
    search.channel = channelFilter;
  }
  if (continuationEntryId) {
    search.entryId = continuationEntryId;
  }
  return search;
}
