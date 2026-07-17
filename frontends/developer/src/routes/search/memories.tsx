import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { Search as SearchIcon, Database } from "lucide-react";
import { useAdminSearchMemories } from "@/hooks/useAdminApi";
import { MemoryCard } from "@/components/memories/MemoryCard";
import { useDebounce } from "@/hooks/useDebounce";
import { EmptySearchState } from "@/components/search/EmptySearchState";
import { SearchLoadingState } from "@/components/search/SearchLoadingState";
import { NoResultsState } from "@/components/search/NoResultsState";
import { LoadMoreButton } from "@/components/search/LoadMoreButton";

export const Route = createFileRoute("/search/memories")({
  component: SearchMemoriesPage,
});

function SearchMemoriesPage() {
  const [query, setQuery] = useState("");
  const debouncedQuery = useDebounce(query, 300);
  const [namespaceInput, setNamespaceInput] = useState("user");
  const [namespacePrefix, setNamespacePrefix] = useState<string[]>(["user"]);

  const {
    data,
    isLoading,
    isFetchingNextPage,
    hasNextPage,
    fetchNextPage,
  } = useAdminSearchMemories({
    namespacePrefix,
    query: debouncedQuery,
    limit: 20,
  });

  const results = (data?.pages.flatMap((page) => page.items || []) || [])
    .sort((a, b) => (b.score ?? 0) - (a.score ?? 0));
  const hasResults = results.length > 0;

  return (
    <div className="flex h-full flex-col">
      <div className="px-5 pb-5 pt-8 md:px-10 md:pt-10">
        <h1 className="console-title text-4xl leading-tight text-foreground md:text-5xl">
          Search Memories
        </h1>
        <p className="console-subtitle mt-3 text-base md:text-lg">
          Semantic search across memory items
        </p>
      </div>

      <div className="px-5 pb-6 md:px-10">
        <div className="space-y-4">
          <div className="relative">
            <SearchIcon className="absolute left-3 top-1/2 h-5 w-5 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              placeholder="Search memory items..."
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="console-input w-full py-3 pl-10 pr-4 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary"
            />
          </div>
          <div className="relative">
            <input
              type="text"
              placeholder="Namespace prefix (comma-separated, e.g., user,alice)"
              value={namespaceInput}
              onChange={(e) => {
                const value = e.target.value;
                setNamespaceInput(value);
                const trimmed = value.trim();
                setNamespacePrefix(trimmed ? trimmed.split(",").map(s => s.trim()).filter(Boolean) : []);
              }}
              className="console-input w-full py-2 px-4 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary"
            />
          </div>
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
                <Database className="h-5 w-5 text-muted-foreground" />
                <h2 className="text-lg font-semibold text-foreground">
                  Results ({results.length})
                </h2>
              </div>
              <div className="space-y-3">
                {results.map((memory) => (
                  <MemoryCard
                    key={memory.id}
                    memory={memory}
                    score={memory.score}
                    matchedQueries={memory.matchedQueries}
                  />
                ))}
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