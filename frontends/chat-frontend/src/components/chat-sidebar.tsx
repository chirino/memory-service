import type { ConversationSummary } from "@/client";
import { Archive, ArchiveRestore, Plus, Search } from "lucide-react";

type ChatSidebarProps = {
  conversations: ConversationSummary[];
  archiveFilter: "exclude" | "include" | "only";
  selectedConversationId: string | null;
  onSelectConversation: (conversation: ConversationSummary) => void;
  onSelectConversationId: (conversationId: string) => void;
  onArchiveFilterChange: (value: "exclude" | "include" | "only") => void;
  onNewChat: () => void;
  onOpenSearch: () => void;
  statusMessage?: string | null;
  resumableConversationIds?: Set<string>;
  childrenByParentId?: Map<string, ConversationSummary[]>;
  selectedRootConversationId?: string | null;
  selectedConversationLineage?: Set<string>;
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
  archiveFilter,
  selectedConversationId,
  onSelectConversation,
  onSelectConversationId,
  onArchiveFilterChange,
  onNewChat,
  onOpenSearch,
  statusMessage,
  resumableConversationIds = new Set(),
  childrenByParentId = new Map(),
  selectedRootConversationId = null,
  selectedConversationLineage = new Set(),
}: ChatSidebarProps) {
  const showingArchived = archiveFilter === "only";

  const renderConversationItem = (conversation: ConversationSummary, depth = 0, animationDelay?: string) => {
    const conversationId = conversation.id ?? null;
    const isSelected = conversationId === selectedConversationId;
    const isResumable = conversationId ? resumableConversationIds.has(conversationId) : false;
    const isExpanded = conversationId
      ? conversationId === selectedRootConversationId || selectedConversationLineage.has(conversationId)
      : false;
    const children = conversationId ? (childrenByParentId.get(conversationId) ?? []) : [];

    return (
      <div
        key={conversationId ?? `${depth}-${conversation.createdAt ?? conversation.title ?? "conversation"}`}
        className={depth === 0 ? "animate-fade-in" : undefined}
        style={depth === 0 && animationDelay ? { animationDelay } : undefined}
      >
        <button
          type="button"
          onClick={() => {
            if (conversationId) {
              onSelectConversationId(conversationId);
              return;
            }
            onSelectConversation(conversation);
          }}
          className={`w-full rounded-xl px-4 py-3.5 text-left transition-all ${
            isSelected ? "border border-stone/10 bg-mist" : "border border-transparent hover:bg-mist/60"
          }`}
          style={depth > 0 ? { paddingLeft: `${1 + depth * 1.25}rem` } : undefined}
        >
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0 flex-1">
              <h3 className={`truncate ${depth > 0 ? "text-sm" : "font-medium"} text-ink`}>
                {conversation.title || "Untitled conversation"}
              </h3>
              {depth === 0 && conversation.lastMessagePreview && (
                <p className="mt-1 line-clamp-2 text-sm text-stone">{conversation.lastMessagePreview}</p>
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

        {isExpanded && children.length > 0 && (
          <div className="mt-1 space-y-1">{children.map((child) => renderConversationItem(child, depth + 1))}</div>
        )}
      </div>
    );
  };

  return (
    <aside className="flex w-80 flex-col border-r border-stone/20 bg-cream">
      {/* Sidebar Header */}
      <header className="border-b border-stone/10 px-6 py-5">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="font-serif text-2xl tracking-tight">{showingArchived ? "Archived" : "Conversations"}</h1>
            <p className="mt-0.5 text-sm text-stone">
              {showingArchived ? "Previously archived chats" : "Your recent chats"}
            </p>
          </div>
          {!showingArchived && (
            <button
              type="button"
              onClick={onNewChat}
              className="group flex items-center gap-2 rounded-full bg-ink px-4 py-2 text-sm font-medium text-cream transition-all hover:bg-ink/90 hover:shadow-lg hover:shadow-ink/10"
            >
              <Plus className="h-4 w-4 transition-transform group-hover:rotate-90" />
              New
            </button>
          )}
        </div>
      </header>

      {/* Search Button */}
      <div className="px-5 py-4">
        <button
          type="button"
          onClick={onOpenSearch}
          className="flex w-full items-center gap-3 rounded-xl border border-transparent bg-mist py-2.5 pl-3 pr-4 text-left text-sm text-stone/60 transition-colors hover:border-stone/20 hover:bg-mist/80"
        >
          <Search className="h-4 w-4 text-stone" />
          <span>Search conversations...</span>
        </button>
      </div>

      {statusMessage && <div className="px-5 py-2 text-xs text-terracotta">{statusMessage}</div>}

      {/* Conversation List */}
      <nav className="flex-1 overflow-y-auto px-3 pb-4">
        {conversations.length === 0 && (
          <p className="px-4 text-sm text-stone">
            {showingArchived ? "No archived conversations." : "No conversations yet."}
          </p>
        )}

        <div className="space-y-1">
          {conversations.map((conversation, index) => {
            const animationDelay = `${index * 0.05}s`;
            return renderConversationItem(conversation, 0, animationDelay);
          })}
        </div>
      </nav>

      {/* Archive toggle at bottom */}
      <div className="border-t border-stone/10 px-5 py-3">
        <button
          type="button"
          onClick={() => onArchiveFilterChange(showingArchived ? "exclude" : "only")}
          className="flex w-full items-center justify-center gap-2 rounded-lg py-1.5 text-xs text-stone transition-colors hover:text-ink"
        >
          {showingArchived ? (
            <>
              <ArchiveRestore className="h-3.5 w-3.5" />
              Back to conversations
            </>
          ) : (
            <>
              <Archive className="h-3.5 w-3.5" />
              View archived
            </>
          )}
        </button>
      </div>
    </aside>
  );
}
