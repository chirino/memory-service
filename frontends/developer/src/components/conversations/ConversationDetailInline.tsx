import { Link } from "@tanstack/react-router";
import { Archive, Loader2, User, Hash } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { useAdminConversation, useAdminConversationEntries } from "@/hooks/useAdminApi";
import { formatRelativeTime } from "@/lib/utils";
import { EntryCard } from "@/components/conversations/EntryCard";
import type { Entry } from "@/api/client";

interface ConversationDetailInlineProps {
  conversationId: string;
  highlightEntryId?: string;
}

export function ConversationDetailInline({ conversationId, highlightEntryId }: ConversationDetailInlineProps) {
  const { data: conversation, isLoading: isLoadingConversation, error: conversationError } = useAdminConversation(conversationId);
  const { data: entriesData, isLoading: isLoadingEntries, error: entriesError } = useAdminConversationEntries(conversationId, {
    forks: "none",
    limit: 10,
  });

  if (isLoadingConversation || isLoadingEntries) {
    return (
      <div className="flex items-center justify-center py-8">
        <Loader2 className="h-6 w-6 animate-spin text-primary" />
      </div>
    );
  }

  if (conversationError || entriesError) {
    return (
      <div className="py-4 text-center text-sm text-destructive">
        {((conversationError || entriesError) as Error).message || "Failed to load conversation details"}
      </div>
    );
  }

  if (!conversation) {
    return null;
  }

  const entries = (entriesData?.data || []) as Entry[];

  return (
    <div className="space-y-4 pt-4">
      {/* Metadata badges */}
      <div className="flex items-center gap-2 flex-wrap">
        {conversation.archived ? (
          <Badge variant="secondary">
            <Archive className="w-3 h-3 mr-1" />
            Archived
          </Badge>
        ) : (
          <Badge>Active</Badge>
        )}
        {conversation.ownerUserId && (
          <Badge variant="outline">
            <User className="w-3 h-3 mr-1" />
            {conversation.ownerUserId}
          </Badge>
        )}
        {entries.length > 0 && (
          <Badge variant="outline">
            <Hash className="w-3 h-3 mr-1" />
            {entries.length} {entries.length === 1 ? "entry" : "entries"}
          </Badge>
        )}
      </div>

      {/* Conversation metadata */}
      <div className="grid grid-cols-2 gap-4 text-sm">
        <div>
          <span className="text-muted-foreground">Conversation ID</span>
          <p className="mt-1 font-mono text-xs text-foreground break-all">{conversation.id}</p>
        </div>
        <div>
          <span className="text-muted-foreground">Created</span>
          <p className="mt-1 text-xs text-foreground">
            {conversation.createdAt ? formatRelativeTime(conversation.createdAt) : "—"}
          </p>
        </div>
      </div>

      {/* Entries preview */}
      {entries.length > 0 && (
        <div className="space-y-3">
          <div className="text-sm font-medium text-muted-foreground">
            Recent Entries {highlightEntryId && "(showing highlighted entry)"}
          </div>
          <div className="space-y-2">
            {entries
              .filter((entry: Entry) => !highlightEntryId || entry.id === highlightEntryId)
              .slice(0, 3)
              .map((entry: Entry) => (
                <div
                  key={entry.id}
                  className={`console-panel rounded-lg p-3 ${entry.id === highlightEntryId ? "ring-2 ring-primary" : ""}`}
                >
                  <EntryCard entry={entry} compact={false} />
                </div>
              ))}
          </div>
          {entries.length > 3 && (
            <Link
              to="/conversations/$conversationId"
              params={{ conversationId }}
              search={highlightEntryId ? { entryId: highlightEntryId } : undefined}
              className="inline-block text-sm text-primary hover:underline"
            >
              View all entries →
            </Link>
          )}
        </div>
      )}
    </div>
  );
}

// Made with Bob
