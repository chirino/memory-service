import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { Archive, ArchiveRestore, Eye } from "lucide-react";
import { useAdminConversations, useArchiveConversation, useUnarchiveConversation } from "@/hooks/useAdminApi";
import { useAuth } from "@/lib/auth";
import { formatRelativeTime, cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

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
      archiveMutation.mutate(conversationId);
    }
  };

  const handleUnarchive = (conversationId: string) => {
    unarchiveMutation.mutate(conversationId);
  };

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="border-b border-border bg-background px-8 py-6">
        <h1 className="mb-2 text-3xl font-semibold text-foreground">Conversations</h1>
        <p className="text-muted-foreground">Browse and inspect all conversations across users</p>
      </div>

      {/* Filters */}
      <div className="border-b border-border bg-background px-8 py-4">
        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">Show:</span>
          <div className="flex gap-1">
            <button
              onClick={() => setArchiveFilter("exclude")}
              className={cn(
                "rounded-md px-3 py-1.5 text-sm transition-colors",
                archiveFilter === "exclude"
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-accent/50",
              )}
            >
              Active
            </button>
            <button
              onClick={() => setArchiveFilter("include")}
              className={cn(
                "rounded-md px-3 py-1.5 text-sm transition-colors",
                archiveFilter === "include"
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-accent/50",
              )}
            >
              All
            </button>
            <button
              onClick={() => setArchiveFilter("only")}
              className={cn(
                "rounded-md px-3 py-1.5 text-sm transition-colors",
                archiveFilter === "only"
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-accent/50",
              )}
            >
              Archived
            </button>
          </div>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-auto p-8">
        {isLoading && (
          <div className="flex items-center justify-center py-12">
            <div className="text-center">
              <div className="mb-4 inline-block h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
              <p className="text-sm text-muted-foreground">Loading conversations...</p>
            </div>
          </div>
        )}

        {error && (
          <div className="rounded-lg border border-destructive/50 bg-destructive/10 p-4 text-center">
            <p className="text-sm text-destructive">Failed to load conversations: {error.message}</p>
          </div>
        )}

        {!isLoading && !error && conversations.length === 0 && (
          <div className="rounded-lg border border-border bg-card p-12 text-center">
            <p className="text-muted-foreground">
              {archiveFilter === "only" ? "No archived conversations found" : "No conversations found"}
            </p>
          </div>
        )}

        {!isLoading && !error && conversations.length > 0 && (
          <div className="overflow-hidden rounded-lg border border-border bg-card">
            <table className="w-full">
              <thead className="border-b border-border bg-muted/50">
                <tr>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground">Title</th>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground">Owner</th>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground">Created</th>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground">Updated</th>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground">Status</th>
                  <th className="px-4 py-3 text-right text-sm font-medium text-muted-foreground">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {conversations.map((conversation) => (
                  <tr key={conversation.id} className="hover:bg-muted/30 transition-colors">
                    <td className="px-4 py-3">
                      <Link
                        to="/conversations/$conversationId"
                        params={{ conversationId: conversation.id }}
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
                    <td className="px-4 py-3">
                      <span className="text-sm text-foreground">{conversation.ownerUserId}</span>
                    </td>
                    <td className="px-4 py-3">
                      <span className="text-sm text-muted-foreground">
                        {formatRelativeTime(conversation.createdAt)}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <span className="text-sm text-muted-foreground">
                        {formatRelativeTime(conversation.updatedAt)}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      {conversation.archived ? (
                        <Badge variant="secondary">Archived</Badge>
                      ) : (
                        <Badge variant="outline">Active</Badge>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-2">
                        <Link to="/conversations/$conversationId" params={{ conversationId: conversation.id }}>
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
                                onClick={() => handleUnarchive(conversation.id)}
                                disabled={unarchiveMutation.isPending}
                              >
                                <ArchiveRestore className="h-4 w-4" />
                                Unarchive
                              </Button>
                            ) : (
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => handleArchive(conversation.id)}
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
                ))}
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
