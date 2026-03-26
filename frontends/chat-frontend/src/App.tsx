import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ChildConversationSummary, Conversation, ConversationSummary } from "@/client";
import { ApiError, ConversationsService, OpenAPI } from "@/client";
import { ChatPanel } from "@/components/chat-panel";
import { ChatSidebar } from "@/components/chat-sidebar";
import { SearchModal } from "@/components/search-modal";
import { PendingTransfersPanel } from "@/components/sharing";
import { useResumeCheck } from "@/hooks/useResumeCheck";
import { useEventStream, useStreamingConversations } from "@/hooks/useEventStream";
import { useAuth } from "@/lib/auth";

type ListUserConversationsResponse = {
  data?: ConversationSummary[];
  afterCursor?: string | null;
};

function generateConversationId() {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  // Fallback for insecure contexts (plain HTTP) where crypto.randomUUID() is unavailable
  return "10000000-1000-4000-8000-100000000000".replace(/[018]/g, (c) =>
    (+c ^ (crypto.getRandomValues(new Uint8Array(1))[0] & (15 >> (+c / 4)))).toString(16),
  );
}

function getConversationIdFromUrl(): string | null {
  if (typeof window === "undefined") {
    return null;
  }
  const params = new URLSearchParams(window.location.search);
  const id = params.get("conversationId");
  return id && id.trim().length > 0 ? id : null;
}

function updateConversationInUrl(conversationId: string | null, replace = false) {
  if (typeof window === "undefined") {
    return;
  }
  const url = new URL(window.location.href);
  if (conversationId) {
    url.searchParams.set("conversationId", conversationId);
  } else {
    url.searchParams.delete("conversationId");
  }
  const next = url.toString();
  if (next === window.location.href) {
    return;
  }
  if (replace) {
    window.history.replaceState(null, "", next);
  } else {
    window.history.pushState(null, "", next);
  }
}

type SidebarErrorFallbackProps = {
  onRetry: () => void;
};

function SidebarErrorFallback({ onRetry }: SidebarErrorFallbackProps) {
  return (
    <aside className="flex w-80 flex-col border-r bg-background">
      <div className="flex items-center justify-between border-b px-4 py-3">
        <div>
          <h1 className="text-base font-semibold">Conversations</h1>
          <p className="text-xs text-muted-foreground">Browse and resume chats.</p>
        </div>
      </div>
      <div className="flex-1 px-4 py-6 text-sm text-destructive">Failed to load conversations. Please try again.</div>
      <div className="flex flex-wrap justify-end gap-2 border-t px-4 py-3">
        <Button size="sm" onClick={onRetry}>
          Retry
        </Button>
      </div>
    </aside>
  );
}

function ChatSidebarLoading() {
  return (
    <aside className="flex w-80 flex-col border-r bg-background">
      <div className="border-b px-4 py-3">
        <div className="h-5 w-32 rounded-full bg-muted/60" />
        <div className="mt-1 h-3 w-24 rounded-full bg-muted/50" />
      </div>
      <div className="border-b p-3">
        <div className="h-9 rounded-md bg-muted/20" />
      </div>
      <div className="flex-1 space-y-2 overflow-hidden p-2">
        {[...Array(4)].map((_, index) => (
          <div key={index} className="h-24 rounded-md bg-muted/20" />
        ))}
      </div>
    </aside>
  );
}

function App() {
  const [statusMessage, setStatusMessage] = useState<string | null>(null);
  const [isSearchOpen, setIsSearchOpen] = useState(false);
  const [archiveFilter, setArchiveFilter] = useState<"exclude" | "include" | "only">("exclude");
  const [selectedConversationId, setSelectedConversationId] = useState<string | null>(null);
  const pendingUrlLookupRef = useRef<string | null>(null);
  const [resolvedConversationIds, setResolvedConversationIds] = useState<Set<string>>(new Set());
  const [sidebarChildrenByParentId, setSidebarChildrenByParentId] = useState<Map<string, ConversationSummary[]>>(
    new Map(),
  );
  const [sidebarParentByConversationId, setSidebarParentByConversationId] = useState<Map<string, string>>(new Map());
  const queryClient = useQueryClient();

  // Subscribe to server-sent events for live cache invalidation
  useEventStream();
  const streamingConversations = useStreamingConversations();

  const conversationsQuery = useQuery<ConversationSummary[], ApiError, ConversationSummary[]>({
    queryKey: ["conversations", archiveFilter],
    queryFn: async (): Promise<ConversationSummary[]> => {
      const response = (await ConversationsService.listConversations({
        archived: archiveFilter,
        limit: 20,
        mode: "roots",
      })) as unknown as ListUserConversationsResponse;
      return Array.isArray(response.data) ? response.data : [];
    },
  });
  // Extract conversation IDs for resume check
  const conversations = conversationsQuery.data ?? [];
  const conversationIds = conversations.map((conv) => conv.id).filter((id): id is string => !!id);
  const resumeCheckQuery = useResumeCheck(conversationIds);
  const resumableConversationIds = new Set([...(resumeCheckQuery.data ?? []), ...streamingConversations]);

  // Get current user info from auth context (frontend OIDC)
  const auth = useAuth();
  const currentUser = auth.user;
  const currentUserId = currentUser?.userId ?? null;
  const isResolvedSelectedConversation = Boolean(
    selectedConversationId && resolvedConversationIds.has(selectedConversationId),
  );

  const selectedConversationQuery = useQuery<Conversation, ApiError>({
    queryKey: ["conversation", selectedConversationId],
    enabled: isResolvedSelectedConversation,
    queryFn: async (): Promise<Conversation> => {
      return (await ConversationsService.getConversation({
        conversationId: selectedConversationId!,
      })) as Conversation;
    },
  });

  const selectedConversationChildrenQuery = useQuery<ChildConversationSummary[], ApiError, ChildConversationSummary[]>({
    queryKey: ["conversation-sidebar-children", selectedConversationId],
    enabled: isResolvedSelectedConversation,
    queryFn: async (): Promise<ChildConversationSummary[]> => {
      const response = (await ConversationsService.listConversationChildren({
        conversationId: selectedConversationId!,
        limit: 200,
      })) as unknown as ChildConversationSummary[] | { data?: ChildConversationSummary[] };
      return Array.isArray(response) ? response : Array.isArray(response.data) ? response.data : [];
    },
  });

  useEffect(() => {
    if (!selectedConversationId || !selectedConversationChildrenQuery.data) {
      return;
    }

    const children = selectedConversationChildrenQuery.data.map((child) => ({
      id: child.id,
      title: child.title ?? null,
      ownerUserId: child.ownerUserId,
      createdAt: child.createdAt,
      updatedAt: child.updatedAt,
      lastMessagePreview: child.lastMessagePreview ?? null,
      accessLevel: child.accessLevel,
    }));

    setSidebarChildrenByParentId((prev) => {
      const next = new Map(prev);
      next.set(selectedConversationId, children);
      return next;
    });
  }, [selectedConversationChildrenQuery.data, selectedConversationId]);

  useEffect(() => {
    const conversation = selectedConversationQuery.data;
    if (!conversation?.id || !conversation.startedByConversationId) {
      return;
    }
    const conversationId = conversation.id;
    const parentConversationId = conversation.startedByConversationId;

    setSidebarParentByConversationId((prev) => {
      if (prev.get(conversationId) === parentConversationId) {
        return prev;
      }
      const next = new Map(prev);
      next.set(conversationId, parentConversationId);
      return next;
    });
  }, [selectedConversationQuery.data]);

  const { selectedRootConversationId, selectedConversationLineage } = useMemo(() => {
    const selectedId = selectedConversationId ?? null;
    const lineage = new Set<string>();
    let rootId = selectedId;
    let cursorId = selectedId;

    while (cursorId) {
      lineage.add(cursorId);
      const parentId = sidebarParentByConversationId.get(cursorId) ?? null;
      if (!parentId) {
        break;
      }
      rootId = parentId;
      cursorId = parentId;
    }

    return {
      selectedRootConversationId: rootId,
      selectedConversationLineage: lineage,
    };
  }, [selectedConversationId, sidebarParentByConversationId]);

  useEffect(() => {
    const interceptor = (response: Response) => {
      if (response.status === 401) {
        // Show auth error screen instead of auto-redirecting to prevent redirect loops
        // (e.g., Keycloak sees user logged in → redirects back → backend still rejects token → 401 again)
        auth.setAuthError(
          "The server returned 401 Unauthorized. Your token may be invalid or the server configuration may not match.",
        );
      }
      return response;
    };
    OpenAPI.interceptors.response.use(interceptor);
    return () => {
      OpenAPI.interceptors.response.eject(interceptor);
    };
  }, [auth]);

  const markResolvedConversation = useCallback((conversationId: string) => {
    setResolvedConversationIds((prev) => {
      if (prev.has(conversationId)) {
        return prev;
      }
      const next = new Set(prev);
      next.add(conversationId);
      return next;
    });
  }, []);

  // Clear status message when conversations load successfully
  // Note: This effect syncs UI state (status message) with external data (React Query)
  useEffect(() => {
    if (conversationsQuery.data && statusMessage !== null) {
      setStatusMessage(null);
    }
  }, [conversationsQuery.data, statusMessage]);

  useEffect(() => {
    if (conversationsQuery.data === undefined) {
      return;
    }

    const availableIds = new Set(
      (conversationsQuery.data ?? []).map((conversation) => conversation.id).filter((id): id is string => !!id),
    );
    if (availableIds.size > 0) {
      setResolvedConversationIds((prev) => {
        const next = new Set(prev);
        availableIds.forEach((id) => next.add(id));
        return next;
      });
    }

    if (selectedConversationId) {
      if (availableIds.has(selectedConversationId)) {
        return;
      }
      return;
    }

    const urlConversationId = getConversationIdFromUrl();
    if (urlConversationId && !availableIds.has(urlConversationId)) {
      if (pendingUrlLookupRef.current !== urlConversationId) {
        pendingUrlLookupRef.current = urlConversationId;
        void (async () => {
          try {
            await ConversationsService.getConversation({ conversationId: urlConversationId });
            setSelectedConversationId(urlConversationId);
            updateConversationInUrl(urlConversationId, true);
            markResolvedConversation(urlConversationId);
          } catch {
            const candidate = (conversationsQuery.data ?? []).find((conversation) => conversation.id)?.id ?? null;
            const nextSelected = candidate ?? generateConversationId();
            setSelectedConversationId(nextSelected);
            updateConversationInUrl(nextSelected, true);
          } finally {
            pendingUrlLookupRef.current = null;
          }
        })();
      }
      return;
    }
    const candidate =
      urlConversationId && availableIds.has(urlConversationId)
        ? urlConversationId
        : ((conversationsQuery.data ?? []).find((conversation) => conversation.id)?.id ?? null);
    const nextSelected = candidate ?? generateConversationId();

    if (nextSelected !== selectedConversationId) {
      // Note: This effect syncs selected conversation with URL and available conversations
      // We need setState here to initialize selection from URL/available conversations
      setSelectedConversationId(nextSelected);
      updateConversationInUrl(nextSelected, true);
    }
  }, [selectedConversationId, conversationsQuery.data, markResolvedConversation]);

  const archiveConversationMutation = useMutation({
    mutationFn: async (conversationId: string) => {
      await ConversationsService.updateConversation({
        conversationId,
        requestBody: {
          archived: true,
        },
      });
    },
    onSuccess: (_, conversationId) => {
      if (conversationId === selectedConversationId) {
        const nextId = generateConversationId();
        setSelectedConversationId(nextId);
        updateConversationInUrl(nextId, true);
      }
      setResolvedConversationIds((prev) => {
        if (!prev.has(conversationId)) {
          return prev;
        }
        const next = new Set(prev);
        next.delete(conversationId);
        return next;
      });
      void queryClient.invalidateQueries({ queryKey: ["conversations"] });
      void queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
      void queryClient.removeQueries({ queryKey: ["conversation", conversationId] });
      void queryClient.removeQueries({ queryKey: ["conversation-forks", conversationId] });
      void queryClient.removeQueries({ queryKey: ["conversation-path-messages", conversationId] });
      void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
      setStatusMessage(null);
    },
    onError: () => {
      setStatusMessage("Failed to archive conversation. Please try again.");
    },
  });

  const unarchiveConversationMutation = useMutation({
    mutationFn: async (conversationId: string) => {
      await ConversationsService.updateConversation({
        conversationId,
        requestBody: {
          archived: false,
        },
      });
    },
    onSuccess: (_, conversationId) => {
      void queryClient.invalidateQueries({ queryKey: ["conversations"] });
      void queryClient.invalidateQueries({ queryKey: ["conversation", conversationId] });
      setStatusMessage(null);
    },
    onError: () => {
      setStatusMessage("Failed to unarchive conversation. Please try again.");
    },
  });

  const handleNewChat = () => {
    setStatusMessage(null);
    const newId = generateConversationId();
    setSelectedConversationId(newId);
    updateConversationInUrl(newId);
  };

  const handleSelectConversation = (conversation: ConversationSummary) => {
    setStatusMessage(null);
    const id = conversation.id ?? null;
    setSelectedConversationId(id);
    updateConversationInUrl(id);
    if (id) {
      markResolvedConversation(id);
    }
    void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
  };

  const handleSelectConversationId = useCallback(
    (conversationId: string) => {
      setStatusMessage(null);
      setSelectedConversationId(conversationId);
      updateConversationInUrl(conversationId);
      markResolvedConversation(conversationId);
    },
    [markResolvedConversation],
  );

  const handleArchiveConversationById = useCallback(
    (conversationId: string) => {
      setStatusMessage(null);
      archiveConversationMutation.mutate(conversationId);
    },
    [archiveConversationMutation],
  );

  const handleUnarchiveConversationById = useCallback(
    (conversationId: string) => {
      setStatusMessage(null);
      unarchiveConversationMutation.mutate(conversationId);
    },
    [unarchiveConversationMutation],
  );

  useEffect(() => {
    const handlePopState = () => {
      let fromUrl = getConversationIdFromUrl();
      if (!fromUrl) {
        fromUrl = generateConversationId();
        updateConversationInUrl(fromUrl, true);
      }
      setSelectedConversationId(fromUrl);
    };
    if (typeof window !== "undefined") {
      window.addEventListener("popstate", handlePopState);
    }
    return () => {
      if (typeof window !== "undefined") {
        window.removeEventListener("popstate", handlePopState);
      }
    };
  }, []);

  const sidebarContent = conversationsQuery.isError ? (
    <SidebarErrorFallback
      onRetry={() => {
        void conversationsQuery.refetch();
      }}
    />
  ) : conversationsQuery.isLoading ? (
    <ChatSidebarLoading />
  ) : (
    <ChatSidebar
      conversations={conversations}
      archiveFilter={archiveFilter}
      selectedConversationId={selectedConversationId}
      onSelectConversation={handleSelectConversation}
      onSelectConversationId={handleSelectConversationId}
      onArchiveFilterChange={setArchiveFilter}
      onNewChat={handleNewChat}
      onOpenSearch={() => setIsSearchOpen(true)}
      statusMessage={statusMessage}
      resumableConversationIds={resumableConversationIds}
      childrenByParentId={sidebarChildrenByParentId}
      selectedRootConversationId={selectedRootConversationId}
      selectedConversationLineage={selectedConversationLineage}
    />
  );

  return (
    <div className="flex h-screen">
      {sidebarContent}
      <div className="flex flex-1 flex-col">
        <PendingTransfersPanel onNavigateToConversation={handleSelectConversationId} />
        <ChatPanel
          conversationId={selectedConversationId}
          onSelectConversationId={handleSelectConversationId}
          knownConversationIds={resolvedConversationIds}
          resumableConversationIds={resumableConversationIds}
          onArchiveConversation={handleArchiveConversationById}
          onUnarchiveConversation={handleUnarchiveConversationById}
          currentUserId={currentUserId}
          currentUser={currentUser}
        />
      </div>
      <SearchModal
        isOpen={isSearchOpen}
        onClose={() => setIsSearchOpen(false)}
        onSelectConversation={handleSelectConversationId}
      />
    </div>
  );
}

export default App;
