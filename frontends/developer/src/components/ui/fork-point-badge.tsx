import { useState, useEffect } from "react";
import { GitFork, Loader2 } from "lucide-react";
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
import type { Entry } from "@/api/client";

interface ForkPointBadgeProps {
  /** List of forks that diverge at this point */
  forksAtPoint?: ForkOption[];
  /** Visual variant */
  variant?: "default" | "terminal" | "slate";
  /** Current channel filter to preserve in navigation */
  channelFilter?: string;
}

/** Extract text from an entry's content blocks */
function entryText(entry: Entry): string {
  const blocks = entry.content ?? [];
  const textBlock = blocks.find((b) => {
    const block = b as { [key: string]: unknown } | undefined;
    return block && typeof block.text === "string";
  }) as { text: string } | undefined;
  return textBlock?.text ?? "";
}

/** Get author from entry content blocks */
function entryAuthor(entry: Entry): "user" | "assistant" {
  const blocks = entry.content ?? [];
  for (const block of blocks) {
    const item = block as { [key: string]: unknown } | undefined;
    const role =
      item && typeof item.role === "string"
        ? (item.role as string).toUpperCase()
        : undefined;
    if (role === "USER") {
      return "user";
    }
    if (role === "AI" || role === "ASSISTANT") {
      return "assistant";
    }
  }
  return entry.userId ? "user" : "assistant";
}

/** Get the first user message text from entries AFTER the fork point */
function selectForkLabel(entries: Entry[], forkedAtEntryId?: string | null): string {
  if (!entries.length) {
    return "";
  }

  // Find entries after the fork point
  let entriesAfterFork = entries;
  if (forkedAtEntryId) {
    const forkIndex = entries.findIndex((e) => e.id === forkedAtEntryId);
    if (forkIndex >= 0) {
      // Get entries AFTER the fork point (not including the fork point itself)
      entriesAfterFork = entries.slice(forkIndex + 1);
    }
  }

  const userEntries = entriesAfterFork.filter((entry) => entryAuthor(entry) === "user");
  if (!userEntries.length) {
    return "";
  }
  // Get the first user entry after the fork point
  return entryText(userEntries[0]);
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
export function ForkPointBadge({
  forksAtPoint = [],
  variant = "default",
  channelFilter,
}: ForkPointBadgeProps) {
  const hasForks = forksAtPoint.length > 0;
  const [isOpen, setIsOpen] = useState(false);
  const [forkLabels, setForkLabels] = useState<Record<string, string>>({});
  const [isLoading, setIsLoading] = useState(false);

  // Fetch entries for forks when dropdown opens
  useEffect(() => {
    if (!isOpen || !hasForks) {
      return;
    }

    const missing = forksAtPoint.filter(
      (fork) => fork.conversationId && !(fork.conversationId in forkLabels),
    );

    if (!missing.length) {
      return;
    }

    let cancelled = false;

    const fetchLabels = async () => {
      setIsLoading(true);
      
      const results = await Promise.all(
        missing.map(async (fork) => {
          try {
            const response = await fetch(
              `/v1/admin/conversations/${fork.conversationId}/entries?channel=history&limit=50&forks=all`,
              {
                headers: {
                  Authorization: `Bearer ${localStorage.getItem("access_token") || ""}`,
                },
              }
            );
            const data = await response.json();
            const entries = Array.isArray(data?.data) ? data.data : [];
            const label = selectForkLabel(entries, fork.forkedAtEntryId);
            return { id: fork.conversationId, label };
          } catch {
            return { id: fork.conversationId, label: "" };
          }
        }),
      );

      if (cancelled) {
        return;
      }

      setForkLabels((prev) => {
        const next = { ...prev };
        for (const result of results) {
          if (result) {
            next[result.id] = result.label;
          }
        }
        return next;
      });
      setIsLoading(false);
    };

    fetchLabels();

    return () => {
      cancelled = true;
    };
  }, [isOpen, hasForks, forksAtPoint, forkLabels]);

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
    default:
      "fork-point-badge inline-flex items-center gap-1 text-xs text-primary px-2 py-0.5",
    terminal:
      "text-[10px] font-medium px-2 py-0.5 bg-purple-500/20 text-purple-400 rounded uppercase tracking-wider flex items-center space-x-1",
    slate:
      "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-primary/20 text-primary",
  };

  const badge = (
    <span
      className={cn(
        badgeStyles[variant],
        hasForks && "cursor-pointer hover:opacity-80",
      )}
    >
      <GitFork className="w-3 h-3" />
      <span>Forks</span>
      {hasForks && (
        <span className="ml-1 opacity-70">({forksAtPoint.length})</span>
      )}
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
          <GitFork className="w-4 h-4" />
          <span>Fork Branches ({forksAtPoint.length})</span>
          {isLoading && <Loader2 className="w-3 h-3 animate-spin ml-auto" />}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {forksAtPoint.map((fork) => {
          const label = forkLabels[fork.conversationId];
          const formattedLabel = label ? formatForkLabel(label) : undefined;

          return (
            <DropdownMenuItem
              key={fork.conversationId}
              className={cn("flex flex-col items-start gap-1 cursor-pointer", fork.isActive && "bg-accent")}
              onSelect={() => {
                const searchParams = buildSearchParams(fork.forkedAtEntryId);
                const queryString = searchParams
                  ? "?" + new URLSearchParams(searchParams).toString()
                  : "";
                window.location.href = `/conversations/${fork.conversationId}${queryString}`;
              }}
            >
              <div className="flex items-center justify-between w-full">
                <code className="text-xs font-mono truncate max-w-[200px]">
                  {fork.conversationId}
                </code>
                {fork.isActive && (
                  <span className="text-[10px] font-medium text-primary px-1.5 py-0.5 bg-primary/10 rounded">
                    Active
                  </span>
                )}
              </div>
              {formattedLabel && (
                <span className="text-xs text-muted-foreground italic">
                  "{formattedLabel}"
                </span>
              )}
              {fork.createdAt && (
                <span className="text-[10px] text-muted-foreground">
                  Created {formatDate(fork.createdAt)}
                </span>
              )}
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// Made with Bob