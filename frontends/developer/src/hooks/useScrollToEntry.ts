import { useEffect, useRef, useCallback, useState, useMemo } from "react";

interface EntryWithContent {
	id: string;
	channel?: string;
	contentType?: string;
	content?: unknown[];
}

interface UseScrollToEntryOptions {
	/** The entry ID to scroll to directly (from URL search params ?entryId=) */
	targetEntryId: string | undefined;
	/** The fork point entry ID - will scroll to the next user message after this entry (from URL search params ?forkedAt=) */
	forkedAtEntryId?: string | undefined;
	/** All entries (unfiltered) */
	entries: EntryWithContent[];
	/** Function to reset channel filter to "all" */
	setChannelFilter?: (filter: string) => void;
}

interface UseScrollToEntryResult {
	/** ID of the entry currently being highlighting */
	highlightedEntryId: string | null;
	/** Ref callback to attach to entry elements */
	getEntryRef: (entryId: string) => (el: HTMLElement | null) => void;
	/** Call this to select an entry without triggering autoscroll (for in-page clicks) */
	selectWithoutScroll: (entryId: string) => void;
}

/**
 * Check if an entry is a user message (not AI/assistant)
 */
function isUserMessage(entry: EntryWithContent): boolean {
	if (entry.channel !== "history" || entry.contentType !== "message") {
		return false;
	}
	const firstContent = entry.content?.[0] as { role?: string } | undefined;
	return firstContent?.role === "USER" || firstContent?.role === "user";
}

/**
 * Hook to handle scrolling to and highlighting a specific entry from search results or fork navigation.
 *
 * Supports two modes:
 * 1. targetEntryId - Scrolls directly to the specified entry
 * 2. forkedAtEntryId - Finds the next user message after the fork point and scrolls to it
 *
 * When scrolling:
 * 1. Resets channel filter to "all" to ensure the entry is visible
 * 2. Scrolls the entry into view
 * 3. Highlights the entry temporarily (3 seconds)
 */
export function useScrollToEntry({
	targetEntryId,
	forkedAtEntryId,
	entries,
	setChannelFilter,
}: UseScrollToEntryOptions): UseScrollToEntryResult {
	const [highlightedEntryId, setHighlightedEntryId] = useState<string | null>(null);
	const entryRefs = useRef<Map<string, HTMLElement>>(new Map());
	const hasScrolled = useRef(false);
	// Flag to skip the next scroll (set when user clicks an entry in-page)
	const skipNextScroll = useRef(false);

	// Compute the actual entry ID to scroll to
	const resolvedEntryId = useMemo(() => {
		// Direct entry ID takes precedence
		if (targetEntryId) {
			return targetEntryId;
		}

		// For forkedAt, find the next user message after the fork point
		if (forkedAtEntryId && entries.length > 0) {
			const forkIndex = entries.findIndex((e) => e.id === forkedAtEntryId);
			if (forkIndex === -1) {
				return undefined;
			}

			// Search for the next user message after the fork point
			for (let i = forkIndex + 1; i < entries.length; i++) {
				if (isUserMessage(entries[i])) {
					return entries[i].id;
				}
			}
		}

		return undefined;
	}, [targetEntryId, forkedAtEntryId, entries]);

	// Handle scrolling when target entry changes
	useEffect(() => {
		if (!resolvedEntryId || entries.length === 0) {
			return;
		}

		// Check if we should skip this scroll (user clicked an entry in-page)
		if (skipNextScroll.current) {
			skipNextScroll.current = false;
			hasScrolled.current = true;
			// Still highlight the entry, just don't scroll
			setHighlightedEntryId(resolvedEntryId);
			setTimeout(() => {
				setHighlightedEntryId(null);
			}, 3000);
			return;
		}

		// If we already scrolled to this entry, don't scroll again
		if (hasScrolled.current) {
			return;
		}

		// Find the entry in the list
		const entryExists = entries.some((e) => e.id === resolvedEntryId);
		if (!entryExists) {
			return;
		}

		// Check if the entry is already visible (has a rendered element)
		const existingElement = entryRefs.current.get(resolvedEntryId);
		if (existingElement) {
			// Entry is already visible - just scroll and highlight without changing filter
			hasScrolled.current = true;
			requestAnimationFrame(() => {
				existingElement.scrollIntoView({ behavior: "smooth", block: "center" });
				setHighlightedEntryId(resolvedEntryId);
				setTimeout(() => {
					setHighlightedEntryId(null);
				}, 3000);
			});
			return;
		}

		// Entry not visible - reset channel filter to show all entries
		if (setChannelFilter) {
			setChannelFilter("all");
		}

		// Wait for re-render with all entries visible, then scroll
		requestAnimationFrame(() => {
			const element = entryRefs.current.get(resolvedEntryId);
			if (element) {
				hasScrolled.current = true;
				element.scrollIntoView({ behavior: "smooth", block: "center" });
				setHighlightedEntryId(resolvedEntryId);

				// Remove highlight after 3 seconds
				setTimeout(() => {
					setHighlightedEntryId(null);
				}, 3000);
			}
		});
	}, [resolvedEntryId, entries, setChannelFilter]);

	// Reset scroll state when entry ID param changes (for external navigation)
	useEffect(() => {
		// Only reset if not skipping (external navigation like clicking a link)
		if (!skipNextScroll.current) {
			hasScrolled.current = false;
		}
	}, [targetEntryId, forkedAtEntryId]);

	// Ref callback factory for entry elements
	const getEntryRef = useCallback((entryId: string) => {
		return (el: HTMLElement | null) => {
			if (el) {
				entryRefs.current.set(entryId, el);
			} else {
				entryRefs.current.delete(entryId);
			}
		};
	}, []);

	// Mark that the next URL change should not trigger scrolling (for in-page clicks)
	const selectWithoutScroll = useCallback((_entryId: string) => {
		skipNextScroll.current = true;
	}, []);

	return {
		highlightedEntryId,
		getEntryRef,
		selectWithoutScroll,
	};
}

// Made with Bob