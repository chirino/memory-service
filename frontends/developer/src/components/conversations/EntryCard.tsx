import * as React from "react";
import { Badge } from "@/components/ui/badge";
import { CopyButton } from "@/components/ui/copy-button";
import { ForkPointBadge } from "@/components/ui/fork-point-badge";
import { TimestampPopover } from "@/components/ui/timestamp-popover";
import { ChevronDown, ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Entry } from "@/api/client";

export interface ForkOption {
  conversationId: string;
  title?: string;
}

interface EntryCardProps {
  entry: Entry & {
    isForkPoint?: boolean;
    forksAtPoint?: ForkOption[];
    indexedContent?: string;
  };
  compact?: boolean;
  // Timeline view props
  formatDate?: (date: string) => string;
  isHighlighted?: boolean;
  onClick?: () => void;
  channelFilter?: string;
  entryIdLabel?: string;
  entryIdTitle?: string;
}

const MAX_PREVIEW_LENGTH = 200;

function ExpandableField({ label, value }: { label: string; value: unknown }) {
  const [isExpanded, setIsExpanded] = React.useState(false);
  
  if (value === null || value === undefined) {
    return null;
  }

  // Handle arrays and objects by stringifying them
  const stringValue = typeof value === "string" 
    ? value 
    : Array.isArray(value)
      ? JSON.stringify(value, null, 2)
      : typeof value === "object"
        ? JSON.stringify(value, null, 2)
        : String(value);
  
  const needsExpansion = stringValue.length > MAX_PREVIEW_LENGTH;
  const displayValue = needsExpansion && !isExpanded 
    ? stringValue.slice(0, MAX_PREVIEW_LENGTH) + "..." 
    : stringValue;

  return (
    <div className="space-y-1">
      <div className="flex items-center gap-2">
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </span>
        {needsExpansion && (
          <button
            onClick={(e) => {
              e.stopPropagation();
              setIsExpanded(!isExpanded);
            }}
            className="flex items-center gap-1 text-xs text-primary hover:underline"
          >
            {isExpanded ? (
              <>
                <ChevronDown className="h-3 w-3" />
                Collapse
              </>
            ) : (
              <>
                <ChevronRight className="h-3 w-3" />
                Expand
              </>
            )}
          </button>
        )}
      </div>
      <div className="console-code overflow-x-auto whitespace-pre-wrap rounded-lg p-3 font-mono text-xs leading-5">
        {displayValue}
      </div>
    </div>
  );
}

export const EntryCard = React.forwardRef<HTMLDivElement, EntryCardProps>(
  (
    {
      entry,
      compact = false,
      formatDate,
      isHighlighted,
      onClick,
      channelFilter,
      entryIdLabel,
      entryIdTitle,
    },
    ref,
  ) => {
    const getChannelColor = (channel: string) => {
      switch (channel) {
        case "history":
          return "bg-sage-soft text-primary";
        case "context":
          return "bg-[#eadccd] text-[#98613d]";
        default:
          return "bg-secondary text-muted-foreground";
      }
    };

    // Extract roles from content array
    const extractRoles = (): string[] => {
      if (!entry.content || !Array.isArray(entry.content)) return [];
      
      return entry.content
        .map((item) => {
          if (typeof item === "object" && item !== null) {
            const obj = item as Record<string, unknown>;
            return obj.role ? String(obj.role) : null;
          }
          return null;
        })
        .filter((role): role is string => role !== null);
    };

    const roles = extractRoles();
    const rolesText = roles.length > 0 ? roles.join(", ") : null;

    // Compact view (for inline previews)
    if (compact) {
      return (
        <div className="space-y-2">
          <div className="flex items-center gap-2 flex-wrap">
            <Badge className={cn("text-xs font-medium", getChannelColor(entry.channel || ""))}>
              {entry.channel}
            </Badge>
            {rolesText && (
              <Badge variant="secondary" className="text-xs font-semibold">
                {rolesText}
              </Badge>
            )}
            <span className="font-mono text-xs text-muted-foreground">{entry.id?.slice(0, 8)}...</span>
          </div>
        </div>
      );
    }

    // Timeline view (with optional features)
    if (formatDate || onClick || isHighlighted !== undefined) {
      return (
        <div
          ref={ref}
          onClick={onClick}
          className={cn(
            "console-panel rounded-xl p-4 transition-all duration-300",
            onClick && "cursor-pointer hover:bg-sage-soft/20",
            entry.isForkPoint && "ring-1 ring-primary/20",
            isHighlighted && "border-primary ring-2 ring-primary ring-offset-2",
          )}
        >
          {/* Header */}
          <div className="mb-2 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Badge className={cn("text-xs font-medium", getChannelColor(entry.channel || ""))}>
                {entry.channel}
              </Badge>
              {entry.epoch !== undefined && (
                <Badge className="bg-[#eadccd] text-xs font-medium text-[#98613d]">
                  epoch: {entry.epoch}
                </Badge>
              )}
              <div className="flex min-w-0 flex-1 items-center gap-1">
                <span className="truncate font-mono text-xs text-muted-foreground" title={entryIdTitle ?? entry.id}>
                  {entryIdLabel ?? entry.id}
                </span>
                {!entryIdLabel && <CopyButton value={entry.id || ""} iconSize={3} className="shrink-0" />}
              </div>
              {entry.isForkPoint && (
                <ForkPointBadge forksAtPoint={entry.forksAtPoint} channelFilter={channelFilter} />
              )}
            </div>
            {formatDate && entry.createdAt && (
              <div onClick={(e) => e.stopPropagation()}>
                <TimestampPopover timestamp={entry.createdAt} displayText={formatDate(entry.createdAt)} />
              </div>
            )}
          </div>

          {/* User and content type */}
          <div className="mb-2 flex items-center justify-between text-sm text-muted-foreground">
            <div className="flex items-center gap-4">
              {entry.userId && <span>User: {entry.userId}</span>}
            </div>
            <div className="flex items-center gap-3">
              {entry.contentType && <span>Type: {entry.contentType}</span>}
              {rolesText && <span>Roles: {rolesText}</span>}
            </div>
          </div>

          {/* Expandable Raw Data Sections */}
          <div className="mt-3 space-y-3">
            {entry.indexedContent && (
              <ExpandableField label="Indexed Content" value={entry.indexedContent} />
            )}
            {entry.content && (
              <ExpandableField label="Raw Content" value={entry.content} />
            )}
          </div>
        </div>
      );
    }

    // Default search view (expandable fields)
    return (
      <div className="space-y-3">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 flex-wrap">
            <Badge className={cn("text-xs font-medium", getChannelColor(entry.channel || ""))}>
              {entry.channel}
            </Badge>
            {entry.epoch !== undefined && (
              <Badge className="bg-[#eadccd] text-xs font-medium text-[#98613d]">
                epoch: {entry.epoch}
              </Badge>
            )}
          </div>
        </div>

        {/* Entry ID */}
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs text-muted-foreground">{entry.id}</span>
          <CopyButton value={entry.id || ""} iconSize={3} />
        </div>

        {/* Metadata */}
        <div className="flex items-center gap-4 text-xs text-muted-foreground">
          {entry.userId && <span>User: {entry.userId}</span>}
          {entry.contentType && <span>Type: {entry.contentType}</span>}
          {rolesText && <span>Roles: {rolesText}</span>}
        </div>

        {/* Indexed Content */}
        {entry.indexedContent && (
          <ExpandableField label="Indexed Content" value={entry.indexedContent} />
        )}

        {/* Raw Content */}
        {entry.content && (
          <ExpandableField label="Raw Content" value={entry.content} />
        )}
      </div>
    );
  },
);

EntryCard.displayName = "EntryCard";

// Made with Bob