import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { GitFork } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./dropdown-menu";
import { cn } from "@/lib/utils";
import type { ForkOption } from "@/hooks";

interface ForkPointBadgeProps {
  /** List of forks that diverge at this point */
  forksAtPoint?: ForkOption[];
  /** Visual variant */
  variant?: "default" | "terminal" | "slate";
  /** Current channel filter to preserve in navigation */
  channelFilter?: string;
}

/** Format fork label to 40 characters */
function formatForkLabel(text: string): string {
  const trimmed = text.trim();
  if (!trimmed) {
    return "";
  }
  return trimmed.length <= 40 ? trimmed : `${trimmed.slice(0, 37)}...`;
}

/**
 * A badge that indicates a fork point with a dropdown showing all forks
 * that diverge at this point. Clicking the badge opens the dropdown.
 */
export function ForkPointBadge({ forksAtPoint = [], variant = "default", channelFilter }: ForkPointBadgeProps) {
  const hasForks = forksAtPoint.length > 0;
  const [isOpen, setIsOpen] = useState(false);
  const navigate = useNavigate();

  const formatDate = (dateString?: string | null) => {
    if (!dateString) return "";
    const date = new Date(dateString);
    return date.toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      year: "numeric",
    });
  };

  // Build search params preserving channel filter and adding forkedAt for navigation
  const buildSearchParams = (forkedAtEntryId?: string | null): Record<string, string> | undefined => {
    const params: Record<string, string> = {};
    if (channelFilter && channelFilter !== "all") {
      params.channel = channelFilter;
    }
    if (forkedAtEntryId) {
      params.forkedAt = forkedAtEntryId;
    }
    return Object.keys(params).length > 0 ? params : undefined;
  };

  const badgeStyles = {
    default: "fork-point-badge inline-flex items-center gap-1 text-xs text-primary px-2 py-0.5",
    terminal:
      "flex items-center space-x-1 rounded bg-[#eadccd] px-2 py-0.5 text-[10px] font-medium uppercase tracking-wider text-[#98613d]",
    slate: "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-primary/20 text-primary",
  };

  const badge = (
    <span className={cn(badgeStyles[variant], hasForks && "cursor-pointer hover:opacity-80")}>
      <GitFork className="h-3 w-3" />
      <span>Forks</span>
      {hasForks && <span className="ml-1 opacity-70">({forksAtPoint.length})</span>}
    </span>
  );

  if (!hasForks) {
    return badge;
  }

  return (
    <DropdownMenu open={isOpen} onOpenChange={setIsOpen}>
      <DropdownMenuTrigger asChild>{badge}</DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80">
        <DropdownMenuLabel className="flex items-center gap-2">
          <GitFork className="h-4 w-4" />
          <span>Fork Branches ({forksAtPoint.length})</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {forksAtPoint.map((fork) => {
          const label = fork.label || fork.title;
          const formattedLabel = label ? formatForkLabel(label) : undefined;

          return (
            <DropdownMenuItem
              key={fork.conversationId}
              className={cn("flex cursor-pointer flex-col items-start gap-1", fork.isActive && "bg-accent")}
              onSelect={() => {
                const searchParams = buildSearchParams(fork.entryId);
                void navigate({
                  to: "/conversations/$conversationId",
                  params: { conversationId: fork.conversationId },
                  search: searchParams ?? {},
                });
              }}
            >
              <div className="flex w-full items-center justify-between">
                <code className="max-w-[200px] truncate font-mono text-xs">{fork.conversationId}</code>
                {fork.isActive && (
                  <span className="rounded bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                    Active
                  </span>
                )}
              </div>
              {fork.entryId && (
                <span className="text-[10px] text-muted-foreground">
                  Entry <code className="font-mono">{fork.entryId}</code>
                </span>
              )}
              {formattedLabel && <span className="text-xs italic text-muted-foreground">"{formattedLabel}"</span>}
              {fork.createdAt && (
                <span className="text-[10px] text-muted-foreground">Created {formatDate(fork.createdAt)}</span>
              )}
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// Made with Bob
