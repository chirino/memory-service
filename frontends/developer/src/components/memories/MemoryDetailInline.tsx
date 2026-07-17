import * as React from "react";
import { Link } from "@tanstack/react-router";
import { Archive, ChevronDown, Clock, Hash, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
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
import { ExpandableEntryLink } from "./ExpandableEntryLink";

interface MemoryDetailInlineProps {
  memoryId: string;
}

export function MemoryDetailInline({ memoryId }: MemoryDetailInlineProps) {
  const { data, isLoading, error } = useAdminMemory(memoryId);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-8">
        <Loader2 className="h-6 w-6 animate-spin text-primary" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="py-4 text-center text-sm text-destructive">
        {(error as Error).message || "Failed to load memory details"}
      </div>
    );
  }

  if (!data) {
    return null;
  }

  const cognitionKind = getCognitionKind(data.namespace);

  return (
    <div className="space-y-4 pt-4">
      {/* Metadata badges */}
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
          <Badge>Active</Badge>
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

      {/* Memory ID and timestamps */}
      <div className="grid grid-cols-2 gap-4 text-sm">
        <div>
          <span className="text-muted-foreground">Memory ID</span>
          <p className="mt-1 font-mono text-xs text-foreground">{data.id}</p>
        </div>
        <div>
          <span className="text-muted-foreground">Created</span>
          <p className="mt-1 text-xs text-foreground">
            {data.createdAt ? new Date(data.createdAt).toISOString() : "—"}
          </p>
        </div>
      </div>

      {/* Content */}
      {cognitionKind ? (
        <CognitionMemoryCardInline kind={cognitionKind} value={data.value} attributes={data.attributes} />
      ) : (
        <>
          <JsonCardInline label="Value" data={data.value} />
          {data.attributes && Object.keys(data.attributes).length > 0 && (
            <JsonCardInline label="Attributes" data={data.attributes} />
          )}
        </>
      )}
    </div>
  );
}

type ViewMode = "rendered" | "raw";

function CognitionMemoryCardInline({
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
    <div className="console-panel animate-fade-in rounded-xl">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[rgba(43,39,34,0.1)] px-4 py-2">
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
                className={cn("console-segment min-w-0 px-3 py-1 text-xs", viewMode === mode && "console-segment-active")}
              >
                {label}
              </button>
            ))}
          </div>
          <CopyButton value={rawText} />
        </div>
      </div>

      {viewMode === "rendered" ? (
        <div className="p-4">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0 flex-1">
              <div className="mb-2 text-[0.7rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                {COGNITION_KIND_LABELS[kind]}
                {subject && (
                  <>
                    {" · about "}
                    <span className="text-foreground">{subject}</span>
                  </>
                )}
              </div>
              <p className="console-title text-lg leading-[1.3] text-foreground">
                {content || "No content provided."}
              </p>
            </div>
            {hasConfidence && (
              <div className="flex shrink-0 flex-row items-center gap-2 sm:flex-col sm:items-end">
                <ConfidenceRing value={numericConfidence} size={60} />
                <div className="text-xs font-medium text-foreground sm:text-right">
                  {describeConfidence(numericConfidence)}
                </div>
              </div>
            )}
          </div>

          {citations.length > 0 && (
            <TestimonyBlockInline citations={citations} conversationId={conversationId} entryCount={sourceEntryCount} />
          )}

          <ProvenanceDetailsInline provenance={provenance} />
        </div>
      ) : (
        <div className="space-y-3 p-4">
          <JsonHighlight
            value={value}
            className="console-code overflow-x-auto whitespace-pre-wrap rounded-lg p-3 font-mono text-xs leading-5"
          />
          {attributes && Object.keys(attributes).length > 0 && (
            <JsonHighlight
              value={attributes}
              className="console-code overflow-x-auto whitespace-pre-wrap rounded-lg p-3 font-mono text-xs leading-5"
            />
          )}
        </div>
      )}
    </div>
  );
}

function ConfidenceRing({ value, size = 60 }: { value: number; size?: number }) {
  const pct = Math.max(0, Math.min(1, value));
  const stroke = 5;
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
        style={{ fill: "hsl(var(--foreground))", fontSize: "0.9rem" }}
      >
        {Math.round(pct * 100)}
        <tspan dy="-0.4em" style={{ fontSize: "0.5rem" }}>
          %
        </tspan>
      </text>
    </svg>
  );
}

function TestimonyBlockInline({
  citations,
  conversationId,
  entryCount,
}: {
  citations: unknown[];
  conversationId?: string;
  entryCount: number;
}) {
  return (
    <section className="mt-4 border-t border-[rgba(43,39,34,0.1)] pt-4">
      <div className="mb-2 text-[0.7rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
        In their words
      </div>
      <div className="space-y-2">
        {citations.map((citation, index) => (
          <figure
            key={index}
            className="relative overflow-hidden rounded-lg border border-[rgba(43,39,34,0.08)] bg-[rgba(236,230,219,0.4)] px-4 py-3"
          >
            <span aria-hidden className="memory-quote-mark text-sm">
              &ldquo;
            </span>
            <blockquote className="console-title relative pl-6 text-sm leading-6 text-stone">
              {formatScalar(citation)}
            </blockquote>
          </figure>
        ))}
      </div>
      {(conversationId || entryCount > 0) && (
        <div className="mt-2 text-xs text-muted-foreground">
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

function ProvenanceDetailsInline({ provenance }: { provenance: Record<string, unknown> }) {
  const entries = Object.entries(provenance).filter(([, value]) => value !== null && value !== undefined && value !== "");

  if (entries.length === 0) {
    return null;
  }

  const conversationId = getString(provenance.conversation_id);

  return (
    <details className="mt-4 border-t border-[rgba(43,39,34,0.1)] pt-3">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[0.7rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground transition-colors hover:text-foreground">
        <ChevronDown className="provenance-chevron h-3 w-3" />
        Provenance
      </summary>
      <dl className="mt-3 grid gap-x-6 gap-y-3 sm:grid-cols-2">
        {entries.map(([key, value]) => (
          <div key={key} className="flex flex-col gap-1 border-b border-[rgba(43,39,34,0.06)] pb-2">
            <dt className="text-[0.65rem] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
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
      <div className="flex flex-col gap-2">
        {ids.map((id) => (
          <ExpandableEntryLink key={id} entryId={id} />
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

interface JsonCardInlineProps {
  label: string;
  data: unknown;
}

function JsonCardInline({ label, data }: JsonCardInlineProps) {
  const text = React.useMemo(() => {
    if (data === undefined || data === null) return "(empty)";
    try {
      return formatJson(data);
    } catch {
      return String(data);
    }
  }, [data]);

  return (
    <div className="console-panel rounded-lg">
      <div className="flex items-center justify-between border-b border-[rgba(43,39,34,0.1)] px-3 py-2">
        <div className="text-sm font-medium">{label}</div>
        <CopyButton value={text} />
      </div>
      <div className="p-3">
        <JsonHighlight
          value={data}
          className="console-code overflow-x-auto whitespace-pre-wrap rounded-lg p-3 font-mono text-xs leading-5"
        />
      </div>
    </div>
  );
}

// Made with Bob
