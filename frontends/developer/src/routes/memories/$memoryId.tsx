import * as React from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Archive, ArrowLeft, ChevronDown, Clock, Hash, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { TimestampPopover } from "@/components/ui/timestamp-popover";
import { JsonHighlight, formatJson } from "@/components/content-renderers/JsonHighlight";
import { useAdminMemory } from "@/hooks/useAdminApi";
import { cn } from "@/lib/utils";
import {
  COGNITION_KIND_LABELS,
  type CognitionKind,
  describeConfidence,
  getCognitionKind,
  normalizeCognitionMemoryValue,
} from "@/lib/cognition";

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

  const cognitionKind = getCognitionKind(data.namespace);

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
            <div className="mb-1.5 text-[0.7rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              Memory key
            </div>
            <div className="flex items-center gap-2 mb-2">
              <h1 className="truncate font-mono text-lg font-medium text-foreground md:text-xl">
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
          {cognitionKind ? (
            <CognitionMemoryCard kind={cognitionKind} value={data.value} attributes={data.attributes} />
          ) : (
            <>
              <JsonCard label="Value" data={data.value} />

              {data.attributes && Object.keys(data.attributes).length > 0 && (
                <JsonCard label="Attributes" data={data.attributes} />
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

type ViewMode = "rendered" | "raw";

function CognitionMemoryCard({
  kind,
  value,
  attributes,
}: {
  kind: CognitionKind;
  value: unknown;
  attributes?: Record<string, unknown>;
}) {
  const [viewMode, setViewMode] = React.useState<ViewMode>("rendered");
  const parsedValue = React.useMemo(() => normalizeCognitionMemoryValue(value), [value]);
  const processedAt = getString(parsedValue.provenance?.processed_at);
  const subject = getString(attributes?.sub);
  const content = getString(parsedValue.content);

  const numericConfidence =
    typeof parsedValue.confidence === "number" ? parsedValue.confidence : Number(parsedValue.confidence);
  const hasConfidence = Number.isFinite(numericConfidence);

  const citations = React.useMemo(
    () => (parsedValue.citations ?? []).filter((citation) => citation !== null && citation !== undefined),
    [parsedValue.citations],
  );

  const provenance = parsedValue.provenance ?? {};
  const conversationId = getString(provenance.conversation_id);
  const sourceEntryCount = Array.isArray(provenance.entry_ids) ? provenance.entry_ids.length : 0;

  const rawText = React.useMemo(() => {
    try {
      return formatJson({
        value,
        attributes,
      });
    } catch {
      return String(value);
    }
  }, [attributes, value]);

  return (
    <div className="console-panel animate-fade-in rounded-2xl">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[rgba(43,39,34,0.1)] px-5 py-3">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
          <span className="font-mono">cognition.v1/{kind}</span>
          {processedAt && (
            <>
              <span aria-hidden>·</span>
              <span>
                Processed <time dateTime={processedAt}>{new Date(processedAt).toLocaleString()}</time>
              </span>
            </>
          )}
        </div>
        <div className="flex items-center gap-2">
          <div className="console-segmented">
            {(
              [
                ["rendered", "Rendered"],
                ["raw", "JSON"],
              ] as const
            ).map(([mode, label]) => (
              <button
                key={mode}
                type="button"
                onClick={() => setViewMode(mode)}
                className={cn("console-segment min-w-0 px-4", viewMode === mode && "console-segment-active")}
              >
                {label}
              </button>
            ))}
          </div>
          <CopyButton value={rawText} />
        </div>
      </div>

      {viewMode === "rendered" ? (
        <div className="p-5 md:p-6">
          <div className="flex flex-col gap-6 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0 flex-1">
              <div className="mb-3 text-[0.7rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                {COGNITION_KIND_LABELS[kind]}
                {subject && (
                  <>
                    {" · about "}
                    <span className="text-foreground">{subject}</span>
                  </>
                )}
              </div>
              <p className="console-title text-2xl leading-[1.3] text-foreground md:text-[1.75rem]">
                {content || "No content provided."}
              </p>
            </div>
            {hasConfidence && (
              <div className="flex shrink-0 flex-row items-center gap-3 sm:flex-col sm:items-end sm:gap-2">
                <ConfidenceRing value={numericConfidence} />
                <div className="text-sm font-medium text-foreground sm:text-right">
                  {describeConfidence(numericConfidence)}
                </div>
              </div>
            )}
          </div>

          {citations.length > 0 && (
            <TestimonyBlock citations={citations} conversationId={conversationId} entryCount={sourceEntryCount} />
          )}

          <ProvenanceDetails provenance={provenance} />
        </div>
      ) : (
        <div className="space-y-4 p-5">
          <JsonHighlight
            value={value}
            className="console-code overflow-x-auto whitespace-pre-wrap rounded-lg p-4 font-mono text-xs leading-6"
          />
          {attributes && Object.keys(attributes).length > 0 && (
            <JsonHighlight
              value={attributes}
              className="console-code overflow-x-auto whitespace-pre-wrap rounded-lg p-4 font-mono text-xs leading-6"
            />
          )}
        </div>
      )}
    </div>
  );
}

function ConfidenceRing({ value }: { value: number }) {
  const pct = Math.max(0, Math.min(1, value));
  const size = 78;
  const stroke = 6;
  const radius = (size - stroke) / 2;
  const circumference = 2 * Math.PI * radius;
  const center = size / 2;

  const [mounted, setMounted] = React.useState(false);
  React.useEffect(() => {
    const frame = requestAnimationFrame(() => setMounted(true));
    return () => cancelAnimationFrame(frame);
  }, []);
  const dashOffset = circumference * (1 - (mounted ? pct : 0));

  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      role="img"
      aria-label={`Confidence ${Math.round(pct * 100)} percent`}
      className="shrink-0"
    >
      <circle cx={center} cy={center} r={radius} fill="none" stroke="rgba(43,39,34,0.1)" strokeWidth={stroke} />
      <circle
        cx={center}
        cy={center}
        r={radius}
        fill="none"
        stroke="hsl(var(--primary))"
        strokeWidth={stroke}
        strokeLinecap="round"
        strokeDasharray={circumference}
        strokeDashoffset={dashOffset}
        transform={`rotate(-90 ${center} ${center})`}
        className="confidence-ring-progress"
      />
      <text
        x={center}
        y={center}
        textAnchor="middle"
        dominantBaseline="central"
        className="console-title"
        style={{ fill: "hsl(var(--foreground))", fontSize: "1.15rem" }}
      >
        {Math.round(pct * 100)}
        <tspan dy="-0.5em" style={{ fontSize: "0.6rem" }}>
          %
        </tspan>
      </text>
    </svg>
  );
}

function TestimonyBlock({
  citations,
  conversationId,
  entryCount,
}: {
  citations: unknown[];
  conversationId?: string;
  entryCount: number;
}) {
  return (
    <section className="mt-6 border-t border-[rgba(43,39,34,0.1)] pt-6">
      <div className="mb-3 text-[0.7rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
        In their words
      </div>
      <div className="space-y-3">
        {citations.map((citation, index) => (
          <figure
            key={index}
            className="relative overflow-hidden rounded-xl border border-[rgba(43,39,34,0.08)] bg-[rgba(236,230,219,0.4)] px-6 py-5"
          >
            <span aria-hidden className="memory-quote-mark">
              &ldquo;
            </span>
            <blockquote className="console-title relative pl-9 text-lg leading-7 text-stone">
              {formatScalar(citation)}
            </blockquote>
          </figure>
        ))}
      </div>
      {(conversationId || entryCount > 0) && (
        <div className="mt-2.5 text-xs text-muted-foreground">
          {conversationId && (
            <>
              Extracted from conversation{" "}
              <Link
                to="/conversations/$conversationId"
                params={{ conversationId }}
                className="font-mono text-primary hover:underline"
              >
                {conversationId.slice(0, 8)}
              </Link>
            </>
          )}
          {entryCount > 0 && (
            <>
              {" · "}
              {entryCount} source message{entryCount !== 1 ? "s" : ""}
            </>
          )}
        </div>
      )}
    </section>
  );
}

function ProvenanceDetails({ provenance }: { provenance: Record<string, unknown> }) {
  const entries = Object.entries(provenance).filter(([, value]) => value !== null && value !== undefined && value !== "");

  if (entries.length === 0) {
    return null;
  }

  const conversationId = getString(provenance.conversation_id);

  return (
    <details className="mt-6 border-t border-[rgba(43,39,34,0.1)] pt-4">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[0.7rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground transition-colors hover:text-foreground">
        <ChevronDown className="provenance-chevron h-3.5 w-3.5" />
        Provenance
      </summary>
      <dl className="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2">
        {entries.map(([key, value]) => (
          <div key={key} className="flex flex-col gap-1 border-b border-[rgba(43,39,34,0.06)] pb-3">
            <dt className="text-[0.7rem] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
              {formatAttributeLabel(key)}
            </dt>
            <dd className="break-words font-mono text-xs leading-5 text-foreground">
              {renderProvenanceValue(key, value, conversationId)}
            </dd>
          </div>
        ))}
      </dl>
    </details>
  );
}

function renderProvenanceValue(key: string, value: unknown, conversationId?: string): React.ReactNode {
  if (key === "conversation_id" && typeof value === "string") {
    return (
      <Link
        to="/conversations/$conversationId"
        params={{ conversationId: value }}
        className="break-all text-primary hover:underline"
      >
        {value}
      </Link>
    );
  }

  if (key === "entry_ids" && Array.isArray(value)) {
    const ids = value.filter((id): id is string => typeof id === "string");
    if (ids.length === 0) {
      return formatProvenanceValue(key, value);
    }
    if (!conversationId) {
      return ids.join(", ");
    }
    return (
      <div className="flex flex-col gap-1">
        {ids.map((id) => (
          <Link
            key={id}
            to="/conversations/$conversationId"
            params={{ conversationId }}
            search={{ entryId: id }}
            className="break-all text-primary hover:underline"
          >
            {id}
          </Link>
        ))}
      </div>
    );
  }

  return formatProvenanceValue(key, value);
}

function formatProvenanceValue(key: string, value: unknown): string {
  if (key.toLowerCase().endsWith("_at") && typeof value === "string") {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) return date.toLocaleString();
  }
  return formatScalar(value);
}

function getString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function formatScalar(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (Array.isArray(value)) return value.map(formatScalar).join(", ");
  if (typeof value === "object") return formatJson(value);
  return String(value);
}

function formatAttributeLabel(key: string): string {
  return key
    .replace(/_/g, " ")
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/\b\w/g, (match) => match.toUpperCase());
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
