import { Popover, PopoverContent, PopoverTrigger } from "./popover";
import { CopyButton } from "./copy-button";
import { cn } from "@/lib/utils";

export interface TimestampPopoverProps {
  /** The ISO timestamp string from the entry JSON */
  timestamp: string;
  /** Optional formatted display text. If not provided, will format the timestamp */
  displayText?: string;
  /** Optional className for the trigger */
  className?: string;
}

/**
 * A clickable timestamp that shows a popover with the full ISO timestamp
 * and a copy button.
 *
 * @example
 * ```tsx
 * <TimestampPopover timestamp={entry.createdAt} />
 * ```
 *
 * @example With custom display text
 * ```tsx
 * <TimestampPopover
 *   timestamp={entry.createdAt}
 *   displayText={formatDate(entry.createdAt)}
 * />
 * ```
 */
export function TimestampPopover({
  timestamp,
  displayText,
  className,
}: TimestampPopoverProps) {
  const formattedDisplay =
    displayText ||
    new Date(timestamp).toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      year: "numeric",
      hour: "numeric",
      minute: "2-digit",
      hour12: true,
    });

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            "text-xs text-muted-foreground hover:text-foreground hover:underline transition-colors cursor-pointer",
            className,
          )}
        >
          {formattedDisplay}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-3" align="end">
        <div className="space-y-2">
          <p className="text-xs font-medium text-muted-foreground">
            Full Timestamp
          </p>
          <div className="flex items-center gap-2">
            <code className="text-sm font-mono bg-muted px-2 py-1 rounded select-all">
              {timestamp}
            </code>
            <CopyButton value={timestamp} iconSize={4} />
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}

// Made with Bob