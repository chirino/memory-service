import { createFileRoute } from "@tanstack/react-router";
import { useState, useMemo } from "react";
import { ChevronRight } from "lucide-react";
import { useAdminMemoriesInfinite } from "@/hooks/useAdminApi";
import { cn } from "@/lib/utils";
import { MemoryCard } from "@/components/memories/MemoryCard";

function getErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Unknown error";
}

export const Route = createFileRoute("/memories/")({
  component: MemoriesPage,
});

function MemoriesPage() {
  const [archiveFilter, setArchiveFilter] = useState<"exclude" | "include" | "only">("exclude");
  const [namespaceFilter, setNamespaceFilter] = useState<string[]>([]);
  const [keyPrefixFilter, setKeyPrefixFilter] = useState("");

  const {
    data,
    isLoading,
    error,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = useAdminMemoriesInfinite({
    namespacePrefix: namespaceFilter.length > 0 ? namespaceFilter : undefined,
    keyPrefix: keyPrefixFilter || undefined,
    limit: 50,
  });

  const rawMemories = useMemo(() => {
    const pages = data?.pages ?? [];
    return pages.flatMap((page) => page.items ?? []);
  }, [data]);

  const memories = rawMemories.filter((memory) => {
    if (archiveFilter === "include") return true;
    if (archiveFilter === "only") return memory.archived;
    return !memory.archived;
  });

  // Extract unique namespace segments for navigation
  const namespaceSegments = new Set<string>();
  memories.forEach((memory) => {
    if (memory.namespace && memory.namespace.length > namespaceFilter.length) {
      namespaceSegments.add(memory.namespace[namespaceFilter.length]);
    }
  });

  const handleNamespaceClick = (segment: string) => {
    setNamespaceFilter([...namespaceFilter, segment]);
  };

  const handleNamespaceBreadcrumb = (index: number) => {
    setNamespaceFilter(namespaceFilter.slice(0, index));
  };

  return (
    <div className="flex h-full flex-col">
      <div className="px-5 pb-5 pt-8 md:px-10 md:pt-10">
        <h1 className="console-title text-4xl leading-tight text-foreground md:text-5xl">Memories</h1>
        <p className="console-subtitle mt-3 text-base md:text-lg">Explore episodic memories by namespace hierarchy</p>
      </div>

      <div className="px-5 pb-6 md:px-10">
        <div className="space-y-3">
          <div className="flex items-center gap-2">
            <div className="console-segmented">
              <button
                onClick={() => setArchiveFilter("exclude")}
                className={cn(
                  "console-segment",
                  archiveFilter === "exclude" && "console-segment-active",
                )}
              >
                Active
              </button>
              <button
                onClick={() => setArchiveFilter("include")}
                className={cn(
                  "console-segment",
                  archiveFilter === "include" && "console-segment-active",
                )}
              >
                All
              </button>
              <button
                onClick={() => setArchiveFilter("only")}
                className={cn(
                  "console-segment",
                  archiveFilter === "only" && "console-segment-active",
                )}
              >
                Archived
              </button>
            </div>
          </div>

          {namespaceFilter.length > 0 && (
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">Namespace:</span>
              <div className="flex items-center gap-1">
                <button
                  onClick={() => setNamespaceFilter([])}
                  className="rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-white/65 hover:text-foreground"
                >
                  root
                </button>
                {namespaceFilter.map((segment, index) => (
                  <div key={index} className="flex items-center gap-1">
                    <ChevronRight className="h-4 w-4 text-muted-foreground" />
                    <button
                      onClick={() => handleNamespaceBreadcrumb(index + 1)}
                      className="rounded-md px-2 py-1 text-sm text-foreground hover:bg-white/65"
                    >
                      {segment}
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}

          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Key prefix:</span>
            <input
              type="text"
              value={keyPrefixFilter}
              onChange={(e) => setKeyPrefixFilter(e.target.value)}
              placeholder="Filter by key..."
              className="console-input px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            />
            {keyPrefixFilter && (
              <button
                onClick={() => setKeyPrefixFilter("")}
                className="text-sm text-muted-foreground hover:text-foreground"
              >
                Clear
              </button>
            )}
          </div>
        </div>
      </div>

      <div className="flex flex-1 overflow-hidden">
        {namespaceSegments.size > 0 && (
          <div className="ml-5 hidden w-64 border-r border-[rgba(43,39,34,0.1)] pr-4 pt-2 md:ml-10 md:block">
            <h3 className="mb-3 text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">Namespaces</h3>
            <div className="space-y-1">
              {Array.from(namespaceSegments).sort().map((segment) => (
                <button
                  key={segment}
                  onClick={() => handleNamespaceClick(segment)}
                  className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm text-stone transition-colors hover:bg-white/65 hover:text-foreground"
                >
                  <ChevronRight className="h-4 w-4" />
                  {segment}
                </button>
              ))}
            </div>
          </div>
        )}

        <div className="flex-1 overflow-y-auto px-5 pb-8 md:px-10">
          {isLoading && (
            <div className="flex items-center justify-center py-12">
              <div className="text-center">
                <div className="mb-4 inline-block h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
                <p className="text-sm text-muted-foreground">Loading memories...</p>
              </div>
            </div>
          )}

          {error && (
            <div className="console-panel rounded-xl p-4 text-center">
              <p className="text-sm text-destructive">Failed to load memories: {getErrorMessage(error)}</p>
            </div>
          )}

          {!isLoading && !error && memories.length === 0 && (
            <div className="console-panel rounded-xl p-12 text-center">
              <p className="text-muted-foreground">
                {archiveFilter === "only" ? "No archived memories found" : "No memories found"}
              </p>
            </div>
          )}

          {!isLoading && !error && memories.length > 0 && (
            <div className="space-y-3">
              {memories.map((memory) => (
                <MemoryCard key={memory.id} memory={memory} />
              ))}
            </div>
          )}

          {!isLoading && !error && memories.length > 0 && (
            <>
              {hasNextPage && (
                <div className="mt-6 flex justify-center">
                  <button
                    type="button"
                    onClick={() => void fetchNextPage()}
                    disabled={isFetchingNextPage}
                    className="console-panel rounded-lg px-6 py-3 text-sm text-muted-foreground transition-colors hover:text-foreground disabled:cursor-wait disabled:opacity-60"
                  >
                    {isFetchingNextPage ? "Loading more memories..." : "Load more memories"}
                  </button>
                </div>
              )}
              <div className="mt-4 text-center text-sm text-muted-foreground">
                Showing {memories.length} memor{memories.length !== 1 ? "ies" : "y"}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}



// Made with Bob
