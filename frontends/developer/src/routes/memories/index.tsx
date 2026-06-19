import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { ChevronRight, Eye } from "lucide-react";
import { useAdminMemories } from "@/hooks/useAdminApi";
import { formatRelativeTime, truncate, cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

export const Route = createFileRoute("/memories/")({
  component: MemoriesPage,
});

function MemoriesPage() {
  const [archiveFilter, setArchiveFilter] = useState<"exclude" | "include" | "only">("exclude");
  const [namespaceFilter, setNamespaceFilter] = useState<string[]>([]);
  const [keyPrefixFilter, setKeyPrefixFilter] = useState("");

  const { data, isLoading, error } = useAdminMemories({
    archived: archiveFilter,
    namespacePrefix: namespaceFilter.length > 0 ? namespaceFilter : undefined,
    keyPrefix: keyPrefixFilter || undefined,
    limit: 50,
  });

  const memories = data?.items || [];

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
      {/* Header */}
      <div className="border-b border-border bg-background px-8 py-6">
        <h1 className="mb-2 text-3xl font-semibold text-foreground">Memories</h1>
        <p className="text-muted-foreground">Explore episodic memories by namespace hierarchy</p>
      </div>

      {/* Filters */}
      <div className="border-b border-border bg-background px-8 py-4">
        <div className="space-y-3">
          {/* Archive filter */}
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Show:</span>
            <div className="flex gap-1">
              <button
                onClick={() => setArchiveFilter("exclude")}
                className={cn(
                  "rounded-md px-3 py-1.5 text-sm transition-colors",
                  archiveFilter === "exclude"
                    ? "bg-accent text-accent-foreground"
                    : "text-muted-foreground hover:bg-accent/50",
                )}
              >
                Active
              </button>
              <button
                onClick={() => setArchiveFilter("include")}
                className={cn(
                  "rounded-md px-3 py-1.5 text-sm transition-colors",
                  archiveFilter === "include"
                    ? "bg-accent text-accent-foreground"
                    : "text-muted-foreground hover:bg-accent/50",
                )}
              >
                All
              </button>
              <button
                onClick={() => setArchiveFilter("only")}
                className={cn(
                  "rounded-md px-3 py-1.5 text-sm transition-colors",
                  archiveFilter === "only"
                    ? "bg-accent text-accent-foreground"
                    : "text-muted-foreground hover:bg-accent/50",
                )}
              >
                Archived
              </button>
            </div>
          </div>

          {/* Namespace breadcrumb */}
          {namespaceFilter.length > 0 && (
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">Namespace:</span>
              <div className="flex items-center gap-1">
                <button
                  onClick={() => setNamespaceFilter([])}
                  className="rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground"
                >
                  root
                </button>
                {namespaceFilter.map((segment, index) => (
                  <div key={index} className="flex items-center gap-1">
                    <ChevronRight className="h-4 w-4 text-muted-foreground" />
                    <button
                      onClick={() => handleNamespaceBreadcrumb(index + 1)}
                      className="rounded-md px-2 py-1 text-sm text-foreground hover:bg-accent"
                    >
                      {segment}
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Key prefix filter */}
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Key prefix:</span>
            <input
              type="text"
              value={keyPrefixFilter}
              onChange={(e) => setKeyPrefixFilter(e.target.value)}
              placeholder="Filter by key..."
              className="rounded-md border border-input bg-background px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
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

      {/* Content */}
      <div className="flex flex-1 overflow-hidden">
        {/* Namespace navigation sidebar */}
        {namespaceSegments.size > 0 && (
          <div className="w-64 border-r border-border bg-muted/30 p-4">
            <h3 className="mb-3 text-sm font-medium text-muted-foreground">Namespaces</h3>
            <div className="space-y-1">
              {Array.from(namespaceSegments).sort().map((segment) => (
                <button
                  key={segment}
                  onClick={() => handleNamespaceClick(segment)}
                  className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors hover:bg-accent hover:text-accent-foreground"
                >
                  <ChevronRight className="h-4 w-4" />
                  {segment}
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Memory list */}
        <div className="flex-1 overflow-auto p-8">
          {isLoading && (
            <div className="flex items-center justify-center py-12">
              <div className="text-center">
                <div className="mb-4 inline-block h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
                <p className="text-sm text-muted-foreground">Loading memories...</p>
              </div>
            </div>
          )}

          {error && (
            <div className="rounded-lg border border-destructive/50 bg-destructive/10 p-4 text-center">
              <p className="text-sm text-destructive">Failed to load memories: {error.message}</p>
            </div>
          )}

          {!isLoading && !error && memories.length === 0 && (
            <div className="rounded-lg border border-border bg-card p-12 text-center">
              <p className="text-muted-foreground">
                {archiveFilter === "only" ? "No archived memories found" : "No memories found"}
              </p>
            </div>
          )}

          {!isLoading && !error && memories.length > 0 && (
            <div className="space-y-4">
              {memories.map((memory) => (
                <div key={memory.id} className="rounded-lg border border-border bg-card p-6">
                  <div className="mb-3 flex items-start justify-between">
                    <div className="flex-1">
                      <div className="mb-2 flex items-center gap-2">
                        <span className="font-mono text-sm font-medium text-foreground">
                          {memory.namespace.join(" / ")}
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
                  <div className="mt-4 rounded-md bg-muted/50 p-4">
                    <div className="text-xs text-muted-foreground mb-2">Value Preview:</div>
                    <pre className="overflow-x-auto text-sm">
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
              ))}
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
