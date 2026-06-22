import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { Archive, ArchiveRestore, Eye, SlidersHorizontal } from "lucide-react";
import { useAdminConversations, useArchiveConversation, useUnarchiveConversation } from "@/hooks/useAdminApi";
import { useAuth } from "@/lib/auth";
import { formatRelativeTime, cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

function getErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Unknown error";
}

export const Route = createFileRoute("/conversations/")({
  component: ConversationsPage,
});

function ConversationsPage() {
  const [archiveFilter, setArchiveFilter] = useState<"exclude" | "include" | "only">("exclude");
  const { data, isLoading, error } = useAdminConversations({ archived: archiveFilter, limit: 50 });
  const archiveMutation = useArchiveConversation();
  const unarchiveMutation = useUnarchiveConversation();
  const auth = useAuth();
  const isAdmin = auth.hasRole("admin");

  const conversations = data?.data || [];

  const handleArchive = (conversationId: string) => {
    if (confirm("Archive this conversation?")) {
      archiveMutation.mutate({
        path: { id: conversationId },
        body: { archived: true },
      });
    }
  };

  const handleUnarchive = (conversationId: string) => {
    unarchiveMutation.mutate({
      path: { id: conversationId },
      body: { archived: false },
    });
  };

  return (
    <div className="flex h-full flex-col">
      <div className="px-5 pb-6 pt-8 md:px-10 md:pt-10">
        <div className="flex items-start justify-between gap-6">
          <div>
            <h1 className="console-title text-4xl leading-tight text-foreground md:text-5xl">Conversations</h1>
            <p className="console-subtitle mt-3 text-base md:text-lg">Browse and inspect all conversations across users</p>
          </div>
          <button
            type="button"
            className="mt-3 inline-flex h-11 w-11 items-center justify-center rounded-lg border border-[rgba(43,39,34,0.12)] bg-white/70 text-primary transition-colors hover:bg-sage-soft/45"
            aria-label="Filter conversations"
          >
            <SlidersHorizontal className="h-5 w-5" strokeWidth={1.55} />
          </button>
        </div>

        <div className="mt-6 flex items-center gap-3">
          <div className="console-segmented">
            <button
              onClick={() => setArchiveFilter("exclude")}
              className={cn(
                "console-segment",
                archiveFilter === "exclude" && "console-segment-active",
              )}
            >
              Active
            </button>
            <button
              onClick={() => setArchiveFilter("include")}
              className={cn(
                "console-segment",
                archiveFilter === "include" && "console-segment-active",
              )}
            >
              All
            </button>
            <button
              onClick={() => setArchiveFilter("only")}
              className={cn(
                "console-segment",
                archiveFilter === "only" && "console-segment-active",
              )}
            >
              Archived
            </button>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-5 pb-8 md:px-10">
        {isLoading && (
          <div className="flex items-center justify-center py-12">
            <div className="text-center">
              <div className="mb-4 inline-block h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
              <p className="text-sm text-muted-foreground">Loading conversations...</p>
            </div>
          </div>
        )}

        {error && (
          <div className="console-panel rounded-xl p-4 text-center">
            <p className="text-sm text-destructive">Failed to load conversations: {getErrorMessage(error)}</p>
          </div>
        )}

        {!isLoading && !error && conversations.length === 0 && (
          <div className="console-panel rounded-xl p-12 text-center">
            <p className="text-muted-foreground">
              {archiveFilter === "only" ? "No archived conversations found" : "No conversations found"}
            </p>
          </div>
        )}

        {!isLoading && !error && conversations.length > 0 && (
          <div className="overflow-x-auto">
            <table className="console-table">
              <thead>
                <tr>
                  <th>Title</th>
                  <th>Owner</th>
                  <th>Created</th>
                  <th>Updated</th>
                  <th>Status</th>
                  <th className="text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {conversations.map((conversation) => {
                  if (!conversation.id) {
                    return null;
                  }
                  const conversationId = conversation.id;
                  return (
                  <tr key={conversationId}>
                    <td>
                      <Link
                        to="/conversations/$conversationId"
                        params={{ conversationId }}
                        className="font-medium text-foreground hover:text-primary hover:underline"
                      >
                        {conversation.title || "Untitled Conversation"}
                      </Link>
                        {conversation.clientId && (
                          <div className="mt-1 text-xs text-muted-foreground">
                            Client: {conversation.clientId}
                          </div>
                        )}
                    </td>
                    <td>
                      <span className="text-sm text-foreground">{conversation.ownerUserId}</span>
                    </td>
                    <td>
                      <span className="text-sm text-muted-foreground">
                        {formatRelativeTime(conversation.createdAt)}
                      </span>
                    </td>
                    <td>
                      <span className="text-sm text-muted-foreground">
                        {formatRelativeTime(conversation.updatedAt)}
                      </span>
                    </td>
                    <td>
                      {conversation.archived ? (
                        <Badge variant="secondary">Archived</Badge>
                      ) : (
                        <Badge>Active</Badge>
                      )}
                    </td>
                    <td>
                      <div className="flex items-center justify-end gap-2">
                        <Link to="/conversations/$conversationId" params={{ conversationId }}>
                          <Button variant="ghost" size="sm">
                            <Eye className="h-4 w-4" />
                            View
                          </Button>
                        </Link>
                        {isAdmin && (
                          <>
                            {conversation.archived ? (
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => handleUnarchive(conversationId)}
                                disabled={unarchiveMutation.isPending}
                              >
                                <ArchiveRestore className="h-4 w-4" />
                                Unarchive
                              </Button>
                            ) : (
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => handleArchive(conversationId)}
                                disabled={archiveMutation.isPending}
                              >
                                <Archive className="h-4 w-4" />
                                Archive
                              </Button>
                            )}
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {!isLoading && !error && conversations.length > 0 && (
          <div className="mt-4 text-center text-sm text-muted-foreground">
            Showing {conversations.length} conversation{conversations.length !== 1 ? "s" : ""}
          </div>
        )}
      </div>
    </div>
  );
}

// Made with Bob
