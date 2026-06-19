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
        <div className="text-center">
          <Loader2 className="mb-4 inline-block h-8 w-8 animate-spin text-primary" />
          <p className="text-sm text-muted-foreground">Loading memory…</p>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex h-full items-center justify-center p-8">
        <div className="rounded-lg border border-destructive/50 bg-destructive/10 p-6 text-center max-w-md">
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
      {/* Header */}
      <div className="border-b border-border bg-background px-8 py-6">
        <div className="mb-4">
          <Link to="/memories">
            <Button variant="ghost" size="sm">
              <ArrowLeft className="h-4 w-4 mr-1" />
              Back to Memories
            </Button>
          </Link>
        </div>

        {/* Breadcrumb */}
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

        {/* Title and metadata */}
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 mb-2">
              <h1 className="text-3xl font-semibold font-mono truncate">
                {data.key ?? "(no key)"}
              </h1>
              <CopyButton value={data.key ?? ""} />
            </div>

            {/* Badges */}
            <div className="flex items-center gap-2 flex-wrap">
              {data.archived ? (
                <Badge variant="secondary" className="bg-slate-100 text-slate-700">
                  <Archive className="w-3 h-3 mr-1" />
                  Archived
                  {data.archivedAt && (
                    <span className="ml-1 text-xs">
                      {new Date(data.archivedAt).toISOString().slice(0, 10)}
                    </span>
                  )}
                </Badge>
              ) : (
                <Badge variant="outline" className="text-emerald-700 border-emerald-300">
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

      {/* Metadata bar */}
      <div className="border-b border-border bg-muted/30 px-8 py-4">
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

      {/* Content */}
      <div className="flex-1 overflow-auto p-8">
        <div className="space-y-6">
          {/* Value */}
          <JsonCard label="Value" data={data.value} />

          {/* Attributes */}
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
    <div className="border rounded-md bg-card">
      <div className="flex items-center justify-between px-4 py-2 border-b">
        <div className="text-sm font-medium">{label}</div>
        <CopyButton value={text} />
      </div>
      <div className="p-4">
        <JsonHighlight
          value={data}
          className="text-xs font-mono overflow-x-auto whitespace-pre-wrap"
        />
      </div>
    </div>
  );
}

// Made with Bob
