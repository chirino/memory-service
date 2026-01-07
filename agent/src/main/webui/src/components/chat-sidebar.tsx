import type { ConversationSummary } from "@/client";
import { Button } from "@/components/ui/button";
import { ConversationHoverMenu } from "@/components/conversation-hover-menu";
import { Loader2 } from "lucide-react";

type ChatSidebarProps = {
  conversations: ConversationSummary[];
  search: string;
  onSearchChange: (value: string) => void;
  selectedConversationId: string | null;
  onSelectConversation: (conversation: ConversationSummary) => void;
  onNewChat: () => void;
  onSummarizeConversation?: (conversation: ConversationSummary) => void;
  onDeleteConversation?: (conversation: ConversationSummary) => void;
  statusMessage?: string | null;
  resumableConversationIds?: Set<string>;
};

function formatDateTime(value?: string) {
  if (!value) return "Unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

export function ChatSidebar({
  conversations,
  search,
  onSearchChange,
  selectedConversationId,
  onSelectConversation,
  onNewChat,
  onSummarizeConversation,
  onDeleteConversation,
  statusMessage,
  resumableConversationIds = new Set(),
}: ChatSidebarProps) {
  const filteredConversations = conversations.filter((conversation) => {
    if (!search.trim()) {
      return true;
    }
    const q = search.toLowerCase();
    return (
      (conversation.title ?? "").toLowerCase().includes(q) ||
      (conversation.lastMessagePreview ?? "").toLowerCase().includes(q)
    );
  });

  return (
    <aside className="flex w-80 flex-col border-r bg-background">
      <div className="flex items-center justify-between border-b px-4 py-3">
        <div>
          <h1 className="text-base font-semibold">Conversations</h1>
          <p className="text-xs text-muted-foreground">Browse and resume chats.</p>
        </div>
        <Button size="sm" variant="outline" onClick={onNewChat}>
          New chat
        </Button>
      </div>

      <div className="border-b p-3">
        <input
          type="search"
          placeholder="Search conversations"
          value={search}
          onChange={(event) => onSearchChange(event.target.value)}
          className="w-full rounded-md border px-3 py-1.5 text-sm outline-none ring-offset-background focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        />
      </div>
      {statusMessage && <div className="px-4 py-2 text-[11px] text-destructive">{statusMessage}</div>}

      <div className="flex-1 overflow-y-auto p-2">
        {filteredConversations.length === 0 && (
          <p className="px-2 text-xs text-muted-foreground">No conversations yet.</p>
        )}

        <div className="space-y-1">
          {filteredConversations.map((conversation) => {
            const isSelected = conversation.id === selectedConversationId;
            const isResumable = conversation.id ? resumableConversationIds.has(conversation.id) : false;
            return (
              <div key={conversation.id} className="group relative flex">
                <div className="flex-1">
                <button
                  type="button"
                  onClick={() => onSelectConversation(conversation)}
                  className={`w-full rounded-md px-3 py-2 pr-12 text-left text-xs ${
                    isSelected ? "bg-accent" : "hover:bg-accent/60"
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <div className="flex min-w-0 flex-1 items-center gap-2">
                      <span className="truncate text-sm font-medium">
                        {conversation.title || "Untitled conversation"}
                      </span>
                    </div>
                    {/* {conversation.accessLevel && (
                      <span className="ml-2 text-[10px] uppercase text-muted-foreground">
                        {conversation.accessLevel}
                      </span>
                    )} */}
                  </div>
                  <div className="mt-0.5 text-[10px] text-muted-foreground">
                    Updated {formatDateTime(conversation.updatedAt || conversation.createdAt)}
                  </div>
                </button>
                <ConversationHoverMenu
                  onSummarize={() => onSummarizeConversation?.(conversation)}
                  onDelete={() => onDeleteConversation?.(conversation)}
                />
                </div>
                {isResumable && <Loader2 className="h-6 w-6 z-10 flex-shrink-0 animate-spin text-muted-foreground" />}
              </div>
            );
          })}
        </div>
      </div>
    </aside>
  );
}
