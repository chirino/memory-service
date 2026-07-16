import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { Search as SearchIcon, MessageSquare, ChevronDown, ChevronRight } from "lucide-react";
import { useAdminSearchConversations } from "@/hooks/useAdminApi";
import { formatRelativeTime } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { EntryCard } from "@/components/conversations/EntryCard";
import { useDebounce } from "@/hooks/useDebounce";
import { EmptySearchState } from "@/components/search/EmptySearchState";
import { SearchLoadingState } from "@/components/search/SearchLoadingState";
import { NoResultsState } from "@/components/search/NoResultsState";
import { LoadMoreButton } from "@/components/search/LoadMoreButton";

export const Route = createFileRoute("/search/conversations")({
  component: SearchConversationsPage,
});

function SearchConversationsPage() {
  const [query, setQuery] = useState("");
  const debouncedQuery = useDebounce(query, 300);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const {
    data,
    isLoading,
    isFetchingNextPage,
    hasNextPage,
    fetchNextPage,
  } = useAdminSearchConversations({
    query: debouncedQuery,
    searchType: "fulltext",
    limit: 20,
    includeEntry: true,
    groupByConversation: true,
  });

  const results = (data?.pages.flatMap((page) => page.data || []) || [])
    .sort((a, b) => (b.score ?? 0) - (a.score ?? 0));
  const hasResults = results.length > 0;

  const toggleExpand = (key: string) => {
    setExpandedId(expandedId === key ? null : key);
  };

  return (
    <div className="flex h-full flex-col">
      <div className="px-5 pb-5 pt-8 md:px-10 md:pt-10">
        <h1 className="console-title text-4xl leading-tight text-foreground md:text-5xl">
          Search Conversations
        </h1>
        <p className="console-subtitle mt-3 text-base md:text-lg">
          Full-text search across conversation entries
        </p>
      </div>

      <div className="px-5 pb-6 md:px-10">
        <div className="relative">
          <SearchIcon className="absolute left-3 top-1/2 h-5 w-5 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search conversation entries..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="console-input w-full py-3 pl-10 pr-4 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary"
          />
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-5 pb-8 md:px-10">
        {!query && <EmptySearchState />}

        {query && isLoading && <SearchLoadingState />}

        {query && !isLoading && !hasResults && <NoResultsState query={query} />}

        {query && hasResults && (
          <div className="space-y-8">
            <div>
              <div className="mb-4 flex items-center gap-2">
                <MessageSquare className="h-5 w-5 text-muted-foreground" />
                <h2 className="text-lg font-semibold text-foreground">
                  Results ({results.length})
                </h2>
              </div>
              <div className="space-y-3">
                {results.map((result) => {
                  const expandKey = `${result.conversationId}-${result.entryId}`;
                  
                  // Extract roles from entry content
                  const extractRoles = (): string => {
                    if (!result.entry?.content || !Array.isArray(result.entry.content)) return "";
                    const roles = result.entry.content
                      .map((item) => {
                        if (typeof item === "object" && item !== null) {
                          const obj = item as Record<string, unknown>;
                          return obj.role ? String(obj.role) : null;
                        }
                        return null;
                      })
                      .filter((role): role is string => role !== null);
                    return roles.length > 0 ? roles.join(", ") : "";
                  };
                  const rolesText = extractRoles();
                  
                  return (
                  <div key={expandKey} className="console-panel rounded-xl overflow-hidden">
                    <button
                      onClick={() => toggleExpand(expandKey)}
                      className="w-full p-4 text-left transition-colors hover:bg-sage-soft/25"
                    >
                      <div className="mb-2 flex items-start justify-between">
                        <div className="flex items-start gap-2 flex-1 min-w-0">
                          {expandedId === expandKey ? (
                            <ChevronDown className="h-5 w-5 text-muted-foreground shrink-0 mt-0.5" />
                          ) : (
                            <ChevronRight className="h-5 w-5 text-muted-foreground shrink-0 mt-0.5" />
                          )}
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2 mb-1 flex-wrap">
                              <h3 className="font-medium text-foreground">
                                {result.conversationTitle || "Untitled Conversation"}
                              </h3>
                              {result.score && (
                                <Badge variant="outline" className="font-mono text-xs">
                                  {result.score.toFixed(3)}
                                </Badge>
                              )}
                              {rolesText && (
                                <Badge variant="secondary" className="text-xs">
                                  {rolesText}
                                </Badge>
                              )}
                            </div>
                            {expandedId !== expandKey && result.highlights && (
                              <div className="mt-2 rounded-md bg-sage-soft/30 p-2 text-sm text-foreground">
                                {result.highlights}
                              </div>
                            )}
                          </div>
                        </div>
                      </div>
                      {expandedId !== expandKey && result.entry && (
                        <div className="space-y-1 text-sm text-muted-foreground ml-7">
                          <p className="font-mono text-xs">Entry: {result.entryId?.slice(0, 16)}...</p>
                          {result.entry.createdAt && (
                            <p>Created {formatRelativeTime(result.entry.createdAt)}</p>
                          )}
                        </div>
                      )}
                    </button>
                    {expandedId === expandKey && result.entry && (
                      <div className="border-t border-[rgba(43,39,34,0.1)] px-4 pb-4">
                        <div className="space-y-4 pt-4">
                          <div className="text-sm font-medium text-muted-foreground">
                            Matched Entry
                          </div>
                          <div className="console-panel rounded-lg p-3 ring-2 ring-primary">
                            <EntryCard entry={result.entry} compact={false} />
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                )})}
              </div>
            </div>

            <LoadMoreButton
              hasNextPage={hasNextPage}
              isFetchingNextPage={isFetchingNextPage}
              onLoadMore={() => fetchNextPage()}
            />
          </div>
        )}
      </div>
    </div>
  );
}

// Made with Bob