import { createFileRoute, Link } from "@tanstack/react-router";
import { useState, useEffect } from "react";
import { Search as SearchIcon, MessageSquare, Database } from "lucide-react";
import { type AdminConversation, type AdminMemoryItem } from "@/api/client";
import { useAdminConversations, useAdminMemories } from "@/hooks/useAdminApi";
import { formatRelativeTime, truncate, cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";

export const Route = createFileRoute("/search")({
  component: SearchPage,
});

type SearchType = "conversations" | "memories" | "all";

function SearchPage() {
  const [query, setQuery] = useState("");
  const [searchType, setSearchType] = useState<SearchType>("all");
  const [debouncedQuery, setDebouncedQuery] = useState("");

  // Debounce search query
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedQuery(query);
    }, 300);
    return () => clearTimeout(timer);
  }, [query]);

  const shouldSearchConversations = searchType === "all" || searchType === "conversations";
  const shouldSearchMemories = searchType === "all" || searchType === "memories";

  const {
    data: conversationsData,
    isLoading: conversationsLoading,
  } = useAdminConversations({ archived: "exclude" });

  const {
    data: memoriesData,
    isLoading: memoriesLoading,
  } = useAdminMemories({});

  // Filter results based on query
  const filteredConversations = debouncedQuery && conversationsData?.data
    ? conversationsData.data.filter((conv: AdminConversation) => {
        const searchLower = debouncedQuery.toLowerCase();
        return (
          conv.title?.toLowerCase().includes(searchLower) ||
          conv.id?.toLowerCase().includes(searchLower) ||
          conv.ownerUserId?.toLowerCase().includes(searchLower)
        );
      })
    : [];

  const filteredMemories = debouncedQuery && memoriesData?.items
    ? memoriesData.items.filter((memory: AdminMemoryItem) => {
        const searchLower = debouncedQuery.toLowerCase();
        return (
          memory.key?.toLowerCase().includes(searchLower) ||
          memory.namespace?.join("/").toLowerCase().includes(searchLower) ||
          JSON.stringify(memory.value).toLowerCase().includes(searchLower)
        );
      })
    : [];

  const isLoading = conversationsLoading || memoriesLoading;
  const hasResults = filteredConversations.length > 0 || filteredMemories.length > 0;

  return (
    <div className="flex h-full flex-col">
      <div className="px-5 pb-5 pt-8 md:px-10 md:pt-10">
        <h1 className="console-title text-4xl leading-tight text-foreground md:text-5xl">Search</h1>
        <p className="console-subtitle mt-3 text-base md:text-lg">Search across conversations and memories</p>
      </div>

      <div className="px-5 pb-6 md:px-10">
        <div className="space-y-4">
          <div className="relative">
            <SearchIcon className="absolute left-3 top-1/2 h-5 w-5 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              placeholder="Search by title, ID, namespace, key, or content..."
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="console-input w-full py-3 pl-10 pr-4 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary"
            />
          </div>

          <div className="flex items-center gap-2">
            <div className="console-segmented">
              <button
                onClick={() => setSearchType("all")}
                className={cn("console-segment", searchType === "all" && "console-segment-active")}
              >
                All
              </button>
              <button
                onClick={() => setSearchType("conversations")}
                className={cn("console-segment", searchType === "conversations" && "console-segment-active")}
              >
                Conversations
              </button>
              <button
                onClick={() => setSearchType("memories")}
                className={cn("console-segment", searchType === "memories" && "console-segment-active")}
              >
                Memories
              </button>
            </div>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-5 pb-8 md:px-10">
        {!query && (
          <div className="flex h-full items-center justify-center">
            <div className="text-center">
              <SearchIcon className="mx-auto mb-4 h-12 w-12 text-primary/40" strokeWidth={1.45} />
              <p className="text-muted-foreground">Enter a search query to get started</p>
            </div>
          </div>
        )}

        {query && isLoading && (
          <div className="flex h-full items-center justify-center">
            <div className="text-center">
              <div className="mb-4 inline-block h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
              <p className="text-sm text-muted-foreground">Searching...</p>
            </div>
          </div>
        )}

        {query && !isLoading && !hasResults && (
          <div className="flex h-full items-center justify-center">
            <div className="text-center">
              <p className="text-muted-foreground">No results found for "{query}"</p>
            </div>
          </div>
        )}

        {query && !isLoading && hasResults && (
          <div className="space-y-8">
            {/* Conversations Results */}
            {shouldSearchConversations && filteredConversations.length > 0 && (
              <div>
                <div className="mb-4 flex items-center gap-2">
                  <MessageSquare className="h-5 w-5 text-muted-foreground" />
                  <h2 className="text-lg font-semibold text-foreground">
                    Conversations ({filteredConversations.length})
                  </h2>
                </div>
                <div className="space-y-3">
                  {filteredConversations.map((conversation) => (
                    <Link
                      key={conversation.id}
                      to="/conversations/$conversationId"
                      params={{ conversationId: conversation.id! }}
                      className="console-panel block rounded-xl p-4 transition-colors hover:bg-sage-soft/25"
                    >
                      <div className="mb-2 flex items-start justify-between">
                        <h3 className="font-medium text-foreground">
                          {conversation.title || "Untitled Conversation"}
                        </h3>
                        {conversation.archived && <Badge variant="secondary">Archived</Badge>}
                      </div>
                      <div className="space-y-1 text-sm text-muted-foreground">
                        <p>ID: {conversation.id}</p>
                        {conversation.ownerUserId && <p>User: {conversation.ownerUserId}</p>}
                        <p>Created {formatRelativeTime(conversation.createdAt)}</p>
                      </div>
                    </Link>
                  ))}
                </div>
              </div>
            )}

            {/* Memories Results */}
            {shouldSearchMemories && filteredMemories.length > 0 && (
              <div>
                <div className="mb-4 flex items-center gap-2">
                  <Database className="h-5 w-5 text-muted-foreground" />
                  <h2 className="text-lg font-semibold text-foreground">
                    Memories ({filteredMemories.length})
                  </h2>
                </div>
                <div className="space-y-3">
                  {filteredMemories.map((memory) => (
                    <Link
                      key={memory.id}
                      to="/memories/$memoryId"
                      params={{ memoryId: memory.id! }}
                      className="console-panel block rounded-xl p-4 transition-colors hover:bg-sage-soft/25"
                    >
                      <div className="mb-2 flex items-start justify-between">
                        <div>
                          <p className="mb-1 font-mono text-xs text-muted-foreground">
                            {memory.namespace?.join(" / ") || ""}
                          </p>
                          <h3 className="font-medium text-foreground">{memory.key}</h3>
                        </div>
                        {memory.archived && <Badge variant="secondary">Archived</Badge>}
                      </div>
                      <div className="space-y-1 text-sm text-muted-foreground">
                        <p className="font-mono">{truncate(JSON.stringify(memory.value), 100)}</p>
                        <p>Created {formatRelativeTime(memory.createdAt)}</p>
                      </div>
                    </Link>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// Made with Bob
