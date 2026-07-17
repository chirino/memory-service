import { useState } from "react";
import { ChevronDown, ChevronRight, Loader2 } from "lucide-react";
import { useAdminEntry } from "@/hooks/useAdminApi";
import { EntryCard } from "@/components/conversations/EntryCard";

interface ExpandableEntryLinkProps {
  entryId: string;
}

export function ExpandableEntryLink({ entryId }: ExpandableEntryLinkProps) {
  const [isExpanded, setIsExpanded] = useState(false);
  const { data: entry, isLoading, error } = useAdminEntry(entryId);

  const toggleExpand = () => {
    setIsExpanded(!isExpanded);
  };

  return (
    <div className="border-l-2 border-primary/20 pl-2">
      <button
        onClick={toggleExpand}
        className="flex items-center gap-1.5 text-xs font-mono text-primary hover:underline"
      >
        {isExpanded ? (
          <ChevronDown className="h-3 w-3 shrink-0" />
        ) : (
          <ChevronRight className="h-3 w-3 shrink-0" />
        )}
        <span className="break-all">{entryId}</span>
      </button>

      {isExpanded && (
        <div className="mt-2 rounded-lg border border-[rgba(43,39,34,0.1)] bg-sage-soft/10 p-3">
          {isLoading && (
            <div className="flex items-center justify-center py-4">
              <Loader2 className="h-4 w-4 animate-spin text-primary" />
            </div>
          )}

          {error && (
            <div className="text-xs text-destructive">
              Failed to load entry: {(error as Error).message}
            </div>
          )}

          {entry && <EntryCard entry={entry} compact={false} />}
        </div>
      )}
    </div>
  );
}

// Made with Bob
