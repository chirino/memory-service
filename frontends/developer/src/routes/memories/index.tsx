import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { ChevronRight, Eye } from "lucide-react";
import { useAdminMemories } from "@/hooks/useAdminApi";
import { formatRelativeTime, truncate, cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

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

  const { data, isLoading, error } = useAdminMemories({
    namespacePrefix: namespaceFilter.length > 0 ? namespaceFilter : undefined,
    keyPrefix: keyPrefixFilter || undefined,
    limit: 50,
  });

  const rawMemories = data?.items || [];
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
              {memories.map((memory) => {
                if (!memory.id) {
                  return null;
                }
                return (
                <div key={memory.id} className="console-panel rounded-xl p-5">
                  <div className="mb-3 flex items-start justify-between">
                    <div className="flex-1">
                      <div className="mb-2 flex items-center gap-2">
                        <span className="font-mono text-sm font-medium text-foreground">
                          {memory.namespace?.join(" / ") || "(no namespace)"}
                        </span>
                        {memory.archived && <Badge variant="secondary">Archived</Badge>}
                      </div>
                      <div className="text-lg font-semibold text-foreground">{memory.key}</div>
                      <div className="mt-1 text-sm text-muted-foreground">
                        Created {formatRelativeTime(memory.createdAt)}
                        {memory.expiresAt && ` • Expires ${formatRelativeTime(memory.expiresAt)}`}
                        {memory.revision && ` • Rev ${memory.revision}`}
                      </div>
                    </div>
                    <Link to="/memories/$memoryId" params={{ memoryId: memory.id }}>
                      <Button variant="ghost" size="sm">
                        <Eye className="h-4 w-4" />
                        View
                      </Button>
                    </Link>
                  </div>

                  {/* Value preview */}
                  <div className="console-code mt-4 rounded-lg p-4">
                    <div className="mb-2 text-xs text-muted-foreground">Value Preview:</div>
                    <pre className="overflow-x-auto text-sm leading-6">
                      {truncate(JSON.stringify(memory.value, null, 2), 200)}
                    </pre>
                  </div>

                  {/* Attributes */}
                  {memory.attributes && Object.keys(memory.attributes).length > 0 && (
                    <div className="mt-3 flex flex-wrap gap-2">
                      {Object.entries(memory.attributes).slice(0, 3).map(([key, value]) => (
                        <Badge key={key} variant="outline">
                          {key}: {String(value)}
                        </Badge>
                      ))}
                      {Object.keys(memory.attributes).length > 3 && (
                        <Badge variant="outline">+{Object.keys(memory.attributes).length - 3} more</Badge>
                      )}
                    </div>
                  )}

                  <div className="mt-3 border-t border-border pt-3 text-xs text-muted-foreground">
                    Memory ID: <span className="font-mono">{memory.id}</span>
                  </div>
                </div>
                );
              })}
            </div>
          )}

          {!isLoading && !error && memories.length > 0 && (
            <div className="mt-4 text-center text-sm text-muted-foreground">
              Showing {memories.length} memor{memories.length !== 1 ? "ies" : "y"}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// Made with Bob
