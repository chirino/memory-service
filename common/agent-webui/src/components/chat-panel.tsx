import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type React from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  Conversation,
  type ConversationController,
  type ConversationMessage,
  type RenderableConversationMessage,
  useConversationInput,
  useConversationMessages,
  useConversationStreaming,
} from "@/components/conversation";
import { ConversationsUI } from "@/components/conversations-ui";
import type { ApiError, Conversation as ApiConversation, ConversationForkSummary, Message } from "@/client";
import { ConversationsService } from "@/client";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useWebSocketStream } from "@/hooks/useWebSocketStream";
import { useSseStream } from "@/hooks/useSseStream";
import type { StreamStartParams } from "@/hooks/useStreamTypes";

type ListUserMessagesResponse = {
  data?: Message[];
  nextCursor?: string | null;
};

type ListConversationForksResponse = {
  data?: ConversationForkSummary[];
};

type ForkPoint = {
  conversationId: string;
  previousMessageId: string | null;
};

type ForkOption = {
  conversationId: string;
  forkedAtConversationId?: string | null;
  forkedAtMessageId?: string | null;
  createdAt?: string | null;
  label?: string;
};

type ChatPanelProps = {
  conversationId: string | null;
  onSelectConversationId?: (conversationId: string) => void;
  resumableConversationIds?: Set<string>;
  knownConversationIds?: Set<string>;
};

type PendingFork = {
  conversationId: string;
  message: string;
};

type ConversationMeta = {
  forkedAtConversationId: string | null;
  forkedAtMessageId: string | null;
};

type StreamMode = "websocket" | "sse";

type ChatMessageRowProps = {
  message: RenderableConversationMessage;
  isEditing: boolean;
  editingText: string;
  onEditingTextChange: (value: string) => void;
  onEditStart: (message: ConversationMessage) => void;
  onEditCancel: () => void;
  onForkSend: () => void;
  composerDisabled: boolean;
  conversationId: string | null;
  forkOptionsCount: number;
  forkLabels: Record<string, string>;
  setForkLabels: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  activeForkMenuMessageId: string | null;
  setActiveForkMenuMessageId: (id: string | null) => void;
  userMessageIndexById: Map<string, number>;
  selectForkLabel: (messages: Message[], userIndex?: number) => string;
  formatForkLabel: (text: string) => string;
  formatForkTimestamp: (value?: string | null) => string;
  forkPoint: ForkPoint | null;
  forkOptions: ForkOption[];
  forkLoading: boolean;
  isForkMenuOpen: boolean;
  onForkSelect: (conversationId: string) => void;
  openForkMenuMessageId: string | null;
  setOpenForkMenuMessageId: (id: string | null) => void;
  messageRef?: React.Ref<HTMLDivElement>;
};

function ChatMessageRow({
  message,
  isEditing,
  editingText,
  onEditingTextChange,
  onEditStart,
  onEditCancel,
  onForkSend,
  composerDisabled,
  conversationId,
  forkOptionsCount,
  forkLabels,
  setForkLabels,
  activeForkMenuMessageId,
  setActiveForkMenuMessageId,
  userMessageIndexById,
  selectForkLabel,
  formatForkLabel,
  formatForkTimestamp,
  forkPoint,
  forkOptions,
  forkLoading,
  isForkMenuOpen,
  onForkSelect,
  openForkMenuMessageId,
  setOpenForkMenuMessageId,
  messageRef,
}: ChatMessageRowProps) {
  const isUser = message.author === "user";
  const hasForks = forkOptionsCount > 1;

  useEffect(() => {
    if (!isForkMenuOpen || !forkPoint) {
      return;
    }
    const missing = forkOptions.filter((entry) => entry.conversationId && !forkLabels[entry.conversationId]);
    if (!missing.length) {
      return;
    }
    let cancelled = false;
    void Promise.all(
      missing.map(async (fork) => {
        try {
          const response = (await ConversationsService.listConversationMessages({
            conversationId: fork.conversationId,
            limit: 200,
            channel: "history",
          })) as unknown as ListUserMessagesResponse;
          const messages = Array.isArray(response.data) ? response.data : [];
          const messageId = activeForkMenuMessageId ?? message.id;
          const userIndex =
            messageId && userMessageIndexById.has(messageId) ? userMessageIndexById.get(messageId) : undefined;
          const label = selectForkLabel(messages, userIndex);
          return { id: fork.conversationId, label };
        } catch (error) {
          void error;
          return null;
        }
      }),
    ).then((results) => {
      if (cancelled) {
        return;
      }
      setForkLabels((prev) => {
        const next = { ...prev };
        results.forEach((result) => {
          if (result) {
            next[result.id] = result.label;
          }
        });
        return next;
      });
    });
    return () => {
      cancelled = true;
    };
  }, [
    activeForkMenuMessageId,
    forkLabels,
    forkPoint,
    isForkMenuOpen,
    message.id,
    forkOptions,
    selectForkLabel,
    setForkLabels,
    userMessageIndexById,
  ]);

  if (isEditing) {
    return (
      <div key={message.id} className={`flex ${isUser ? "justify-end" : "justify-start"}`}>
        <div className={`relative flex max-w-[80%] flex-col gap-1 ${isUser ? "items-end" : "items-start"}`}>
          <div className="w-full rounded-lg border bg-background px-3 py-2 text-sm shadow-sm">
            <textarea
              value={editingText}
              onChange={(event) => onEditingTextChange(event.target.value)}
              rows={3}
              className="w-full resize-none rounded-md border px-3 py-2 text-sm outline-none ring-offset-background focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
            />
            <div className="mt-2 flex justify-end gap-2">
              <Button size="sm" variant="outline" onClick={onEditCancel} disabled={composerDisabled}>
                Cancel
              </Button>
              <Button size="sm" onClick={onForkSend} disabled={composerDisabled || !editingText.trim()}>
                Send
              </Button>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div key={message.id}>
      <ConversationsUI.MessageRow
        message={message}
        messageRef={messageRef}
        overlay={
          isUser && message.displayState === "stable" ? (
            <div className="absolute bottom-0 right-3 z-10 flex translate-y-[50%] opacity-0 transition-opacity group-hover:opacity-100">
              <button
                type="button"
                onClick={() => onEditStart(message)}
                disabled={composerDisabled}
                className="rounded-full border bg-background px-2 py-0.5 text-[10px] font-medium text-foreground shadow-sm disabled:opacity-50"
              >
                Edit
              </button>
            </div>
          ) : null
        }
      />
      {hasForks ? (
        <div className="relative w-full text-right">
          <button
            type="button"
            className="pointer-events-auto text-xs font-medium text-muted-foreground hover:text-foreground"
            onClick={() => {
              setActiveForkMenuMessageId(message.id);
              if (openForkMenuMessageId === message.id) {
                setOpenForkMenuMessageId(null);
                setActiveForkMenuMessageId(null);
              } else {
                setOpenForkMenuMessageId(message.id);
              }
            }}
          >
            Forks ({forkOptionsCount})
          </button>
          {isForkMenuOpen && forkPoint ? (
            <div className="absolute right-0 z-10 mt-2 w-64 rounded-md border bg-background p-2 text-xs shadow-sm">
              {forkLoading ? (
                <div className="px-2 py-2 text-sm text-muted-foreground">Loading...</div>
              ) : forkOptions.length === 0 ? null : (
                forkOptions.map((fork) => {
                  const isActive = fork.conversationId === conversationId;
                  const fallbackLabel = isActive ? message.content : "Loading fork message...";
                  const label = forkLabels[fork.conversationId] ?? fallbackLabel;
                  return (
                    <button
                      key={fork.conversationId}
                      type="button"
                      onClick={() => onForkSelect(fork.conversationId)}
                      className={`flex w-full items-center justify-between rounded-md px-2 py-2 text-left transition-colors ${
                        isActive ? "bg-muted text-foreground" : "hover:bg-muted/60"
                      }`}
                    >
                      <div className="flex flex-col">
                        <span className="text-sm font-medium">{formatForkLabel(label)}</span>
                        <span className="text-xs text-muted-foreground">{formatForkTimestamp(fork.createdAt)}</span>
                      </div>
                      {isActive ? (
                        <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-semibold text-primary">
                          Active
                        </span>
                      ) : null}
                    </button>
                  );
                })
              )}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function messageText(msg: Message): string {
  const blocks = msg.content ?? [];
  const textBlock = blocks.find((b) => {
    const block = b as { [key: string]: unknown } | undefined;
    return block && typeof block.text === "string";
  }) as { text: string } | undefined;
  return textBlock?.text ?? "";
}

function messageAuthor(msg: Message): "user" | "assistant" {
  const blocks = msg.content ?? [];
  for (const block of blocks) {
    const item = block as { [key: string]: unknown } | undefined;
    const role = item && typeof item.role === "string" ? (item.role as string).toUpperCase() : undefined;
    if (role === "USER") {
      return "user";
    }
    if (role === "AI" || role === "ASSISTANT") {
      return "assistant";
    }
  }
  return msg.userId ? "user" : "assistant";
}

type ChatPanelContentProps = {
  conversationId: string | null;
  isResolvedConversation: boolean;
  resumableConversationIds?: Set<string>;
  pendingForkRef: React.MutableRefObject<PendingFork | null>;
  hasResumedRef: React.MutableRefObject<Record<string, boolean>>;
  onSelectConversationId?: (conversationId: string) => void;
  queryClient: ReturnType<typeof useQueryClient>;
  userMessageIndexById: Map<string, number>;
  canceling: boolean;
  streamMode: StreamMode;
  setStreamMode: React.Dispatch<React.SetStateAction<StreamMode>>;
};

function ChatPanelContent({
  conversationId,
  isResolvedConversation,
  resumableConversationIds,
  pendingForkRef,
  hasResumedRef,
  onSelectConversationId,
  queryClient,
  userMessageIndexById,
  canceling,
  streamMode,
  setStreamMode,
  forksQuery,
  conversationMetaById,
}: ChatPanelContentProps & {
  forksQuery: ReturnType<typeof useQuery<ConversationForkSummary[], ApiError, ConversationForkSummary[]>>;
  conversationMetaById: Map<string, ConversationMeta>;
}) {
  const { messages } = useConversationMessages();
  const { resumeStream, isBusy } = useConversationStreaming();
  const { submit } = useConversationInput();
  const [forking, setForking] = useState(false);
  const [editingMessage, setEditingMessage] = useState<{ id: string; conversationId: string } | null>(null);
  const [editingText, setEditingText] = useState("");
  const [forkLabels, setForkLabels] = useState<Record<string, string>>({});
  const [activeForkMenuMessageId, setActiveForkMenuMessageId] = useState<string | null>(null);
  const [openForkMenuMessageId, setOpenForkMenuMessageId] = useState<string | null>(null);

  // Scroll management refs
  const viewportRef = useRef<HTMLDivElement>(null);
  const messageRefs = useRef<Map<string, HTMLDivElement>>(new Map());
  const isNearBottomRef = useRef(true);
  const shouldAutoScrollRef = useRef(true);
  const lastMessageCountRef = useRef(0);
  const isInitialLoadRef = useRef(true);

  const formatForkTimestamp = (value?: string | null) => {
    if (!value) {
      return "Unknown time";
    }
    const timestamp = new Date(value);
    if (Number.isNaN(timestamp.getTime())) {
      return "Unknown time";
    }
    return timestamp.toLocaleString();
  };

  const selectForkLabel = (messages: Message[], userIndex?: number) => {
    if (!messages.length) {
      return "Forked message";
    }
    const userMessages = messages.filter((msg) => messageAuthor(msg) === "user");
    if (!userMessages.length) {
      return "Forked message";
    }
    const candidate =
      userIndex !== undefined && userIndex >= 0 && userIndex < userMessages.length
        ? userMessages[userIndex]
        : userMessages[userMessages.length - 1];
    return messageText(candidate);
  };

  const formatForkLabel = (text: string) => {
    const trimmed = text.trim();
    if (!trimmed) {
      return "Forked message";
    }
    return trimmed.length <= 60 ? trimmed : `${trimmed.slice(0, 57)}...`;
  };

  const lastAssistantMessageId = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i -= 1) {
      if (messages[i]?.author === "assistant") {
        return messages[i]?.id ?? null;
      }
    }
    return null;
  }, [messages]);

  // Get the current conversationId from the conversation context to check if state is synced
  const { conversationId: stateConversationId } = useConversationMessages();

  useEffect(() => {
    if (!conversationId) {
      return;
    }
    if (!isResolvedConversation) {
      return;
    }
    // Wait for state to sync with prop before resuming
    if (stateConversationId !== conversationId) {
      return;
    }
    if (pendingForkRef.current?.conversationId === conversationId) {
      return;
    }
    if (isBusy || forking) {
      return;
    }
    if (hasResumedRef.current[conversationId]) {
      return;
    }

    if (resumableConversationIds?.has(conversationId)) {
      // Mark as resumed before starting to prevent duplicate attempts
      hasResumedRef.current[conversationId] = true;
      // When resuming, replace the last assistant message to avoid duplicates.
      // State is now synced, so we can use the default (state.conversationId)
      resumeStream({ replaceMessageId: lastAssistantMessageId });
    }
  }, [
    conversationId,
    stateConversationId,
    forking,
    hasResumedRef,
    isBusy,
    isResolvedConversation,
    lastAssistantMessageId,
    pendingForkRef,
    resumableConversationIds,
    resumeStream,
  ]);

  useEffect(() => {
    const pending = pendingForkRef.current;
    if (!conversationId || !pending || pending.conversationId !== conversationId) {
      return;
    }
    // Wait for state to sync with prop before submitting to ensure correct conversation ID
    if (stateConversationId !== conversationId) {
      return;
    }
    if (isBusy || forking) {
      return;
    }
    const trimmed = pending.message.trim();
    pendingForkRef.current = null;
    if (!trimmed) {
      return;
    }
    submit(trimmed);
  }, [conversationId, stateConversationId, forking, isBusy, pendingForkRef, submit]);

  useEffect(() => {
    setEditingMessage(null);
    setEditingText("");
    setForkLabels({});
    setActiveForkMenuMessageId(null);
    setOpenForkMenuMessageId(null);
  }, [conversationId]);

  // Compute fork point and options for the currently open fork menu
  const openForkMenuMessage = useMemo(() => {
    if (!openForkMenuMessageId) {
      return null;
    }
    return messages.find((msg) => msg.id === openForkMenuMessageId) ?? null;
  }, [messages, openForkMenuMessageId]);

  const forkPoint = useMemo<ForkPoint | null>(() => {
    if (!openForkMenuMessage) {
      return null;
    }
    // If this is the first message of a forked conversation, use the fork origin
    if (openForkMenuMessage.previousMessageId === null && openForkMenuMessage.forkedFrom?.conversationId) {
      return {
        conversationId: openForkMenuMessage.forkedFrom.conversationId,
        previousMessageId: openForkMenuMessage.forkedFrom.messageId ?? null,
      };
    }
    return {
      conversationId: openForkMenuMessage.conversationId,
      previousMessageId: openForkMenuMessage.previousMessageId,
    };
  }, [openForkMenuMessage]);

  const forkPointKey = useCallback((conversationKey: string | null | undefined, previousMessageId: string | null) => {
    if (!conversationKey) {
      return null;
    }
    return `${conversationKey}:${previousMessageId ?? "__start__"}`;
  }, []);

  const forksByPointKey = useMemo(() => {
    const grouped = new Map<string, ConversationForkSummary[]>();
    (forksQuery.data ?? []).forEach((fork) => {
      if (!fork.forkedAtConversationId || !fork.conversationId) {
        return;
      }
      const key = forkPointKey(fork.forkedAtConversationId, fork.forkedAtMessageId ?? null);
      if (!key) {
        return;
      }
      const list = grouped.get(key) ?? [];
      list.push(fork);
      grouped.set(key, list);
    });
    return grouped;
  }, [forkPointKey, forksQuery.data]);

  const currentMeta = conversationId ? conversationMetaById.get(conversationId) : null;
  const currentForkPointKey = useMemo(() => {
    if (!currentMeta?.forkedAtConversationId) {
      return null;
    }
    return forkPointKey(currentMeta.forkedAtConversationId, currentMeta.forkedAtMessageId ?? null);
  }, [currentMeta, forkPointKey]);

  const getForkOptionsForPoint = useCallback(
    (forkKey: string | null) => {
      if (!forkKey) {
        return [];
      }
      const forksAtPoint = forksByPointKey.get(forkKey) ?? [];
      const showParentEntry = Boolean(currentForkPointKey && forkKey === currentForkPointKey);

      const options: ForkOption[] = forksAtPoint.map((fork) => ({
        conversationId: fork.conversationId ?? "",
        forkedAtConversationId: fork.forkedAtConversationId ?? null,
        forkedAtMessageId: fork.forkedAtMessageId ?? null,
        createdAt: fork.createdAt ?? null,
        label: fork.title ?? undefined,
      }));

      if (showParentEntry && currentMeta?.forkedAtConversationId) {
        options.push({
          conversationId: currentMeta.forkedAtConversationId,
          forkedAtConversationId: null,
          forkedAtMessageId: currentMeta.forkedAtMessageId ?? null,
        });
      }

      if (conversationId && (forksAtPoint.length > 0 || showParentEntry)) {
        options.push({
          conversationId,
          forkedAtConversationId: currentMeta?.forkedAtConversationId ?? null,
          forkedAtMessageId: currentMeta?.forkedAtMessageId ?? null,
        });
      }

      const seen = new Set<string>();
      return options.filter((opt) => {
        if (!opt.conversationId || seen.has(opt.conversationId)) {
          return false;
        }
        seen.add(opt.conversationId);
        return true;
      });
    },
    [conversationId, currentForkPointKey, currentMeta, forksByPointKey],
  );

  const forkOptions = useMemo<ForkOption[]>(() => {
    if (!forkPoint) {
      return [];
    }
    const key = forkPointKey(forkPoint.conversationId, forkPoint.previousMessageId ?? null);
    return getForkOptionsForPoint(key);
  }, [forkPoint, forkPointKey, getForkOptionsForPoint]);

  const forkOptionsByMessageId = useMemo(() => {
    const map = new Map<string, ForkOption[]>();
    messages.forEach((message) => {
      const messageConversationId = message.conversationId;
      const messageMeta = messageConversationId ? conversationMetaById.get(messageConversationId) : null;
      const messagePreviousId = message.previousMessageId ?? null;
      const pointConversationId =
        messagePreviousId === null && messageMeta?.forkedAtConversationId
          ? messageMeta.forkedAtConversationId
          : messageConversationId;
      const pointPreviousId =
        messagePreviousId === null && messageMeta?.forkedAtConversationId
          ? (messageMeta.forkedAtMessageId ?? null)
          : messagePreviousId;
      const forkKey = forkPointKey(pointConversationId, pointPreviousId);
      map.set(message.id, getForkOptionsForPoint(forkKey));
    });
    return map;
  }, [conversationMetaById, forkPointKey, getForkOptionsForPoint, messages]);

  const forkLoading = forksQuery.isLoading || forksQuery.isFetching;
  const isForkMenuOpen = Boolean(openForkMenuMessageId && forkPoint);

  // Check if user is near bottom of viewport (within 100px)
  const checkNearBottom = useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport) {
      return false;
    }
    const threshold = 100;
    const distanceFromBottom = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight;
    return distanceFromBottom <= threshold;
  }, []);

  // Scroll to bottom of viewport
  const scrollToBottom = useCallback(
    (behavior: ScrollBehavior = "smooth") => {
      const viewport = viewportRef.current;
      if (!viewport) {
        return;
      }

      // Try to scroll to the last message element if available
      if (messages.length > 0) {
        const lastMessage = messages[messages.length - 1];
        const lastMessageElement = messageRefs.current.get(lastMessage.id);
        if (lastMessageElement) {
          lastMessageElement.scrollIntoView({ behavior, block: "end" });
          return;
        }
      }

      // Fallback to scrolling to bottom of viewport
      viewport.scrollTo({
        top: viewport.scrollHeight,
        behavior,
      });
    },
    [messages],
  );

  // Handle scroll events to track if user is near bottom
  const handleScroll = useCallback(() => {
    isNearBottomRef.current = checkNearBottom();
    // If user manually scrolls away from bottom, disable auto-scroll
    if (!isNearBottomRef.current) {
      shouldAutoScrollRef.current = false;
    }
  }, [checkNearBottom]);

  // Scroll to bottom on initial load
  useLayoutEffect(() => {
    if (!isResolvedConversation || !conversationId) {
      return;
    }
    if (messages.length > 0 && isInitialLoadRef.current) {
      isInitialLoadRef.current = false;
      console.info("[ChatPanel] initial-scroll", {
        conversationId,
        messagesLength: messages.length,
        lastMessageId: messages[messages.length - 1]?.id ?? null,
      });
      // Use multiple delays to ensure DOM is fully rendered and messages are laid out
      const attemptScroll = () => {
        const viewport = viewportRef.current;
        const lastMessage = messages[messages.length - 1];
        const lastMessageElement = messageRefs.current.get(lastMessage?.id);

        if (viewport) {
          // If we have the last message element, use it; otherwise use scrollHeight
          if (lastMessageElement) {
            lastMessageElement.scrollIntoView({ behavior: "instant", block: "end" });
            shouldAutoScrollRef.current = true;
            isNearBottomRef.current = true;
          } else if (viewport.scrollHeight > viewport.clientHeight) {
            viewport.scrollTo({ top: viewport.scrollHeight, behavior: "instant" });
            shouldAutoScrollRef.current = true;
            isNearBottomRef.current = true;
          }
        }
      };

      // Try multiple times with increasing delays to catch different render phases
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          attemptScroll();
          // Also try after a short delay in case messages are still rendering
          setTimeout(attemptScroll, 50);
          setTimeout(attemptScroll, 200);
        });
      });
    }
  }, [conversationId, isResolvedConversation, messages.length, messages]);

  useEffect(() => {
    isInitialLoadRef.current = true;
    shouldAutoScrollRef.current = true;
    isNearBottomRef.current = true;
    lastMessageCountRef.current = 0;
  }, [conversationId]);

  // Auto-scroll when new messages arrive or content streams, if near bottom
  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || messages.length === 0) {
      return;
    }

    const messageCountChanged = messages.length !== lastMessageCountRef.current;
    const wasEmpty = lastMessageCountRef.current === 0;
    const hasStreamingMessage = messages.some((msg) => msg.displayState === "streaming");

    // Update near-bottom status
    isNearBottomRef.current = checkNearBottom();

    // If this is the first time messages appear (initial load), force scroll to bottom
    if (wasEmpty && isInitialLoadRef.current && viewport.scrollHeight > viewport.clientHeight) {
      isInitialLoadRef.current = false;
      requestAnimationFrame(() => {
        scrollToBottom("instant");
        shouldAutoScrollRef.current = true;
        isNearBottomRef.current = true;
      });
      lastMessageCountRef.current = messages.length;
      return;
    }

    // If user scrolled away, don't auto-scroll unless they scroll back near bottom
    if (!shouldAutoScrollRef.current && !isNearBottomRef.current) {
      lastMessageCountRef.current = messages.length;
      return;
    }

    // Re-enable auto-scroll if user scrolls back near bottom
    if (isNearBottomRef.current) {
      shouldAutoScrollRef.current = true;
    }

    // Auto-scroll if near bottom and (messages changed or streaming)
    if ((messageCountChanged || hasStreamingMessage) && shouldAutoScrollRef.current) {
      requestAnimationFrame(() => {
        // Double-check we're still near bottom before scrolling
        if (checkNearBottom()) {
          scrollToBottom("smooth");
        }
      });
    }

    lastMessageCountRef.current = messages.length;
  }, [messages, checkNearBottom, scrollToBottom]);

  // Attach scroll listener
  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) {
      return;
    }
    viewport.addEventListener("scroll", handleScroll, { passive: true });
    return () => {
      viewport.removeEventListener("scroll", handleScroll);
    };
  }, [handleScroll]);

  const handleForkSelect = useCallback(
    (selectedConversationId: string) => {
      onSelectConversationId?.(selectedConversationId);
      setOpenForkMenuMessageId(null);
      setActiveForkMenuMessageId(null);
    },
    [onSelectConversationId],
  );

  const handleEditStart = useCallback((message: ConversationMessage) => {
    if (!message.id || !message.conversationId) {
      return;
    }
    setEditingMessage({ id: message.id, conversationId: message.conversationId });
    setEditingText(message.content);
    setActiveForkMenuMessageId(null);
    setOpenForkMenuMessageId(null);
  }, []);

  const handleEditCancel = useCallback(() => {
    setEditingMessage(null);
    setEditingText("");
  }, []);

  const handleForkSend = useCallback(async () => {
    const trimmed = editingText.trim();
    if (!trimmed || !editingMessage) {
      return;
    }

    setForking(true);
    try {
      const response = (await ConversationsService.forkConversationAtMessage({
        conversationId: editingMessage.conversationId,
        messageId: editingMessage.id,
        requestBody: {},
      })) as ApiConversation;

      if (!response?.id) {
        return;
      }

      pendingForkRef.current = {
        conversationId: response.id,
        message: trimmed,
      };
      setEditingMessage(null);
      setEditingText("");
      setActiveForkMenuMessageId(null);
      onSelectConversationId?.(response.id);
      void queryClient.invalidateQueries({ queryKey: ["conversations"] });
      void queryClient.invalidateQueries({ queryKey: ["conversation-forks", editingMessage.conversationId] });
    } catch (error) {
      void error;
    } finally {
      setForking(false);
    }
  }, [editingMessage, editingText, onSelectConversationId, pendingForkRef, queryClient]);

  const composerDisabled = isBusy || forking || canceling;

  return (
    <main className="flex flex-1 flex-col bg-muted/20">
      <div className="border-b px-6 py-4">
        <h2 className="text-lg font-semibold">Chat with your agent</h2>
        <p className="text-xs text-muted-foreground">
          Start a new chat or select a conversation from the left to continue.
        </p>
        <div className="mt-3 flex flex-wrap items-center gap-2 text-xs">
          <span className="font-semibold text-foreground/70">Stream via</span>
          <ToggleGroup
            type="single"
            value={streamMode}
            onValueChange={(value) => setStreamMode(value as StreamMode)}
            variant="outline"
          >
            <ToggleGroupItem value="sse" aria-label="Use SSE stream">
              Server Sent Events
            </ToggleGroupItem>
            <ToggleGroupItem value="websocket" aria-label="Use WebSocket stream">
              WebSocket
            </ToggleGroupItem>
          </ToggleGroup>
        </div>
      </div>

      <ConversationsUI.Viewport ref={viewportRef} className="relative pt-0">
        <ConversationsUI.Messages>
          {(items) => {
            if (items.length === 0) {
              return <ConversationsUI.EmptyState />;
            }

            const turns = items.reduce<
              {
                key: string;
                user: RenderableConversationMessage | null;
                assistants: RenderableConversationMessage[];
              }[]
            >((acc, message, index) => {
              if (message.author === "user") {
                acc.push({
                  key: `turn-${message.id ?? index}`,
                  user: message,
                  assistants: [],
                });
              } else {
                const lastTurn = acc[acc.length - 1];
                if (lastTurn) {
                  lastTurn.assistants.push(message);
                } else {
                  acc.push({
                    key: `turn-${message.id ?? index}`,
                    user: null,
                    assistants: [message],
                  });
                }
              }
              return acc;
            }, []);

            return turns.map((turn) => (
              <section key={turn.key} className="relative flex flex-col gap-3">
                {turn.user ? (
                  <div className="sticky top-0 isolation-auto z-20 pb-3">
                    <div className="relative">
                      {/* todo: use a flex layout so the first div grows vertically and the second div can remain a fixed height. */}
                      <div className="pointer-events-none absolute left-0 top-0 z-0 w-full">
                        <div className="h-8 bg-background" />
                        <div className="h-16 bg-gradient-to-b from-background via-background/85 to-background/0" />
                      </div>
                      <div className="relative z-10">
                        <div className="pt-2">
                          <ChatMessageRow
                            key={turn.user.id}
                            message={turn.user}
                            isEditing={
                              turn.user.displayState === "stable" &&
                              editingMessage?.id === turn.user.id &&
                              editingMessage?.conversationId === turn.user.conversationId
                            }
                            editingText={editingText}
                            onEditingTextChange={setEditingText}
                            onEditStart={handleEditStart}
                            onEditCancel={handleEditCancel}
                            onForkSend={handleForkSend}
                            composerDisabled={composerDisabled}
                            conversationId={conversationId}
                            forkOptionsCount={(forkOptionsByMessageId.get(turn.user.id) ?? []).length}
                            forkLabels={forkLabels}
                            setForkLabels={setForkLabels}
                            activeForkMenuMessageId={activeForkMenuMessageId}
                            setActiveForkMenuMessageId={setActiveForkMenuMessageId}
                            userMessageIndexById={userMessageIndexById}
                            selectForkLabel={selectForkLabel}
                            formatForkLabel={formatForkLabel}
                            formatForkTimestamp={formatForkTimestamp}
                            forkPoint={openForkMenuMessageId === turn.user.id ? forkPoint : null}
                            forkOptions={openForkMenuMessageId === turn.user.id ? forkOptions : []}
                            forkLoading={openForkMenuMessageId === turn.user.id ? forkLoading : false}
                            isForkMenuOpen={openForkMenuMessageId === turn.user.id && isForkMenuOpen}
                            onForkSelect={handleForkSelect}
                            openForkMenuMessageId={openForkMenuMessageId}
                            setOpenForkMenuMessageId={setOpenForkMenuMessageId}
                            messageRef={(el) => {
                              if (el) {
                                messageRefs.current.set(turn.user!.id, el);
                              } else {
                                messageRefs.current.delete(turn.user!.id);
                              }
                            }}
                          />
                        </div>
                      </div>
                    </div>
                  </div>
                ) : null}
                {turn.assistants.length > 0 ? (
                  <div className="flex flex-col gap-3">
                    {turn.assistants.map((message) => {
                      const isEditing =
                        message.displayState === "stable" &&
                        editingMessage?.id === message.id &&
                        editingMessage?.conversationId === message.conversationId;
                      const messageForkOptions = forkOptionsByMessageId.get(message.id) ?? [];
                      return (
                        <ChatMessageRow
                          key={message.id}
                          message={message}
                          isEditing={isEditing}
                          editingText={editingText}
                          onEditingTextChange={setEditingText}
                          onEditStart={handleEditStart}
                          onEditCancel={handleEditCancel}
                          onForkSend={handleForkSend}
                          composerDisabled={composerDisabled}
                          conversationId={conversationId}
                          forkOptionsCount={messageForkOptions.length}
                          forkLabels={forkLabels}
                          setForkLabels={setForkLabels}
                          activeForkMenuMessageId={activeForkMenuMessageId}
                          setActiveForkMenuMessageId={setActiveForkMenuMessageId}
                          userMessageIndexById={userMessageIndexById}
                          selectForkLabel={selectForkLabel}
                          formatForkLabel={formatForkLabel}
                          formatForkTimestamp={formatForkTimestamp}
                          forkPoint={openForkMenuMessageId === message.id ? forkPoint : null}
                          forkOptions={openForkMenuMessageId === message.id ? forkOptions : []}
                          forkLoading={openForkMenuMessageId === message.id ? forkLoading : false}
                          isForkMenuOpen={openForkMenuMessageId === message.id && isForkMenuOpen}
                          onForkSelect={handleForkSelect}
                          openForkMenuMessageId={openForkMenuMessageId}
                          setOpenForkMenuMessageId={setOpenForkMenuMessageId}
                          messageRef={(el) => {
                            if (el) {
                              messageRefs.current.set(message.id, el);
                            } else {
                              messageRefs.current.delete(message.id);
                            }
                          }}
                        />
                      );
                    })}
                  </div>
                ) : null}
              </section>
            ));
          }}
        </ConversationsUI.Messages>
      </ConversationsUI.Viewport>

      <ConversationsUI.Composer
        disabled={!conversationId || composerDisabled || Boolean(editingMessage)}
        cancelDisabled={canceling}
        sendLabel="Send"
        stopLabel="Stop"
      />
    </main>
  );
}

export function ChatPanel({
  conversationId,
  onSelectConversationId,
  knownConversationIds,
  resumableConversationIds,
}: ChatPanelProps) {
  const [assistantIdOverrides, setAssistantIdOverrides] = useState<Record<string, string>>({});
  const assistantIdOverridesRef = useRef<Record<string, string>>({});
  const [canceling, setCanceling] = useState(false);
  const [streamMode, setStreamMode] = useState<StreamMode>("sse");
  // Track pending IDs per conversation to prevent desync across concurrent streams
  // The queue stores pairs: [assistantId, userId] for each stream start
  const pendingIdQueueRef = useRef<Map<string, string[]>>(new Map());
  const pendingAssistantIdsRef = useRef<Map<string, string[]>>(new Map());
  const lastAssistantIdRef = useRef<Map<string, string | null>>(new Map());
  // Temporary storage for IDs generated before we know the conversation
  // These will be moved to per-conversation queues when startStream/resumeStream is called
  const tempIdQueueRef = useRef<string[]>([]);
  const firstChunkEmittedRef = useRef<Record<string, boolean>>({});
  const hasResumedRef = useRef<Record<string, boolean>>({});
  const pendingForkRef = useRef<PendingFork | null>(null);

  const sseStream = useSseStream();
  // Keep ref in sync with state
  useEffect(() => {
    assistantIdOverridesRef.current = assistantIdOverrides;
  }, [assistantIdOverrides]);
  const webSocketStream = useWebSocketStream();
  const queryClient = useQueryClient();

  // Reset resume tracking and pending IDs when switching conversations
  const previousConversationIdRef = useRef<string | null>(null);
  useLayoutEffect(() => {
    if (previousConversationIdRef.current !== conversationId) {
      const previousId = previousConversationIdRef.current;
      if (previousId) {
        delete hasResumedRef.current[previousId];
        pendingAssistantIdsRef.current.delete(previousId);
        // Clear per-conversation pending ID queue when switching away
        pendingIdQueueRef.current.delete(previousId);
      }
      previousConversationIdRef.current = conversationId;
      if (conversationId) {
        // Reset resume flag when switching TO a conversation to allow resume on switch back
        delete hasResumedRef.current[conversationId];
      }
      webSocketStream.close();
      sseStream.close();
    }
  }, [conversationId, webSocketStream, sseStream]);

  // Reset hasResumed flag when resumableConversationIds changes to allow retry
  // This handles the case where the resume check query completes after the effect first runs
  useEffect(() => {
    // Effect intentionally empty - the auto-resume effect will re-run when resumableConversationIds changes
  }, [conversationId, resumableConversationIds]);

  useEffect(() => {
    return () => {
      webSocketStream.close();
      sseStream.close();
    };
  }, [webSocketStream, sseStream]);

  const isResolvedConversation = Boolean(conversationId && knownConversationIds?.has(conversationId));
  const forksQuery = useQuery<ConversationForkSummary[], ApiError, ConversationForkSummary[]>({
    queryKey: ["conversation-forks", conversationId],
    enabled: isResolvedConversation,
    queryFn: async (): Promise<ConversationForkSummary[]> => {
      const response = (await ConversationsService.listConversationForks({
        conversationId: conversationId!,
      })) as unknown as ListConversationForksResponse;
      const data = Array.isArray(response.data) ? response.data : [];
      return data;
    },
  });

  const conversationQuery = useQuery<ApiConversation, ApiError, ApiConversation>({
    queryKey: ["conversation", conversationId],
    enabled: isResolvedConversation,
    queryFn: async (): Promise<ApiConversation> => {
      const convo = (await ConversationsService.getConversation({
        conversationId: conversationId!,
      })) as ApiConversation;
      return convo;
    },
  });

  const forkFingerprint = useMemo(() => {
    const forks = forksQuery.data ?? [];
    if (!forks.length) {
      return "none";
    }
    return forks
      .map(
        (fork) => `${fork.conversationId ?? ""}:${fork.forkedAtConversationId ?? ""}:${fork.forkedAtMessageId ?? ""}`,
      )
      .sort()
      .join("|");
  }, [forksQuery.data]);

  const messagesQuery = useQuery<Message[], ApiError, Message[]>({
    queryKey: ["conversation-path-messages", conversationId, forkFingerprint],
    enabled: Boolean(conversationId && conversationQuery.data && forksQuery.data),
    queryFn: async (): Promise<Message[]> => {
      const currentConversation = conversationQuery.data!;
      const forks = forksQuery.data ?? [];
      const forkMetaById = new Map<
        string,
        { forkedAtConversationId?: string | null; forkedAtMessageId?: string | null }
      >();

      forks.forEach((fork) => {
        if (!fork.conversationId) {
          return;
        }
        forkMetaById.set(fork.conversationId, {
          forkedAtConversationId: fork.forkedAtConversationId ?? null,
          forkedAtMessageId: fork.forkedAtMessageId ?? null,
        });
      });

      forkMetaById.set(conversationId!, {
        forkedAtConversationId: currentConversation.forkedAtConversationId ?? null,
        forkedAtMessageId: currentConversation.forkedAtMessageId ?? null,
      });

      const lineage: string[] = [];
      const seen = new Set<string>();
      let cursor: string | null = conversationId!;
      while (cursor && !seen.has(cursor)) {
        lineage.push(cursor);
        seen.add(cursor);
        const meta = forkMetaById.get(cursor);
        if (!meta?.forkedAtConversationId) {
          break;
        }
        cursor = meta.forkedAtConversationId;
      }
      lineage.reverse();

      const messagesByConversation = new Map<string, Message[]>();
      await Promise.all(
        lineage.map(async (id) => {
          const response = (await ConversationsService.listConversationMessages({
            conversationId: id,
            limit: 200,
            channel: "history",
          })) as unknown as ListUserMessagesResponse;
          messagesByConversation.set(id, Array.isArray(response.data) ? response.data : []);
        }),
      );

      const combined: Message[] = [];
      for (let i = 0; i < lineage.length; i += 1) {
        const conversationMessages = messagesByConversation.get(lineage[i]) ?? [];
        const childId = lineage[i + 1];
        if (!childId) {
          combined.push(...conversationMessages);
          continue;
        }
        const childMeta = forkMetaById.get(childId);
        if (childMeta && childMeta.forkedAtMessageId === null) {
          continue;
        }
        if (childMeta?.forkedAtMessageId) {
          const forkedIndex = conversationMessages.findIndex((msg) => msg.id === childMeta.forkedAtMessageId);
          if (forkedIndex >= 0) {
            combined.push(...conversationMessages.slice(0, forkedIndex + 1));
            continue;
          }
        }
        combined.push(...conversationMessages);
      }

      return combined;
    },
  });

  const conversationMetaById = useMemo(() => {
    const map = new Map<string, ConversationMeta>();
    for (const fork of forksQuery.data ?? []) {
      if (!fork.conversationId) {
        continue;
      }
      map.set(fork.conversationId, {
        forkedAtConversationId: fork.forkedAtConversationId ?? null,
        forkedAtMessageId: fork.forkedAtMessageId ?? null,
      });
    }
    if (conversationId && conversationQuery.data) {
      map.set(conversationId, {
        forkedAtConversationId: conversationQuery.data.forkedAtConversationId ?? null,
        forkedAtMessageId: conversationQuery.data.forkedAtMessageId ?? null,
      });
    }
    return map;
  }, [conversationId, conversationQuery.data, forksQuery.data]);

  useEffect(() => {
    const messages = messagesQuery.data ?? [];
    if (!messages.length) {
      return;
    }
    const lastAssistantByConversation = new Map<string, string>();
    messages.forEach((msg) => {
      if (!msg.id || !msg.conversationId) {
        return;
      }
      if (messageAuthor(msg) !== "assistant") {
        return;
      }
      lastAssistantByConversation.set(msg.conversationId, msg.id);
    });
    let updates: Record<string, string> | null = null;
    lastAssistantByConversation.forEach((assistantId, conversationKey) => {
      // Compute the resolved ID (after applying any existing overrides)
      // Use ref to get current value without adding to dependencies
      const resolved = assistantIdOverridesRef.current[assistantId] ?? assistantId;
      const previous = lastAssistantIdRef.current.get(conversationKey) ?? null;
      // Compare previous to resolved ID to avoid re-processing the same message
      if (previous && previous === resolved) {
        return;
      }
      const pendingIds = pendingAssistantIdsRef.current.get(conversationKey);
      const pendingId = pendingIds?.shift();
      if (!pendingId) {
        // No pending ID available, just track the resolved backend ID
        lastAssistantIdRef.current.set(conversationKey, resolved);
        return;
      }
      // Align backend assistant IDs with optimistic IDs to prevent duplicates.
      updates = updates ?? {};
      updates[assistantId] = pendingId;
      // Store the resolved ID (pendingId) for next comparison
      lastAssistantIdRef.current.set(conversationKey, pendingId);
    });
    if (updates) {
      setAssistantIdOverrides((prev) => ({ ...prev, ...updates }));
    }
  }, [messagesQuery.data]);

  const conversationMessages = useMemo<ConversationMessage[]>(() => {
    const messages = messagesQuery.data ?? [];
    const firstIndexByConversation = new Map<string, number>();
    const mapped: ConversationMessage[] = [];

    messages.forEach((msg) => {
      if (!msg.id || !msg.conversationId) {
        return;
      }
      const author = messageAuthor(msg);
      const resolvedId = author === "assistant" ? (assistantIdOverrides[msg.id] ?? msg.id) : msg.id;
      if (!firstIndexByConversation.has(msg.conversationId)) {
        firstIndexByConversation.set(msg.conversationId, mapped.length);
      }
      mapped.push({
        id: resolvedId,
        conversationId: msg.conversationId,
        author,
        content: messageText(msg),
        createdAt: msg.createdAt,
      });
    });

    return mapped.map((msg, index) => {
      const firstIndex = firstIndexByConversation.get(msg.conversationId);
      if (firstIndex !== index) {
        return msg;
      }
      const meta = conversationMetaById.get(msg.conversationId);
      if (!meta?.forkedAtConversationId) {
        return msg;
      }
      return {
        ...msg,
        forkedFrom: {
          conversationId: meta.forkedAtConversationId,
          messageId: meta.forkedAtMessageId ?? null,
        },
      };
    });
  }, [assistantIdOverrides, conversationMetaById, messagesQuery.data]);

  const conversationGroupId = conversationQuery.data?.conversationGroupId ?? null;

  const userMessageIndexById = useMemo(() => {
    const indexById = new Map<string, number>();
    let index = 0;
    conversationMessages.forEach((message) => {
      if (message.author !== "user") {
        return;
      }
      indexById.set(message.id, index);
      index += 1;
    });
    return indexById;
  }, [conversationMessages]);

  const startEventStream = useCallback(
    (
      targetConversationId: string,
      text: string,
      resumePosition: number,
      resetResume: boolean,
      callbacks: {
        onChunk?: (chunk: string) => void;
        onComplete?: () => void;
        onError?: (error: unknown) => void;
      },
    ) => {
      const appendAssistantChunk = (chunk: string) => {
        if (!chunk.length) {
          return;
        }
        callbacks.onChunk?.(chunk);
        if (!firstChunkEmittedRef.current[targetConversationId]) {
          firstChunkEmittedRef.current[targetConversationId] = true;
          void queryClient.invalidateQueries({ queryKey: ["conversations"] });
          void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
        }
      };
      const params: StreamStartParams = {
        sessionId: targetConversationId,
        text,
        resumePosition,
        resetResume,
        onChunk: appendAssistantChunk,
        onReplayFailed: () => {
          callbacks.onComplete?.();
          void queryClient.invalidateQueries({ queryKey: ["conversation-path-messages", targetConversationId] });
          void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
        },
        onCleanEnd: () => {
          callbacks.onComplete?.();
          void queryClient.invalidateQueries({ queryKey: ["conversation-path-messages", targetConversationId] });
          void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
        },
        onError: (error: unknown) => {
          callbacks.onError?.(error);
          void queryClient.invalidateQueries({ queryKey: ["conversation-path-messages", targetConversationId] });
          void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
        },
      };

      const useSse = streamMode === "sse";

      if (useSse) {
        sseStream.close();
        webSocketStream.close();
        try {
          sseStream.start(params);
        } catch (error) {
          callbacks.onError?.(error);
        }
        return;
      }

      sseStream.close();
      webSocketStream.close();
      // Wrap in try/catch to handle synchronous errors from webSocketStream.start()
      try {
        webSocketStream.start(params);
      } catch (error) {
        callbacks.onError?.(error);
      }
    },
    [queryClient, sseStream, streamMode, webSocketStream],
  );

  const idFactory = useCallback(() => {
    const id = typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `msg-${Date.now()}`;
    // IDs are generated before we know which conversation they're for
    // Store in temp queue, will be moved to per-conversation queue in startStream/resumeStream
    tempIdQueueRef.current.push(id);
    return id;
  }, []);

  const controller = useMemo<ConversationController>(
    () => ({
      idFactory,
      startStream: async (targetConversationId, text, callbacks) => {
        // IDs were generated before this call, so they're in temp queue
        // Move the first 2 IDs (assistant, user) from temp queue to this conversation's queue
        // This ensures per-conversation tracking. We use FIFO since ids are generated in order.
        const tempQueue = tempIdQueueRef.current;
        if (tempQueue.length >= 2) {
          // Take the first 2 IDs (FIFO: assistant, then user)
          const assistantId = tempQueue.shift()!;
          const userId = tempQueue.shift()!;

          // Add to conversation queue
          const conversationQueue = pendingIdQueueRef.current.get(targetConversationId) ?? [];
          conversationQueue.push(assistantId, userId);
          pendingIdQueueRef.current.set(targetConversationId, conversationQueue);
        }

        // Get IDs from per-conversation queue
        const conversationQueue = pendingIdQueueRef.current.get(targetConversationId) ?? [];
        const pendingAssistantId = conversationQueue.shift();
        conversationQueue.shift(); // user message ID (consumed but not used)
        if (conversationQueue.length === 0) {
          pendingIdQueueRef.current.delete(targetConversationId);
        } else {
          pendingIdQueueRef.current.set(targetConversationId, conversationQueue);
        }

        if (pendingAssistantId) {
          const queue = pendingAssistantIdsRef.current.get(targetConversationId) ?? [];
          queue.push(pendingAssistantId);
          pendingAssistantIdsRef.current.set(targetConversationId, queue);
        }
        startEventStream(targetConversationId, text, 0, true, callbacks);
      },
      resumeStream: async (targetConversationId, callbacks) => {
        if (!callbacks.replaceMessageId) {
          // IDs may have been generated before this call
          // Move the first ID from temp queue to this conversation's queue if present (FIFO)
          const tempQueue = tempIdQueueRef.current;
          if (tempQueue.length >= 1) {
            const assistantId = tempQueue.shift()!;
            const conversationQueue = pendingIdQueueRef.current.get(targetConversationId) ?? [];
            conversationQueue.push(assistantId);
            pendingIdQueueRef.current.set(targetConversationId, conversationQueue);
          }

          // Get ID from per-conversation queue
          const conversationQueue = pendingIdQueueRef.current.get(targetConversationId) ?? [];
          const pendingAssistantId = conversationQueue.shift();
          if (conversationQueue.length === 0) {
            pendingIdQueueRef.current.delete(targetConversationId);
          } else {
            pendingIdQueueRef.current.set(targetConversationId, conversationQueue);
          }

          if (pendingAssistantId) {
            const queue = pendingAssistantIdsRef.current.get(targetConversationId) ?? [];
            queue.push(pendingAssistantId);
            pendingAssistantIdsRef.current.set(targetConversationId, queue);
          }
        }
        startEventStream(targetConversationId, "", 0, false, callbacks);
      },
      cancelStream: async (targetConversationId) => {
        if (!targetConversationId) {
          return;
        }
        setCanceling(true);
        try {
          await ConversationsService.cancelConversationResponse({ conversationId: targetConversationId });
        } catch (error) {
          void error;
        } finally {
          setCanceling(false);
        }
        webSocketStream.close();
        void queryClient.invalidateQueries({ queryKey: ["conversation-path-messages", targetConversationId] });
        void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
      },
      selectConversation: async (id) => {
        onSelectConversationId?.(id);
      },
    }),
    [idFactory, onSelectConversationId, queryClient, startEventStream, webSocketStream],
  );

  return (
    <Conversation.Root
      controller={controller}
      conversationId={conversationId}
      conversationGroupId={conversationGroupId}
      messages={conversationMessages}
    >
      <ChatPanelContent
        conversationId={conversationId}
        isResolvedConversation={isResolvedConversation}
        resumableConversationIds={resumableConversationIds}
        pendingForkRef={pendingForkRef}
        hasResumedRef={hasResumedRef}
        onSelectConversationId={onSelectConversationId}
        queryClient={queryClient}
        userMessageIndexById={userMessageIndexById}
        canceling={canceling}
        forksQuery={forksQuery}
        conversationMetaById={conversationMetaById}
        streamMode={streamMode}
        setStreamMode={setStreamMode}
      />
    </Conversation.Root>
  );
}
