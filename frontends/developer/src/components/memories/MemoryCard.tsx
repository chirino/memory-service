import { useState } from "react";
import { ChevronDown, ChevronRight, Eye } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { formatRelativeTime } from "@/lib/utils";
import { MemoryDetailInline } from "./MemoryDetailInline";
import {
  COGNITION_KIND_LABELS,
  cognitionConfidence,
  describeConfidence,
  getCognitionKind,
  normalizeCognitionMemoryValue,
} from "@/lib/cognition";
import type { AdminMemory } from "@/hooks/useAdminApi";

interface MemoryCardProps {
  memory: AdminMemory;
  score?: number;
  highlights?: string;
  matchedQueries?: string[];
}

export function MemoryCard({ memory, score, highlights, matchedQueries }: MemoryCardProps) {
  const [isExpanded, setIsExpanded] = useState(false);

  if (!memory.id) {
    return null;
  }

  const memoryId = memory.id;
  const namespaceLabel = memory.namespace?.join(" / ") || "(no namespace)";
  const cognitionKind = getCognitionKind(memory.namespace);

  const toggleExpand = () => {
    setIsExpanded(!isExpanded);
  };

  if (cognitionKind) {
    const cognitionValue = normalizeCognitionMemoryValue(memory.value);
    const content = typeof cognitionValue.content === "string" ? cognitionValue.content : undefined;
    const confidence = cognitionConfidence(cognitionValue);
    const subject = typeof memory.attributes?.sub === "string" ? memory.attributes.sub : undefined;

    return (
      <div className="console-panel rounded-xl overflow-hidden">
        <button
          onClick={toggleExpand}
          className="w-full p-5 text-left transition-colors hover:bg-sage-soft/20"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0 flex-1">
              <div className="mb-2 flex flex-wrap items-center gap-2">
                {isExpanded ? (
                  <ChevronDown className="h-5 w-5 text-muted-foreground shrink-0" />
                ) : (
                  <ChevronRight className="h-5 w-5 text-muted-foreground shrink-0" />
                )}
                <Badge>{COGNITION_KIND_LABELS[cognitionKind]}</Badge>
                <span className="truncate font-mono text-xs text-muted-foreground">{namespaceLabel}</span>
                {memory.archived && <Badge variant="secondary">Archived</Badge>}
                {score !== undefined && (
                  <Badge variant="outline" className="font-mono text-xs">
                    {score.toFixed(3)}
                  </Badge>
                )}
              </div>
              <p className="text-lg font-medium leading-snug text-foreground">
                {content || <span className="font-mono text-base text-muted-foreground">{memory.key}</span>}
              </p>
              {!isExpanded && highlights && (
                <div className="mt-2 rounded-md bg-sage-soft/30 p-2 text-sm text-foreground">
                  {highlights}
                </div>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-3">
              {confidence !== null && <ConfidenceChip value={confidence} />}
              <ViewAffordance />
            </div>
          </div>

          {!isExpanded && (
            <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border pt-3 text-xs text-muted-foreground">
              <span>Created {formatRelativeTime(memory.createdAt)}</span>
              {memory.revision && <span>Rev {memory.revision}</span>}
              {memory.expiresAt && <span>Expires {formatRelativeTime(memory.expiresAt)}</span>}
              {subject && (
                <span>
                  about <span className="text-foreground">{subject}</span>
                </span>
              )}
              {matchedQueries && matchedQueries.length > 0 && (
                <span className="text-xs">Matched: {matchedQueries.join(", ")}</span>
              )}
              <span className="font-mono">{memoryId}</span>
            </div>
          )}
        </button>

        {isExpanded && (
          <div className="border-t border-[rgba(43,39,34,0.1)] px-5 pb-5">
            <MemoryDetailInline memoryId={memoryId} />
          </div>
        )}
      </div>
    );
  }

  // Non-cognition memory card
  return (
    <div className="console-panel rounded-xl overflow-hidden">
      <button
        onClick={toggleExpand}
        className="w-full p-5 text-left transition-colors hover:bg-sage-soft/20"
      >
        <div className="flex items-start justify-between">
          <div className="flex-1 min-w-0">
            <div className="mb-2 flex items-center gap-2">
              {isExpanded ? (
                <ChevronDown className="h-5 w-5 text-muted-foreground shrink-0" />
              ) : (
                <ChevronRight className="h-5 w-5 text-muted-foreground shrink-0" />
              )}
              <span className="font-mono text-sm font-medium text-foreground">{namespaceLabel}</span>
              {memory.archived && <Badge variant="secondary">Archived</Badge>}
              {score !== undefined && (
                <Badge variant="outline" className="font-mono text-xs">
                  {score.toFixed(3)}
                </Badge>
              )}
            </div>
            <div className="text-lg font-semibold text-foreground">{memory.key}</div>
            {!isExpanded && highlights && (
              <div className="mt-2 rounded-md bg-sage-soft/30 p-2 text-sm text-foreground">
                {highlights}
              </div>
            )}
            {!isExpanded && (
              <div className="mt-1 text-sm text-muted-foreground">
                Created {formatRelativeTime(memory.createdAt)}
                {memory.expiresAt && ` • Expires ${formatRelativeTime(memory.expiresAt)}`}
                {memory.revision && ` • Rev ${memory.revision}`}
                {matchedQueries && matchedQueries.length > 0 && (
                  <span className="ml-2 text-xs">Matched: {matchedQueries.join(", ")}</span>
                )}
              </div>
            )}
          </div>
          <ViewAffordance />
        </div>

        {!isExpanded && memory.attributes && Object.keys(memory.attributes).length > 0 && (
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

        {!isExpanded && (
          <div className="mt-3 border-t border-border pt-3 text-xs text-muted-foreground">
            Memory ID: <span className="font-mono">{memoryId}</span>
          </div>
        )}
      </button>

      {isExpanded && (
        <div className="border-t border-[rgba(43,39,34,0.1)] px-5 pb-5">
          <MemoryDetailInline memoryId={memoryId} />
        </div>
      )}
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
