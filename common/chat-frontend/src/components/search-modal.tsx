import { useState, useEffect, useRef, useCallback } from "react";
import { createPortal } from "react-dom";
import { X, Search, MessageSquare } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { SearchService } from "@/client";
import type { SearchResult } from "@/client";

type SearchModalProps = {
  isOpen: boolean;
  onClose: () => void;
  onSelectConversation: (conversationId: string) => void;
};

type SearchResultsResponse = {
  data?: SearchResult[];
  nextCursor?: string | null;
};

function SearchResultSkeleton() {
  return (
    <div className="animate-pulse rounded-xl border border-transparent px-4 py-3">
      <div className="flex items-start gap-3">
        <div className="mt-0.5 h-5 w-5 rounded bg-mist" />
        <div className="min-w-0 flex-1">
          <div className="h-5 w-2/3 rounded bg-mist" />
          <div className="mt-2 h-4 w-full rounded bg-mist/70" />
          <div className="mt-1 h-4 w-4/5 rounded bg-mist/50" />
        </div>
      </div>
    </div>
  );
}

function SearchResultItem({
  result,
  onClick,
}: {
  result: SearchResult;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="w-full rounded-xl border border-transparent px-4 py-3 text-left transition-all hover:border-stone/10 hover:bg-mist/60"
    >
      <div className="flex items-start gap-3">
        <MessageSquare className="mt-0.5 h-5 w-5 flex-shrink-0 text-stone" />
        <div className="min-w-0 flex-1">
          <h3 className="truncate font-medium text-ink">
            {result.conversationTitle || "Untitled conversation"}
          </h3>
          {result.highlights && (
            <p className="mt-1 line-clamp-2 text-sm text-stone">
              {result.highlights}
            </p>
          )}
        </div>
      </div>
    </button>
  );
}

export function SearchModal({ isOpen, onClose, onSelectConversation }: SearchModalProps) {
  const modalRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");

  // Debounce search query (2 seconds)
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedQuery(searchQuery);
    }, 500);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  // Search query
  const searchResults = useQuery<SearchResult[]>({
    queryKey: ["search", debouncedQuery],
    queryFn: async () => {
      if (!debouncedQuery.trim()) {
        return [];
      }
      const response = (await SearchService.searchConversations({
        requestBody: {
          query: debouncedQuery,
          limit: 20,
          includeEntry: false,
        },
      })) as unknown as SearchResultsResponse;
      return response.data ?? [];
    },
    enabled: debouncedQuery.trim().length > 0,
    staleTime: 30000,
  });

  // Focus input when modal opens
  useEffect(() => {
    if (isOpen && inputRef.current) {
      inputRef.current.focus();
    }
  }, [isOpen]);

  // Close on escape key
  useEffect(() => {
    function handleEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        onClose();
      }
    }

    if (isOpen) {
      document.addEventListener("keydown", handleEscape);
      return () => {
        document.removeEventListener("keydown", handleEscape);
      };
    }
  }, [isOpen, onClose]);

  // Close when clicking outside
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (modalRef.current && !modalRef.current.contains(event.target as Node)) {
        onClose();
      }
    }

    if (isOpen) {
      document.addEventListener("mousedown", handleClickOutside);
      return () => {
        document.removeEventListener("mousedown", handleClickOutside);
      };
    }
  }, [isOpen, onClose]);

  // Reset state when modal closes
  // Note: This effect syncs UI state (search query) with modal visibility
  useEffect(() => {
    if (!isOpen) {
      setSearchQuery(""); // eslint-disable-line react-hooks/set-state-in-effect
      setDebouncedQuery("");
    }
  }, [isOpen]);

  const handleResultClick = useCallback(
    (conversationId: string) => {
      onSelectConversation(conversationId);
      onClose();
    },
    [onSelectConversation, onClose],
  );

  if (!isOpen) return null;

  const isSearching = searchResults.isFetching && debouncedQuery.trim().length > 0;
  const hasQuery = debouncedQuery.trim().length > 0;
  const results = searchResults.data ?? [];

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[15vh]">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-ink/40" aria-hidden="true" />

      {/* Modal */}
      <div
        ref={modalRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="search-modal-title"
        className="relative z-10 flex max-h-[70vh] w-full max-w-lg flex-col rounded-2xl border border-stone/20 bg-cream shadow-2xl"
      >
        {/* Header with search input */}
        <div className="flex items-center gap-3 border-b border-stone/10 px-4 py-3">
          <Search className="h-5 w-5 flex-shrink-0 text-stone" />
          <input
            ref={inputRef}
            type="text"
            placeholder="Search conversations..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="flex-1 bg-transparent text-base text-ink placeholder:text-stone/60 focus:outline-none"
            id="search-modal-title"
          />
          <button
            type="button"
            onClick={onClose}
            className="rounded p-1 text-stone transition-colors hover:bg-mist hover:text-ink"
            aria-label="Close"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto px-2 py-2">
          {!hasQuery ? (
            <p className="px-4 py-8 text-center text-sm text-stone">
              Type to search across all conversations
            </p>
          ) : isSearching ? (
            <div className="space-y-1">
              {[1, 2, 3, 4].map((i) => (
                <SearchResultSkeleton key={i} />
              ))}
            </div>
          ) : results.length === 0 ? (
            <p className="px-4 py-8 text-center text-sm text-stone">
              No results found for "{debouncedQuery}"
            </p>
          ) : (
            <div className="space-y-1" key={debouncedQuery}>
              {results.map((result, index) => (
                <SearchResultItem
                  key={result.conversationId ?? index}
                  result={result}
                  onClick={() => result.conversationId && handleResultClick(result.conversationId)}
                />
              ))}
            </div>
          )}
        </div>

        {/* Footer hint */}
        {hasQuery && !isSearching && results.length > 0 && (
          <div className="border-t border-stone/10 px-4 py-2">
            <p className="text-xs text-stone">
              {results.length} result{results.length !== 1 ? "s" : ""} found
            </p>
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
