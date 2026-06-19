import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { adminGetEntriesOptions } from "@/api/client";
import type { Entry, ConversationForkSummary } from "@/api/generated/types.gen";
import { createForkView, type ForkOption } from "@/lib/conversation";

// Re-export ForkOption from conversation.ts for backward compatibility
export type { ForkOption };

// Extended entry type with fork point information (for UI display)
export interface EntryWithForkPoint extends Entry {
	isForkPoint?: boolean;
	/** The conversation this entry originally belongs to */
	sourceConversationId?: string;
	/** List of forks that diverge at this fork point */
	forksAtPoint?: ForkOption[];
}

interface UseLineageEntriesOptions {
	/** The current conversation ID */
	conversationId: string;
	/** Fork summaries from adminListForksOptions (contains full fork tree) */
	forkSummaries: ConversationForkSummary[];
}

interface UseLineageEntriesResult {
	/** Combined entries from the entire lineage with fork points marked */
	entries: EntryWithForkPoint[];
	/** Whether any lineage entries are still loading */
	isLoading: boolean;
}

/**
 * Hook to fetch and combine entries from a conversation's fork lineage.
 *
 * Uses createForkView to process entries and fork summaries, providing
 * a unified view of the conversation with fork points annotated.
 *
 * Algorithm:
 * 1. Fetch all entries with forks: "all" to get entries from all related conversations
 * 2. Use createForkView to build the fork view with proper lineage handling
 * 3. Transform EntryAndForkInfo to EntryWithForkPoint for backward compatibility
 */
export function useLineageEntries({
	conversationId,
	forkSummaries,
}: UseLineageEntriesOptions): UseLineageEntriesResult {
	// Fetch all entries for the conversation (with forks: "all" to get entire fork tree)
	const { data: entriesData, isLoading } = useQuery<{ data?: Entry[] }>({
		...adminGetEntriesOptions({
			path: { id: conversationId },
			query: { forks: "all" },
		}),
		queryKey: ["admin", "conversations", conversationId, "entries", "all"],
	});

	// Build a map of fork conversationId -> fork summary for title lookup
	const forkMetaById = useMemo(() => {
		const map = new Map<string, ConversationForkSummary>();
		for (const fork of forkSummaries) {
			if (fork.conversationId) {
				map.set(fork.conversationId, fork);
			}
		}
		return map;
	}, [forkSummaries]);

	// Use createForkView to process entries and get the combined view
	const combinedEntries = useMemo((): EntryWithForkPoint[] => {
		const entries = entriesData?.data || [];
		if (entries.length === 0) {
			return [];
		}

		const forkView = createForkView(entries, forkSummaries);
		const entryAndForkInfos = forkView.entries(conversationId);

		// Transform EntryAndForkInfo to EntryWithForkPoint for backward compatibility
		return entryAndForkInfos.map((item) => {
			const result: EntryWithForkPoint = {
				...item.entry,
				sourceConversationId: item.entry.conversationId,
			};

			if (item.forks && item.forks.length > 0) {
				result.isForkPoint = true;
				// Enrich fork options with title and isActive from forkSummaries
				result.forksAtPoint = item.forks.map((fork) => {
					const meta = forkMetaById.get(fork.conversationId);
					return {
						...fork,
						title: meta?.title || fork.label,
						isActive: fork.conversationId === conversationId,
					};
				});
			}

			return result;
		});
	}, [entriesData?.data, forkSummaries, conversationId, forkMetaById]);

	return {
		entries: combinedEntries,
		isLoading,
	};
}

// Made with Bob