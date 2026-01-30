import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type React from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
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
import type { ApiError, Conversation as ApiConversation, ConversationForkSummary, Entry } from "@/client";
import { ConversationsService } from "@/client";
import { useWebSocketStream } from "@/hooks/useWebSocketStream";
import { useSseStream } from "@/hooks/useSseStream";
import type { StreamStartParams } from "@/hooks/useStreamTypes";
import { Check, Copy, CornerUpLeft, Menu, Pencil, Sparkles, Trash2 } from "lucide-react";
import { ShareButton } from "@/components/sharing";

type ListUserEntriesResponse = {
  data?: Entry[];
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
  forkedAtEntryId?: string | null;
  createdAt?: string | null;
  label?: string;
};

type ChatPanelProps = {
  conversationId: string | null;
  onSelectConversationId?: (conversationId: string) => void;
  resumableConversationIds?: Set<string>;
  knownConversationIds?: Set<string>;
  onIndexConversation?: (conversationId: string) => void;
  onDeleteConversation?: (conversationId: string) => void;
  currentUserId?: string | null;
};

type PendingFork = {
  conversationId: string;
  message: string;
};

type ConversationMeta = {
  forkedAtConversationId: string | null;
  forkedAtEntryId: string | null;
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
  onCopy: (content: string) => void;
  copiedMessageId: string | null;
  composerDisabled: boolean;
  isReader: boolean;
  conversationId: string | null;
  forkOptionsCount: number;
  forkLabels: Record<string, string>;
  setForkLabels: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  activeForkMenuMessageId: string | null;
  setActiveForkMenuMessageId: (id: string | null) => void;
  userMessageIndexById: Map<string, number>;
  selectForkLabel: (entries: Entry[], userIndex?: number) => string;
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
  onCopy,
  copiedMessageId,
  composerDisabled,
  isReader,
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
  const isCopied = copiedMessageId === message.id;

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
          const response = (await ConversationsService.listConversationEntries({
            conversationId: fork.conversationId,
            limit: 200,
            channel: "history",
          })) as unknown as ListUserEntriesResponse;
          const entries = Array.isArray(response.data) ? response.data : [];
          const entryId = activeForkMenuMessageId ?? message.id;
          const userIndex =
            entryId && userMessageIndexById.has(entryId) ? userMessageIndexById.get(entryId) : undefined;
          const label = selectForkLabel(entries, userIndex);
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
        <div className={`w-full max-w-[75%] ${isUser ? "" : ""}`}>
          {/* Edit mode */}
          <div className="edit-glow rounded-2xl border-2 border-sage bg-cream px-5 py-4">
            <textarea
              value={editingText}
              onChange={(event) => onEditingTextChange(event.target.value)}
              rows={3}
              className="w-full resize-none bg-transparent text-[15px] leading-relaxed text-ink focus:outline-none"
            />
            <div className="mt-3 flex items-center justify-end gap-2 border-t border-stone/10 pt-3">
              <button
                type="button"
                onClick={onEditCancel}
                disabled={composerDisabled}
                className="px-4 py-2 text-sm text-stone transition-colors hover:text-ink disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={onForkSend}
                disabled={composerDisabled || !editingText.trim()}
                className="rounded-full bg-ink px-4 py-2 text-sm font-medium text-cream transition-colors hover:bg-ink/90 disabled:opacity-50"
              >
                Send
              </button>
            </div>
          </div>
          <p className="mt-2 text-right text-xs text-terracotta">Editing will create a new fork from this point</p>
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
          message.displayState === "stable" ? (
            <div className="absolute -bottom-6 right-1 z-10 flex gap-1 opacity-0 transition-opacity group-hover:opacity-100">
              <button
                type="button"
                onClick={() => onCopy(message.content)}
                className="rounded p-1 text-stone transition-colors hover:bg-mist hover:text-ink"
                title="Copy message"
              >
                {isCopied ? <Check className="h-3.5 w-3.5 text-sage" /> : <Copy className="h-3.5 w-3.5" />}
              </button>
              {isUser && !isReader && (
                <button
                  type="button"
                  onClick={() => onEditStart(message)}
                  disabled={composerDisabled}
                  className="rounded p-1 text-stone transition-colors hover:bg-mist hover:text-terracotta disabled:opacity-50"
                  title="Edit message"
                >
                  <Pencil className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          ) : null
        }
      />
      {hasForks ? (
        <div className={`relative mt-2 ${isUser ? "pr-1 text-right" : "pl-2"}`}>
          <button
            type="button"
            className="elegant-link pointer-events-auto text-xs text-stone transition-colors hover:text-ink"
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
            {forkOptionsCount} forks
          </button>
          {isForkMenuOpen && forkPoint ? (
            <div className="absolute left-0 z-30 mt-2 w-72 animate-slide-up overflow-hidden rounded-xl border border-stone/20 bg-cream shadow-xl">
              <div className="border-b border-stone/10 bg-mist/50 px-4 py-2.5">
                <span className="text-xs font-medium text-stone">Fork Branches</span>
              </div>
              <div className="py-1">
                {forkLoading ? (
                  <div className="px-4 py-3 text-sm text-stone">Loading...</div>
                ) : forkOptions.length === 0 ? null : (
                  <>
                    {forkOptions.map((fork) => {
                      const isActive = fork.conversationId === conversationId;
                      const fallbackLabel = isActive ? message.content : "Loading fork message...";
                      const label = forkLabels[fork.conversationId] ?? fallbackLabel;
                      return (
                        <button
                          key={fork.conversationId}
                          type="button"
                          onClick={() => onForkSelect(fork.conversationId)}
                          className={`w-full px-4 py-3 text-left transition-colors ${
                            isActive ? "border-l-2 border-sage bg-sage/10" : "hover:bg-mist/50"
                          }`}
                        >
                          <div className="flex items-center justify-between">
                            <div className="min-w-0 flex-1">
                              <p className={`truncate text-sm ${isActive ? "text-ink" : "text-stone"}`}>
                                "{formatForkLabel(label)}"
                              </p>
                              <span className={`text-xs ${isActive ? "text-stone" : "text-stone/60"}`}>
                                {formatForkTimestamp(fork.createdAt)}
                              </span>
                            </div>
                            {isActive ? (
                              <span className="ml-2 rounded-full border border-sage/50 px-2 py-0.5 text-[10px] font-medium text-sage">
                                Active
                              </span>
                            ) : null}
                          </div>
                        </button>
                      );
                    })}
                    {/* Parent option */}
                    <div className="mt-1 border-t border-stone/10 pt-1">
                      <button
                        type="button"
                        className="flex w-full items-center gap-2 px-4 py-3 text-left transition-colors hover:bg-mist/50"
                        onClick={() => {
                          setOpenForkMenuMessageId(null);
                          setActiveForkMenuMessageId(null);
                        }}
                      >
                        <CornerUpLeft className="h-4 w-4 text-stone" />
                        <p className="text-sm text-stone">Return to parent conversation</p>
                      </button>
                    </div>
                  </>
                )}
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function entryText(entry: Entry): string {
  const blocks = entry.content ?? [];
  const textBlock = blocks.find((b) => {
    const block = b as { [key: string]: unknown } | undefined;
    return block && typeof block.text === "string";
  }) as { text: string } | undefined;
  return textBlock?.text ?? "";
}

function entryAuthor(entry: Entry): "user" | "assistant" {
  const blocks = entry.content ?? [];
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
  return entry.userId ? "user" : "assistant";
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
  onIndexConversation?: (conversationId: string) => void;
  onDeleteConversation?: (conversationId: string) => void;
  currentUserId?: string | null;
};

function formatConversationTime(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "numeric",
  }).format(date);
}

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
  streamMode: _streamMode,
  setStreamMode: _setStreamMode,
  forksQuery,
  conversationMetaById,
  conversationQuery,
  onIndexConversation,
  onDeleteConversation,
  currentUserId,
}: ChatPanelContentProps & {
  forksQuery: ReturnType<typeof useQuery<ConversationForkSummary[], ApiError, ConversationForkSummary[]>>;
  conversationMetaById: Map<string, ConversationMeta>;
  conversationQuery: ReturnType<typeof useQuery<ApiConversation, ApiError, ApiConversation>>;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const { messages } = useConversationMessages();
  const { resumeStream, isBusy } = useConversationStreaming();
  const { submit } = useConversationInput();
  const [forking, setForking] = useState(false);
  const [editingMessage, setEditingMessage] = useState<{ id: string; conversationId: string } | null>(null);
  const [editingText, setEditingText] = useState("");
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null);
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

  const selectForkLabel = (entries: Entry[], userIndex?: number) => {
    if (!entries.length) {
      return "Forked entry";
    }
    const userEntries = entries.filter((entry) => entryAuthor(entry) === "user");
    if (!userEntries.length) {
      return "Forked entry";
    }
    const candidate =
      userIndex !== undefined && userIndex >= 0 && userIndex < userEntries.length
        ? userEntries[userIndex]
        : userEntries[userEntries.length - 1];
    return entryText(candidate);
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
      const key = forkPointKey(fork.forkedAtConversationId, fork.forkedAtEntryId ?? null);
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
    return forkPointKey(currentMeta.forkedAtConversationId, currentMeta.forkedAtEntryId ?? null);
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
        forkedAtEntryId: fork.forkedAtEntryId ?? null,
        createdAt: fork.createdAt ?? null,
        label: fork.title ?? undefined,
      }));

      if (showParentEntry && currentMeta?.forkedAtConversationId) {
        options.push({
          conversationId: currentMeta.forkedAtConversationId,
          forkedAtConversationId: null,
          forkedAtEntryId: currentMeta.forkedAtEntryId ?? null,
        });
      }

      if (conversationId && (forksAtPoint.length > 0 || showParentEntry)) {
        options.push({
          conversationId,
          forkedAtConversationId: currentMeta?.forkedAtConversationId ?? null,
          forkedAtEntryId: currentMeta?.forkedAtEntryId ?? null,
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
          ? (messageMeta.forkedAtEntryId ?? null)
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

  const handleCopy = useCallback((messageId: string, content: string) => {
    void navigator.clipboard.writeText(content).then(() => {
      setCopiedMessageId(messageId);
      setTimeout(() => setCopiedMessageId(null), 2000);
    });
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
      const response = (await ConversationsService.forkConversationAtEntry({
        conversationId: editingMessage.conversationId,
        entryId: editingMessage.id,
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

  // Readers can only view conversations, not send messages or edit
  const isReader = conversationQuery.data?.accessLevel === "reader";
  const composerDisabled = isBusy || forking || canceling || isReader;

  // Get conversation title and start time
  const conversationTitle = conversationQuery.data?.title || "New conversation";
  const conversationStartTime = conversationQuery.data?.createdAt
    ? formatConversationTime(conversationQuery.data.createdAt)
    : "";

  return (
    <main className="flex flex-1 flex-col bg-cream">
      {/* Chat Header */}
      <header className="relative z-40 border-b border-stone/10 bg-cream/80 px-8 py-5 backdrop-blur-sm">
        <div className="mx-auto flex max-w-3xl items-center justify-between">
          <div>
            <h2 className="font-serif text-xl">
              {messages.length === 0 ? <span className="text-stone/60">New conversation</span> : conversationTitle}
            </h2>
            <p className="mt-0.5 text-sm text-stone">
              {messages.length === 0
                ? "Start chatting with your agent"
                : conversationStartTime
                  ? `Started ${conversationStartTime}`
                  : ""}
            </p>
          </div>
          {/* Header actions */}
          <div className="flex items-center gap-2">
            {/* Share button */}
            {messages.length > 0 && currentUserId && (
              <ShareButton
                conversationId={conversationId}
                conversationTitle={conversationTitle}
                currentUserId={currentUserId}
                disabled={!conversationId}
              />
            )}

            {/* Conversation menu */}
            <div className="relative">
              <button
                type="button"
                onClick={() => setMenuOpen(!menuOpen)}
                className="rounded-lg p-2 text-stone transition-colors hover:bg-mist hover:text-ink"
                aria-label="Conversation menu"
              >
                <Menu className="h-5 w-5" />
              </button>
              {menuOpen && (
                <div className="absolute right-0 top-full z-50 mt-2 w-48 animate-slide-up overflow-hidden rounded-xl border border-stone/20 bg-cream shadow-xl">
                  <div className="py-1">
                    <button
                      type="button"
                      onClick={() => {
                        if (conversationId) {
                          onIndexConversation?.(conversationId);
                        }
                        setMenuOpen(false);
                      }}
                      disabled={!conversationId || messages.length === 0}
                      className="flex w-full items-center gap-3 px-4 py-3 text-left text-sm text-ink transition-colors hover:bg-mist disabled:opacity-50"
                    >
                      <Sparkles className="h-4 w-4 text-sage" />
                      Index conversation
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        if (conversationId) {
                          onDeleteConversation?.(conversationId);
                        }
                        setMenuOpen(false);
                      }}
                      disabled={!conversationId || messages.length === 0}
                      className="flex w-full items-center gap-3 px-4 py-3 text-left text-sm text-ink transition-colors hover:bg-mist disabled:opacity-50"
                    >
                      <Trash2 className="h-4 w-4 text-terracotta" />
                      Delete conversation
                    </button>
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* Stream mode toggle - hidden for now
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-stone">Stream</span>
            <div className="flex rounded-lg bg-mist p-0.5">
              <button
                type="button"
                onClick={() => setStreamMode("sse")}
                className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                  streamMode === "sse" ? "bg-cream text-ink shadow-sm" : "text-stone hover:text-ink"
                }`}
              >
                SSE
              </button>
              <button
                type="button"
                onClick={() => setStreamMode("websocket")}
                className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                  streamMode === "websocket" ? "bg-cream text-ink shadow-sm" : "text-stone hover:text-ink"
                }`}
              >
                WS
              </button>
            </div>
          </div>
          */}
        </div>
      </header>

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
                      {/* Gradient fade background */}
                      <div className="pointer-events-none absolute left-0 top-0 z-0 w-full">
                        <div className="h-8 bg-cream" />
                        <div className="h-16 bg-gradient-to-b from-cream via-cream/85 to-cream/0" />
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
                            onCopy={(content) => handleCopy(turn.user!.id, content)}
                            copiedMessageId={copiedMessageId}
                            composerDisabled={composerDisabled}
                            isReader={isReader}
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
                  <div className="flex flex-col gap-3 pb-6">
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
                          onCopy={(content) => handleCopy(message.id, content)}
                          copiedMessageId={copiedMessageId}
                          composerDisabled={composerDisabled}
                          isReader={isReader}
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

      {isReader ? (
        <div className="border-t border-stone/10 bg-cream px-8 py-5">
          <div className="mx-auto max-w-3xl">
            <div className="rounded-2xl bg-mist px-5 py-4 text-center text-sm text-stone">
              You have read-only access to this conversation
            </div>
          </div>
        </div>
      ) : (
        <ConversationsUI.Composer
          disabled={!conversationId || composerDisabled || Boolean(editingMessage)}
          cancelDisabled={canceling}
          sendLabel="Send"
          stopLabel="Stop"
        />
      )}
    </main>
  );
}

export function ChatPanel({
  conversationId,
  onSelectConversationId,
  knownConversationIds,
  resumableConversationIds,
  onIndexConversation,
  onDeleteConversation,
  currentUserId,
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
      .map((fork) => `${fork.conversationId ?? ""}:${fork.forkedAtConversationId ?? ""}:${fork.forkedAtEntryId ?? ""}`)
      .sort()
      .join("|");
  }, [forksQuery.data]);

  const entriesQuery = useQuery<Entry[], ApiError, Entry[]>({
    queryKey: ["conversation-path-messages", conversationId, forkFingerprint],
    enabled: Boolean(conversationId && conversationQuery.data && forksQuery.data),
    queryFn: async (): Promise<Entry[]> => {
      const currentConversation = conversationQuery.data!;
      const forks = forksQuery.data ?? [];
      const forkMetaById = new Map<
        string,
        { forkedAtConversationId?: string | null; forkedAtEntryId?: string | null }
      >();

      forks.forEach((fork) => {
        if (!fork.conversationId) {
          return;
        }
        forkMetaById.set(fork.conversationId, {
          forkedAtConversationId: fork.forkedAtConversationId ?? null,
          forkedAtEntryId: fork.forkedAtEntryId ?? null,
        });
      });

      forkMetaById.set(conversationId!, {
        forkedAtConversationId: currentConversation.forkedAtConversationId ?? null,
        forkedAtEntryId: currentConversation.forkedAtEntryId ?? null,
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

      const entriesByConversation = new Map<string, Entry[]>();
      await Promise.all(
        lineage.map(async (id) => {
          const response = (await ConversationsService.listConversationEntries({
            conversationId: id,
            limit: 200,
            channel: "history",
          })) as unknown as ListUserEntriesResponse;
          entriesByConversation.set(id, Array.isArray(response.data) ? response.data : []);
        }),
      );

      const combined: Entry[] = [];
      for (let i = 0; i < lineage.length; i += 1) {
        const conversationEntries = entriesByConversation.get(lineage[i]) ?? [];
        const childId = lineage[i + 1];
        if (!childId) {
          combined.push(...conversationEntries);
          continue;
        }
        const childMeta = forkMetaById.get(childId);
        if (childMeta && childMeta.forkedAtEntryId === null) {
          continue;
        }
        if (childMeta?.forkedAtEntryId) {
          const forkedIndex = conversationEntries.findIndex((entry) => entry.id === childMeta.forkedAtEntryId);
          if (forkedIndex >= 0) {
            combined.push(...conversationEntries.slice(0, forkedIndex + 1));
            continue;
          }
        }
        combined.push(...conversationEntries);
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
        forkedAtEntryId: fork.forkedAtEntryId ?? null,
      });
    }
    if (conversationId && conversationQuery.data) {
      map.set(conversationId, {
        forkedAtConversationId: conversationQuery.data.forkedAtConversationId ?? null,
        forkedAtEntryId: conversationQuery.data.forkedAtEntryId ?? null,
      });
    }
    return map;
  }, [conversationId, conversationQuery.data, forksQuery.data]);

  useEffect(() => {
    const entries = entriesQuery.data ?? [];
    if (!entries.length) {
      return;
    }
    const lastAssistantByConversation = new Map<string, string>();
    entries.forEach((entry) => {
      if (!entry.id || !entry.conversationId) {
        return;
      }
      if (entryAuthor(entry) !== "assistant") {
        return;
      }
      lastAssistantByConversation.set(entry.conversationId, entry.id);
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
  }, [entriesQuery.data]);

  const conversationMessages = useMemo<ConversationMessage[]>(() => {
    const entries = entriesQuery.data ?? [];
    const firstIndexByConversation = new Map<string, number>();
    const mapped: ConversationMessage[] = [];

    entries.forEach((entry) => {
      if (!entry.id || !entry.conversationId) {
        return;
      }
      const author = entryAuthor(entry);
      const resolvedId = author === "assistant" ? (assistantIdOverrides[entry.id] ?? entry.id) : entry.id;
      if (!firstIndexByConversation.has(entry.conversationId)) {
        firstIndexByConversation.set(entry.conversationId, mapped.length);
      }
      mapped.push({
        id: resolvedId,
        conversationId: entry.conversationId,
        author,
        content: entryText(entry),
        createdAt: entry.createdAt,
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
          messageId: meta.forkedAtEntryId ?? null,
        },
      };
    });
  }, [assistantIdOverrides, conversationMetaById, entriesQuery.data]);

  // conversationGroupId is now hidden from the API; use conversationId for state tracking
  const conversationGroupId = conversationId;

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
        startEventStream(targetConversationId, text, true, callbacks);
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
        startEventStream(targetConversationId, "", false, callbacks);
      },
      cancelStream: async (targetConversationId) => {
        if (!targetConversationId) {
          return;
        }
        setCanceling(true);
        try {
          await ConversationsService.deleteConversationResponse({ conversationId: targetConversationId });
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
        conversationQuery={conversationQuery}
        streamMode={streamMode}
        setStreamMode={setStreamMode}
        onIndexConversation={onIndexConversation}
        onDeleteConversation={onDeleteConversation}
        currentUserId={currentUserId}
      />
    </Conversation.Root>
  );
}
