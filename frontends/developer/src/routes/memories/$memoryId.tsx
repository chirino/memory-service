import * as React from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Archive, ArrowLeft, Clock, Hash, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { TimestampPopover } from "@/components/ui/timestamp-popover";
import { JsonHighlight, formatJson } from "@/components/content-renderers/JsonHighlight";
import { useAdminMemory } from "@/hooks/useAdminApi";

export const Route = createFileRoute("/memories/$memoryId")({
  component: MemoryDetailPage,
});

function MemoryDetailPage() {
  const { memoryId } = Route.useParams();
  const { data, isLoading, error } = useAdminMemory(memoryId);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="console-panel rounded-2xl p-10 text-center">
          <Loader2 className="mb-4 inline-block h-8 w-8 animate-spin text-primary" />
          <p className="text-sm text-muted-foreground">Loading memory…</p>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex h-full items-center justify-center p-8">
        <div className="console-panel max-w-md rounded-2xl p-6 text-center">
          <p className="text-sm text-destructive mb-4">
            {(error as Error).message || "Failed to load memory"}
          </p>
          <Link to="/memories">
            <Button variant="outline" size="sm">
              <ArrowLeft className="h-4 w-4 mr-1" />
              Back to Memories
            </Button>
          </Link>
        </div>
      </div>
    );
  }

  if (!data) {
    return null;
  }

  return (
    <div className="flex h-full flex-col">
      <div className="px-5 pb-6 pt-8 md:px-10">
        <div className="mb-4">
          <Link to="/memories">
            <Button variant="ghost" size="sm">
              <ArrowLeft className="h-4 w-4 mr-1" />
              Back to Memories
            </Button>
          </Link>
        </div>

        <div className="mb-3 flex items-center gap-2 text-sm text-muted-foreground">
          <Link to="/memories" className="hover:text-foreground transition-colors">
            Memories
          </Link>
          {data.namespace && data.namespace.length > 0 && (
            <>
              <span>/</span>
              {data.namespace.map((segment, idx) => (
                <React.Fragment key={idx}>
                  <span className="font-mono">{segment}</span>
                  {idx < (data.namespace?.length ?? 0) - 1 && <span>/</span>}
                </React.Fragment>
              ))}
            </>
          )}
        </div>

        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 mb-2">
              <h1 className="truncate font-mono text-2xl font-semibold md:text-3xl">
                {data.key ?? "(no key)"}
              </h1>
              <CopyButton value={data.key ?? ""} />
            </div>

            {/* Badges */}
            <div className="flex items-center gap-2 flex-wrap">
              {data.archived ? (
                <Badge variant="secondary">
                  <Archive className="w-3 h-3 mr-1" />
                  Archived
                  {data.archivedAt && (
                    <span className="ml-1 text-xs">
                      {new Date(data.archivedAt).toISOString().slice(0, 10)}
                    </span>
                  )}
                </Badge>
              ) : (
                <Badge>
                  Active
                </Badge>
              )}
              {data.expiresAt && (
                <Badge variant="outline">
                  <Clock className="w-3 h-3 mr-1" />
                  expires {new Date(data.expiresAt).toISOString().slice(0, 10)}
                </Badge>
              )}
              {data.revision !== undefined && (
                <Badge variant="outline" className="font-mono">
                  <Hash className="w-3 h-3 mr-1" />
                  rev {data.revision}
                </Badge>
              )}
              {data.usage?.fetchCount !== undefined && data.usage.fetchCount > 0 && (
                <Badge variant="outline">
                  {data.usage.fetchCount} fetch{data.usage.fetchCount !== 1 ? "es" : ""}
                </Badge>
              )}
              {data.usage?.lastFetchedAt && (
                <TimestampPopover
                  timestamp={data.usage.lastFetchedAt}
                  displayText={`Last fetched ${new Date(data.usage.lastFetchedAt).toLocaleDateString()}`}
                />
              )}
            </div>
          </div>
        </div>
      </div>

      <div className="border-y border-[rgba(43,39,34,0.1)] bg-white/35 px-5 py-4 md:px-10">
        <div className="grid grid-cols-2 gap-4 text-sm md:grid-cols-3">
          <div>
            <span className="text-muted-foreground">Memory ID</span>
            <p className="mt-1 font-mono text-xs text-foreground">{data.id}</p>
          </div>
          <div>
            <span className="text-muted-foreground">Namespace</span>
            <p className="mt-1 font-mono text-xs text-foreground">
              {data.namespace && data.namespace.length > 0
                ? data.namespace.join(" / ")
                : "(no namespace)"}
            </p>
          </div>
          <div>
            <span className="text-muted-foreground">Created</span>
            <p className="mt-1 text-xs text-foreground">
              {data.createdAt ? new Date(data.createdAt).toISOString() : "—"}
            </p>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-5 py-8 md:px-10">
        <div className="space-y-6">
          <JsonCard label="Value" data={data.value} />

          {data.attributes && Object.keys(data.attributes).length > 0 && (
            <JsonCard label="Attributes" data={data.attributes} />
          )}
        </div>
      </div>
    </div>
  );
}

interface JsonCardProps {
  label: string;
  data: unknown;
}

function JsonCard({ label, data }: JsonCardProps) {
  const text = React.useMemo(() => {
    if (data === undefined || data === null) return "(empty)";
    try {
      return formatJson(data);
    } catch {
      return String(data);
    }
  }, [data]);

  return (
    <div className="console-panel rounded-xl">
      <div className="flex items-center justify-between border-b border-[rgba(43,39,34,0.1)] px-4 py-3">
        <div className="text-sm font-medium">{label}</div>
        <CopyButton value={text} />
      </div>
      <div className="p-4">
        <JsonHighlight
          value={data}
          className="console-code overflow-x-auto whitespace-pre-wrap rounded-lg p-4 font-mono text-xs leading-6"
        />
      </div>
    </div>
  );
}

// Made with Bob
