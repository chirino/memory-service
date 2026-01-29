import type { ConversationSummary } from "@/client";
import { Plus, Search } from "lucide-react";

type ChatSidebarProps = {
  conversations: ConversationSummary[];
  search: string;
  onSearchChange: (value: string) => void;
  selectedConversationId: string | null;
  onSelectConversation: (conversation: ConversationSummary) => void;
  onNewChat: () => void;
  statusMessage?: string | null;
  resumableConversationIds?: Set<string>;
};

function formatRelativeTime(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);
  const diffWeeks = Math.floor(diffDays / 7);

  if (diffMins < 1) return "now";
  if (diffMins < 60) return `${diffMins}m`;
  if (diffHours < 24) return `${diffHours}h`;
  if (diffDays < 7) {
    if (diffDays === 1) return "Yesterday";
    return `${diffDays}d`;
  }
  if (diffWeeks < 4) return `${diffWeeks}w`;

  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
  }).format(date);
}

export function ChatSidebar({
  conversations,
  search,
  onSearchChange,
  selectedConversationId,
  onSelectConversation,
  onNewChat,
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
    <aside className="flex w-80 flex-col border-r border-stone/20 bg-cream">
      {/* Sidebar Header */}
      <header className="border-b border-stone/10 px-6 py-5">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="font-serif text-2xl tracking-tight">Conversations</h1>
            <p className="mt-0.5 text-sm text-stone">Your recent chats</p>
          </div>
          <button
            type="button"
            onClick={onNewChat}
            className="group flex items-center gap-2 rounded-full bg-ink px-4 py-2 text-sm font-medium text-cream transition-all hover:bg-ink/90 hover:shadow-lg hover:shadow-ink/10"
          >
            <Plus className="h-4 w-4 transition-transform group-hover:rotate-90" />
            New
          </button>
        </div>
      </header>

      {/* Search */}
      <div className="px-5 py-4">
        <div className="relative">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-stone" />
          <input
            type="search"
            placeholder="Search conversations..."
            value={search}
            onChange={(event) => onSearchChange(event.target.value)}
            className="w-full rounded-xl border border-transparent bg-mist py-2.5 pl-10 pr-4 text-sm placeholder:text-stone/60 transition-colors focus:border-stone/20 focus:outline-none"
          />
        </div>
      </div>

      {statusMessage && (
        <div className="px-5 py-2 text-xs text-terracotta">{statusMessage}</div>
      )}

      {/* Conversation List */}
      <nav className="flex-1 overflow-y-auto px-3 pb-4">
        {filteredConversations.length === 0 && (
          <p className="px-4 text-sm text-stone">No conversations yet.</p>
        )}

        <div className="space-y-1">
          {filteredConversations.map((conversation, index) => {
            const isSelected = conversation.id === selectedConversationId;
            const isResumable = conversation.id ? resumableConversationIds.has(conversation.id) : false;
            const animationDelay = `${index * 0.05}s`;

            return (
              <div
                key={conversation.id}
                className="animate-fade-in"
                style={{ animationDelay }}
              >
                <button
                  type="button"
                  onClick={() => onSelectConversation(conversation)}
                  className={`w-full rounded-xl px-4 py-3.5 text-left transition-all ${
                    isSelected
                      ? "border border-stone/10 bg-mist"
                      : "border border-transparent hover:bg-mist/60"
                  }`}
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0 flex-1">
                      <h3 className="truncate font-medium text-ink">
                        {conversation.title || "Untitled conversation"}
                      </h3>
                      {conversation.lastMessagePreview && (
                        <p className="mt-1 line-clamp-2 text-sm text-stone">
                          {conversation.lastMessagePreview}
                        </p>
                      )}
                    </div>
                    {isResumable ? (
                      <div className="spinner mt-0.5 flex-shrink-0" />
                    ) : (
                      <span className="mt-0.5 flex-shrink-0 whitespace-nowrap text-xs text-stone">
                        {formatRelativeTime(conversation.updatedAt || conversation.createdAt)}
                      </span>
                    )}
                  </div>
                </button>
              </div>
            );
          })}
        </div>
      </nav>
    </aside>
  );
}
