import { createFileRoute, Link } from "@tanstack/react-router";
import { useState, useMemo } from "react";
import { ChevronRight, Eye } from "lucide-react";
import { useAdminMemoriesInfinite } from "@/hooks/useAdminApi";
import { formatRelativeTime, truncate, cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import {
  COGNITION_KIND_LABELS,
  cognitionConfidence,
  describeConfidence,
  getCognitionKind,
  normalizeCognitionMemoryValue,
} from "@/lib/cognition";

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
              {memories.map((memory) => {
                if (!memory.id) {
                  return null;
                }
                const memoryId = memory.id;
                const namespaceLabel = memory.namespace?.join(" / ") || "(no namespace)";
                const cognitionKind = getCognitionKind(memory.namespace);

                if (cognitionKind) {
                  const cognitionValue = normalizeCognitionMemoryValue(memory.value);
                  const content = typeof cognitionValue.content === "string" ? cognitionValue.content : undefined;
                  const confidence = cognitionConfidence(cognitionValue);
                  const subject = typeof memory.attributes?.sub === "string" ? memory.attributes.sub : undefined;

                  return (
                    <Link
                      key={memoryId}
                      to="/memories/$memoryId"
                      params={{ memoryId }}
                      className="console-panel group block rounded-xl p-5 transition-colors hover:bg-sage-soft/20"
                    >
                      <div className="flex items-start justify-between gap-4">
                        <div className="min-w-0 flex-1">
                          <div className="mb-2 flex flex-wrap items-center gap-2">
                            <Badge>{COGNITION_KIND_LABELS[cognitionKind]}</Badge>
                            <span className="truncate font-mono text-xs text-muted-foreground">{namespaceLabel}</span>
                            {memory.archived && <Badge variant="secondary">Archived</Badge>}
                          </div>
                          <p className="text-lg font-medium leading-snug text-foreground">
                            {content || <span className="font-mono text-base text-muted-foreground">{memory.key}</span>}
                          </p>
                        </div>
                        <div className="flex shrink-0 items-center gap-3">
                          {confidence !== null && <ConfidenceChip value={confidence} />}
                          <ViewAffordance />
                        </div>
                      </div>

                      <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border pt-3 text-xs text-muted-foreground">
                        <span>Created {formatRelativeTime(memory.createdAt)}</span>
                        {memory.revision && <span>Rev {memory.revision}</span>}
                        {memory.expiresAt && <span>Expires {formatRelativeTime(memory.expiresAt)}</span>}
                        {subject && (
                          <span>
                            about <span className="text-foreground">{subject}</span>
                          </span>
                        )}
                        <span className="font-mono">{memoryId}</span>
                      </div>
                    </Link>
                  );
                }

                return (
                  <Link
                    key={memoryId}
                    to="/memories/$memoryId"
                    params={{ memoryId }}
                    className="console-panel group block rounded-xl p-5 transition-colors hover:bg-sage-soft/20"
                  >
                    <div className="mb-3 flex items-start justify-between">
                      <div className="flex-1">
                        <div className="mb-2 flex items-center gap-2">
                          <span className="font-mono text-sm font-medium text-foreground">{namespaceLabel}</span>
                          {memory.archived && <Badge variant="secondary">Archived</Badge>}
                        </div>
                        <div className="text-lg font-semibold text-foreground">{memory.key}</div>
                        <div className="mt-1 text-sm text-muted-foreground">
                          Created {formatRelativeTime(memory.createdAt)}
                          {memory.expiresAt && ` • Expires ${formatRelativeTime(memory.expiresAt)}`}
                          {memory.revision && ` • Rev ${memory.revision}`}
                        </div>
                      </div>
                      <ViewAffordance />
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
                      Memory ID: <span className="font-mono">{memoryId}</span>
                    </div>
                  </Link>
                );
              })}
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

function ViewAffordance() {
  return (
    <span className="inline-flex items-center gap-1 text-sm font-medium text-muted-foreground transition-colors group-hover:text-foreground">
      <Eye className="h-4 w-4" />
      View
    </span>
  );
}

function ConfidenceChip({ value }: { value: number }) {
  return (
    <span
      title={describeConfidence(value)}
      className="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-sage-soft/60 px-2.5 py-1 text-xs font-medium text-primary"
    >
      <span className="h-1.5 w-1.5 rounded-full bg-primary" />
      {Math.round(value * 100)}%
    </span>
  );
}

// Made with Bob
