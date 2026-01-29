import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ConversationSummary } from "@/client";
import { ApiError, ConversationsService, OpenAPI } from "@/client";
import { ChatPanel } from "@/components/chat-panel";
import { ChatSidebar } from "@/components/chat-sidebar";
import { useResumeCheck } from "@/hooks/useResumeCheck";

type ListUserConversationsResponse = {
  data?: ConversationSummary[];
  nextCursor?: string | null;
};

function generateConversationId() {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `session-${Date.now()}`;
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
  const [search, setSearch] = useState("");
  const [selectedConversationId, setSelectedConversationId] = useState<string | null>(null);
  const pendingUrlLookupRef = useRef<string | null>(null);
  const [resolvedConversationIds, setResolvedConversationIds] = useState<Set<string>>(new Set());
  const queryClient = useQueryClient();

  const conversationsQuery = useQuery<ConversationSummary[], ApiError, ConversationSummary[]>({
    queryKey: ["conversations"],
    queryFn: async (): Promise<ConversationSummary[]> => {
      const response = (await ConversationsService.listConversations({
        limit: 20,
        mode: "latest-fork",
      })) as unknown as ListUserConversationsResponse;
      const data = Array.isArray(response.data) ? response.data : [];
      return [...data].sort((a, b) => {
        const aTime = new Date(a.updatedAt || a.createdAt || "").getTime();
        const bTime = new Date(b.updatedAt || b.createdAt || "").getTime();
        return bTime - aTime;
      });
    },
  });
  // Extract conversation IDs for resume check
  const conversations = conversationsQuery.data ?? [];
  const conversationIds = conversations.map((conv) => conv.id).filter((id): id is string => !!id);
  const resumeCheckQuery = useResumeCheck(conversationIds);
  const resumableConversationIds = new Set(resumeCheckQuery.data ?? []);

  useEffect(() => {
    const interceptor = (response: Response) => {
      if (response.status === 401) {
        window.location.reload();
      }
      return response;
    };
    OpenAPI.interceptors.response.use(interceptor);
    return () => {
      OpenAPI.interceptors.response.eject(interceptor);
    };
  }, []);

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
      // eslint-disable-next-line react-hooks/set-state-in-effect
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
          } catch (error) {
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
      setSelectedConversationId(nextSelected); // eslint-disable-line react-hooks/set-state-in-effect
      updateConversationInUrl(nextSelected, true);
    }
  }, [selectedConversationId, conversationsQuery.data]);

  const deleteConversationMutation = useMutation({
    mutationFn: async (conversationId: string) => {
      await ConversationsService.deleteConversation({
        conversationId,
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
      // Clear status message on successful delete
      setStatusMessage(null);
    },
    onError: () => {
      // Set error status message on delete failure
      setStatusMessage("Failed to delete conversation. Please try again.");
    },
  });

  const indexConversationMutation = useMutation({
    mutationFn: async (conversationId: string) => {
      const response = await fetch(`/v1/conversations/${conversationId}/index`, {
        method: "POST",
        credentials: "include",
      });
      if (!response.ok) {
        throw new Error("Indexing failed");
      }
    },
    onSuccess: (_, conversationId) => {
      void queryClient.invalidateQueries({ queryKey: ["conversations"] });
      void queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
      setStatusMessage(null);
    },
    onError: () => {
      setStatusMessage("Failed to index conversation. Please try again.");
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

  const handleDeleteConversationById = useCallback(
    (conversationId: string) => {
      setStatusMessage(null);
      deleteConversationMutation.mutate(conversationId);
    },
    [deleteConversationMutation],
  );

  const handleIndexConversationById = useCallback(
    (conversationId: string) => {
      setStatusMessage(null);
      indexConversationMutation.mutate(conversationId);
    },
    [indexConversationMutation],
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
      search={search}
      onSearchChange={setSearch}
      selectedConversationId={selectedConversationId}
      onSelectConversation={handleSelectConversation}
      onNewChat={handleNewChat}
      statusMessage={statusMessage}
      resumableConversationIds={resumableConversationIds}
    />
  );

  return (
    <div className="flex h-screen">
      {sidebarContent}
      <ChatPanel
        conversationId={selectedConversationId}
        onSelectConversationId={handleSelectConversationId}
        knownConversationIds={resolvedConversationIds}
        resumableConversationIds={resumableConversationIds}
        onIndexConversation={handleIndexConversationById}
        onDeleteConversation={handleDeleteConversationById}
      />
    </div>
  );
}

export default App;
