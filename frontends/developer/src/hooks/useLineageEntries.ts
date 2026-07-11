import { useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { adminGetEntries } from "@/api/generated/sdk.gen";
import { adminGetEntriesQueryKey } from "@/api/generated/@tanstack/react-query.gen";
import type { ConversationForkPoint, Entry } from "@/api/generated/types.gen";
import type { ForkOption } from "@/lib/conversation";

export type { ForkOption };

export interface EntryWithForkPoint extends Entry {
  isForkPoint?: boolean;
  /** The conversation that owns the stored entry. */
  sourceConversationId?: string;
  /** Continuation alternatives supplied by the fork-navigation snapshot. */
  forksAtPoint?: ForkOption[];
}

interface UseLineageEntriesOptions {
  conversationId: string;
  forkPoints: ConversationForkPoint[];
}

interface UseLineageEntriesResult {
  entries: EntryWithForkPoint[];
  isLoading: boolean;
  hasOlderEntries: boolean;
  isLoadingOlderEntries: boolean;
  loadOlderEntries: () => Promise<unknown>;
}

const pageSize = 50;

/**
 * Loads the newest page of the selected conversation's visible ancestry path,
 * then pages backward through older entries.
 * Sibling-fork entries are never requested; navigation options from the
 * admin fork snapshot are attached to their visible display entries.
 */
export function useLineageEntries({ conversationId, forkPoints }: UseLineageEntriesOptions): UseLineageEntriesResult {
  const initialOptions = {
    path: { id: conversationId },
    query: { forks: "none" as const, limit: pageSize, tail: true },
  };
  const entriesQuery = useInfiniteQuery({
    queryKey: adminGetEntriesQueryKey(initialOptions),
    initialPageParam: null as string | null,
    queryFn: async ({ pageParam }) => {
      const { data } = await adminGetEntries({
        path: { id: conversationId },
        query: {
          forks: "none",
          limit: pageSize,
          tail: pageParam === null ? true : undefined,
          beforeCursor: pageParam ?? undefined,
        },
        throwOnError: true,
      });
      return data;
    },
    getNextPageParam: (lastPage) => lastPage.beforeCursor ?? undefined,
  });

  const forksByEntryId = useMemo(() => {
    const result = new Map<string, ForkOption[]>();
    for (const point of forkPoints) {
      result.set(
        point.entryId,
        point.options.map((option) => ({
          conversationId: option.conversationId,
          entryId: option.entryId,
          createdAt: option.createdAt,
          label: option.preview || option.title,
          title: option.title,
          isActive: option.entryId === point.entryId,
        })),
      );
    }
    return result;
  }, [forkPoints]);

  const entries = useMemo<EntryWithForkPoint[]>(() => {
    const pages = entriesQuery.data?.pages ?? [];
    return [...pages]
      .reverse()
      .flatMap((page) => page.data ?? [])
      .map((entry) => {
        const forksAtPoint = forksByEntryId.get(entry.id);
        return {
          ...entry,
          sourceConversationId: entry.conversationId,
          isForkPoint: Boolean(forksAtPoint?.length),
          forksAtPoint,
        };
      });
  }, [entriesQuery.data, forksByEntryId]);

  return {
    entries,
    isLoading: entriesQuery.isLoading,
    hasOlderEntries: entriesQuery.hasNextPage,
    isLoadingOlderEntries: entriesQuery.isFetchingNextPage,
    loadOlderEntries: entriesQuery.fetchNextPage,
  };
}
