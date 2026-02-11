import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type React from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Conversation,
  type AttachmentRef,
  type ChatAttachment,
  type ChatEvent,
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
import { useSseStream } from "@/hooks/useSseStream";
import type { StreamAttachmentRef, StreamStartParams } from "@/hooks/useStreamTypes";
import { Check, Copy, Menu, Paperclip, Pencil, Trash2 } from "lucide-react";
import { useAttachments } from "@/hooks/useAttachments";
import { ShareButton } from "@/components/sharing";
import { UserAvatar } from "@/components/user-avatar";
import type { AuthUser } from "@/lib/auth";
import { createForkView, type EntryAndForkInfo, type ForkOption } from "@/lib/conversation";

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

type ChatPanelProps = {
  conversationId: string | null;
  onSelectConversationId?: (conversationId: string) => void;
  resumableConversationIds?: Set<string>;
  knownConversationIds?: Set<string>;
  onDeleteConversation?: (conversationId: string) => void;
  currentUserId?: string | null;
  currentUser?: AuthUser | null;
};

type PendingFork = {
  conversationId: string;
  message: string;
  attachments?: AttachmentRef[];
};

type ConversationMeta = {
  forkedAtConversationId: string | null;
  forkedAtEntryId: string | null;
};

type ChatMessageRowProps = {
  message: RenderableConversationMessage;
  isEditing: boolean;
  editingText: string;
  onEditingTextChange: (value: string) => void;
  onEditStart: (message: ConversationMessage) => void;
  onEditCancel: () => void;
  onForkSend: (attachments?: AttachmentRef[]) => void;
  onCopy: (content: string) => void;
  copiedMessageId: string | null;
  composerDisabled: boolean;
  isReader: boolean;
  conversationId: string | null;
  forkOptionsCount: number;
  setActiveForkMenuMessageId: (id: string | null) => void;
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
  setActiveForkMenuMessageId,
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
  const forkMenuRef = useRef<HTMLDivElement>(null);
  const editFileInputRef = useRef<HTMLInputElement>(null);
  const { attachments: editAttachments, addFiles: editAddFiles, preloadExisting: editPreloadExisting, removeAttachment: editRemoveAttachment, clearAll: editClearAll } = useAttachments();

  // When entering edit mode, pre-populate with the message's existing attachments
  const prevEditingRef = useRef(false);
  useEffect(() => {
    if (isEditing && !prevEditingRef.current && message.attachments && message.attachments.length > 0) {
      const existing = message.attachments
        .filter((a) => a.href?.startsWith("/v1/attachments/"))
        .map((a) => ({
          attachmentId: a.href!.replace("/v1/attachments/", ""),
          contentType: a.contentType,
          name: a.name,
        }));
      if (existing.length > 0) {
        editPreloadExisting(existing);
      }
    }
    if (!isEditing && prevEditingRef.current) {
      editClearAll();
    }
    prevEditingRef.current = isEditing;
  }, [isEditing, message.attachments, editPreloadExisting, editClearAll]);

  // Close fork menu when clicking outside
  useEffect(() => {
    if (!isForkMenuOpen) {
      return;
    }
    const handleClickOutside = (event: MouseEvent) => {
      if (forkMenuRef.current && !forkMenuRef.current.contains(event.target as Node)) {
        setOpenForkMenuMessageId(null);
        setActiveForkMenuMessageId(null);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
    };
  }, [isForkMenuOpen, setOpenForkMenuMessageId, setActiveForkMenuMessageId]);

  if (isEditing) {
    const handleEditCancel = () => {
      editClearAll();
      onEditCancel();
    };

    return (
      <div key={message.id} className={`flex ${isUser ? "justify-end" : "justify-start"}`}>
        <div className={`w-full max-w-[75%] ${isUser ? "" : ""}`}>
          {/* Edit mode with drag-and-drop */}
          <div
            className="edit-glow rounded-2xl border-2 border-sage bg-cream px-5 py-4"
            onDragOver={(e) => { e.preventDefault(); e.stopPropagation(); }}
            onDrop={(e) => {
              e.preventDefault();
              e.stopPropagation();
              if (e.dataTransfer.files.length > 0) {
                editAddFiles(e.dataTransfer.files);
              }
            }}
          >
            {/* Attachment strip for edit mode */}
            {editAttachments.length > 0 && (
              <div className="mb-2 flex flex-wrap gap-1.5">
                {editAttachments.map((att) => (
                  <ConversationsUI.AttachmentChip key={att.localId} attachment={att} onRemove={editRemoveAttachment} />
                ))}
              </div>
            )}
            <textarea
              value={editingText}
              onChange={(event) => onEditingTextChange(event.target.value)}
              rows={3}
              className="w-full resize-none bg-transparent text-[15px] leading-relaxed text-ink focus:outline-none"
            />
            <div className="mt-3 flex items-center justify-end gap-2 border-t border-stone/10 pt-3">
              <button
                type="button"
                onClick={() => editFileInputRef.current?.click()}
                className="rounded-lg p-2 text-stone/60 transition-colors hover:bg-black/5 hover:text-ink"
                title="Attach files"
              >
                <Paperclip className="h-4 w-4" />
              </button>
              <input
                ref={editFileInputRef}
                type="file"
                multiple
                className="hidden"
                onChange={(e) => {
                  if (e.target.files && e.target.files.length > 0) {
                    editAddFiles(e.target.files);
                  }
                  e.target.value = "";
                }}
              />
              <div className="flex-1" />
              <button
                type="button"
                onClick={handleEditCancel}
                disabled={composerDisabled}
                className="px-4 py-2 text-sm text-stone transition-colors hover:text-ink disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => {
                  const refs = editAttachments
                    .filter((a) => a.status === "uploaded" && a.attachmentId)
                    .map((a) => ({ attachmentId: a.attachmentId!, contentType: a.contentType, name: a.name }));
                  onForkSend(refs.length > 0 ? refs : undefined);
                }}
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
      {hasForks ? (
        <div className={`relative mb-1 ${isUser ? "pr-1 text-right" : "pl-1"}`}>
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
            <div
              ref={forkMenuRef}
              className={`absolute z-30 mt-1 w-72 animate-slide-up overflow-hidden rounded-xl border border-stone/20 bg-cream shadow-xl ${
                isUser ? "right-0" : "left-0"
              }`}
            >
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
                      const label = fork.label || (isActive ? message.content : "Fork message");
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
                  </>
                )}
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
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
    </div>
  );
}

type EntryContent = {
  text: string;
  events?: ChatEvent[];
  attachments?: ChatAttachment[];
};

function entryContent(entry: Entry): EntryContent {
  const blocks = entry.content ?? [];
  const contentBlock = blocks.find((b) => {
    const block = b as { [key: string]: unknown } | undefined;
    return block && (typeof block.text === "string" || Array.isArray(block.events) || Array.isArray(block.attachments));
  }) as { text?: string; events?: ChatEvent[]; attachments?: ChatAttachment[] } | undefined;

  return {
    text: contentBlock?.text ?? "",
    events: contentBlock?.events,
    attachments: contentBlock?.attachments,
  };
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
  canceling: boolean;
  onDeleteConversation?: (conversationId: string) => void;
  currentUserId?: string | null;
  currentUser?: AuthUser | null;
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
  canceling,
  forksQuery,
  conversationQuery,
  entriesWithForks,
  onDeleteConversation,
  currentUserId,
  currentUser,
}: ChatPanelContentProps & {
  forksQuery: ReturnType<typeof useQuery<ConversationForkSummary[], ApiError, ConversationForkSummary[]>>;
  conversationQuery: ReturnType<typeof useQuery<ApiConversation, ApiError, ApiConversation>>;
  entriesWithForks: EntryAndForkInfo[];
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const { messages } = useConversationMessages();
  const { resumeStream, isBusy } = useConversationStreaming();
  const { submit } = useConversationInput();
  const [forking, setForking] = useState(false);
  const [editingMessage, setEditingMessage] = useState<{ id: string; conversationId: string } | null>(null);
  const [editingText, setEditingText] = useState("");
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null);
  const [, setActiveForkMenuMessageId] = useState<string | null>(null);
  const [openForkMenuMessageId, setOpenForkMenuMessageId] = useState<string | null>(null);

  // Scroll management refs
  const viewportRef = useRef<HTMLDivElement>(null);
  const messageRefs = useRef<Map<string, HTMLDivElement>>(new Map());
  const isNearBottomRef = useRef(true);
  const shouldAutoScrollRef = useRef(true);
  const lastMessageCountRef = useRef(0);
  const isInitialLoadRef = useRef(true);
  const lastScrollTopRef = useRef(0);
  const isProgrammaticScrollRef = useRef(false);

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

  const formatForkLabel = (text: string) => {
    const trimmed = text.trim();
    if (!trimmed) {
      return "Forked message";
    }
    return trimmed.length <= 60 ? trimmed : `${trimmed.slice(0, 57)}...`;
  };

  // Get the current conversationId from the conversation context to check if state is synced
  const { conversationId: stateConversationId } = useConversationMessages();

  useEffect(() => {
    // Early exit checks (no logging to reduce noise)
    if (!conversationId || !isResolvedConversation) {
      return;
    }
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
      resumeStream();
    }
  }, [
    conversationId,
    stateConversationId,
    forking,
    hasResumedRef,
    isBusy,
    isResolvedConversation,
    messages,
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
    const forkAttachments = pending.attachments;
    pendingForkRef.current = null;
    if (!trimmed) {
      return;
    }
    submit(trimmed, forkAttachments);
  }, [conversationId, stateConversationId, forking, isBusy, pendingForkRef, submit]);

  useEffect(() => {
    setEditingMessage(null);
    setEditingText("");
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

  // Build fork options map from entriesWithForks (from createForkView)
  const forkOptionsByMessageId = useMemo(() => {
    const map = new Map<string, ForkOption[]>();
    entriesWithForks.forEach(({ entry, forks }) => {
      map.set(entry.id, forks ?? []);
    });
    return map;
  }, [entriesWithForks]);

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

      // Mark as programmatic scroll to prevent handleScroll from disabling auto-scroll
      isProgrammaticScrollRef.current = true;

      // Try to scroll to the last message element if available
      if (messages.length > 0) {
        const lastMessage = messages[messages.length - 1];
        const lastMessageElement = messageRefs.current.get(lastMessage.id);
        if (lastMessageElement) {
          lastMessageElement.scrollIntoView({ behavior, block: "end" });
          // Reset programmatic flag after scroll completes
          requestAnimationFrame(() => {
            isProgrammaticScrollRef.current = false;
            lastScrollTopRef.current = viewport.scrollTop;
          });
          return;
        }
      }

      // Fallback to scrolling to bottom of viewport
      viewport.scrollTo({
        top: viewport.scrollHeight,
        behavior,
      });
      // Reset programmatic flag after scroll completes
      requestAnimationFrame(() => {
        isProgrammaticScrollRef.current = false;
        lastScrollTopRef.current = viewport.scrollTop;
      });
    },
    [messages],
  );

  // Handle scroll events to track if user is near bottom
  const handleScroll = useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport) {
      return;
    }

    const currentScrollTop = viewport.scrollTop;
    const wasNearBottom = isNearBottomRef.current;
    isNearBottomRef.current = checkNearBottom();

    // Skip if this is a programmatic scroll
    if (isProgrammaticScrollRef.current) {
      lastScrollTopRef.current = currentScrollTop;
      return;
    }

    // Detect if user explicitly scrolled UP (away from bottom)
    // Only disable auto-scroll if user scrolled up significantly (more than 50px)
    const scrolledUp = currentScrollTop < lastScrollTopRef.current - 50;

    if (scrolledUp && !isNearBottomRef.current) {
      // User scrolled up away from bottom - disable auto-scroll
      shouldAutoScrollRef.current = false;
    } else if (isNearBottomRef.current && !wasNearBottom) {
      // User scrolled back near bottom - re-enable auto-scroll
      shouldAutoScrollRef.current = true;
    }

    lastScrollTopRef.current = currentScrollTop;
  }, [checkNearBottom]);

  // Scroll to bottom on initial load
  useLayoutEffect(() => {
    if (!isResolvedConversation || !conversationId) {
      return;
    }
    if (messages.length > 0 && isInitialLoadRef.current) {
      isInitialLoadRef.current = false;
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
    lastScrollTopRef.current = 0;
    isProgrammaticScrollRef.current = false;
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
    const hasPendingMessage = messages.some((msg) => msg.displayState === "pending");

    // If this is the first time messages appear (initial load), force scroll to bottom
    if (wasEmpty && isInitialLoadRef.current && viewport.scrollHeight > viewport.clientHeight) {
      isInitialLoadRef.current = false;
      requestAnimationFrame(() => {
        scrollToBottom("instant");
        shouldAutoScrollRef.current = true;
        isNearBottomRef.current = true;
        lastScrollTopRef.current = viewport.scrollTop;
      });
      lastMessageCountRef.current = messages.length;
      return;
    }

    // If user has explicitly disabled auto-scroll by scrolling up, don't auto-scroll
    if (!shouldAutoScrollRef.current) {
      lastMessageCountRef.current = messages.length;
      return;
    }

    // Auto-scroll when:
    // 1. New messages are added (messageCountChanged)
    // 2. Content is streaming (hasStreamingMessage)
    // 3. Message is pending/being sent (hasPendingMessage)
    if (messageCountChanged || hasStreamingMessage || hasPendingMessage) {
      requestAnimationFrame(() => {
        scrollToBottom(hasStreamingMessage ? "instant" : "smooth");
      });
    }

    lastMessageCountRef.current = messages.length;
  }, [messages, scrollToBottom]);

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

  const handleForkSend = useCallback(async (attachments?: AttachmentRef[]) => {
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
        attachments,
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
    <main className="flex flex-1 flex-col overflow-hidden bg-cream">
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

            {/* User avatar */}
            {currentUser && <UserAvatar user={currentUser} />}
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
                            setActiveForkMenuMessageId={setActiveForkMenuMessageId}
                            formatForkLabel={formatForkLabel}
                            formatForkTimestamp={formatForkTimestamp}
                            forkPoint={openForkMenuMessageId === turn.user.id ? forkPoint : null}
                            forkOptions={
                              openForkMenuMessageId === turn.user.id
                                ? (forkOptionsByMessageId.get(turn.user.id) ?? [])
                                : []
                            }
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
                          setActiveForkMenuMessageId={setActiveForkMenuMessageId}
                          formatForkLabel={formatForkLabel}
                          formatForkTimestamp={formatForkTimestamp}
                          forkPoint={openForkMenuMessageId === message.id ? forkPoint : null}
                          forkOptions={
                            openForkMenuMessageId === message.id ? (forkOptionsByMessageId.get(message.id) ?? []) : []
                          }
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
  onDeleteConversation,
  currentUserId,
  currentUser,
}: ChatPanelProps) {
  const [canceling, setCanceling] = useState(false);
  const firstChunkEmittedRef = useRef<Record<string, boolean>>({});
  const hasResumedRef = useRef<Record<string, boolean>>({});
  const pendingForkRef = useRef<PendingFork | null>(null);

  const sseStream = useSseStream();
  const queryClient = useQueryClient();

  // Reset resume tracking when switching conversations
  const previousConversationIdRef = useRef<string | null>(null);
  useLayoutEffect(() => {
    if (previousConversationIdRef.current !== conversationId) {
      const previousId = previousConversationIdRef.current;
      if (previousId) {
        delete hasResumedRef.current[previousId];
      }
      previousConversationIdRef.current = conversationId;
      if (conversationId) {
        // Reset resume flag when switching TO a conversation to allow resume on switch back
        delete hasResumedRef.current[conversationId];
      }
      sseStream.close();
    }
  }, [conversationId, sseStream]);

  // Reset hasResumed flag when resumableConversationIds changes to allow retry
  // This handles the case where the resume check query completes after the effect first runs
  useEffect(() => {
    // Effect intentionally empty - the auto-resume effect will re-run when resumableConversationIds changes
  }, [conversationId, resumableConversationIds]);

  useEffect(() => {
    return () => {
      sseStream.close();
    };
  }, [sseStream]);

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
      const response = (await ConversationsService.listConversationEntries({
        conversationId: conversationId!,
        limit: 200,
        channel: "history",
        forks: "all",
      })) as unknown as ListUserEntriesResponse;
      return Array.isArray(response.data) ? response.data : [];
    },
  });

  // Create ForkView from entries and forks data
  const forkView = useMemo(() => {
    const entries = entriesQuery.data ?? [];
    const forks = forksQuery.data ?? [];
    const forkView = createForkView(entries, forks);

    // // log forkView
    // for( const convoId of forkView.conversationIds()) {
    //   console.log(`Conversation ID: ${convoId}`, forkView.entries(convoId));
    // }

    return forkView;
  }, [entriesQuery.data, forksQuery.data]);

  // Get entries with fork info from the forkView
  const entriesWithForks = useMemo<EntryAndForkInfo[]>(() => {
    if (!forkView || !conversationId) {
      return [];
    }
    return forkView.entries(conversationId);
  }, [conversationId, forkView]);

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

  const conversationMessages = useMemo<ConversationMessage[]>(() => {
    const firstIndexByConversation = new Map<string, number>();
    const mapped: ConversationMessage[] = [];

    entriesWithForks.forEach(({ entry }) => {
      if (!entry.id || !entry.conversationId) {
        return;
      }
      const author = entryAuthor(entry);
      if (!firstIndexByConversation.has(entry.conversationId)) {
        firstIndexByConversation.set(entry.conversationId, mapped.length);
      }
      const { text, events, attachments } = entryContent(entry);
      mapped.push({
        id: entry.id,
        conversationId: entry.conversationId,
        author,
        content: text,
        createdAt: entry.createdAt,
        userId: entry.userId,
        events,
        attachments,
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
  }, [conversationMetaById, entriesWithForks]);

  // conversationGroupId is now hidden from the API; use conversationId for state tracking
  const conversationGroupId = conversationId;

  const startEventStream = useCallback(
    (
      targetConversationId: string,
      text: string,
      resetResume: boolean,
      callbacks: {
        onChunk?: (chunk: string) => void;
        onEvent?: (event: ChatEvent) => void;
        onComplete?: () => void;
        onError?: (error: unknown) => void;
      },
      attachments?: StreamAttachmentRef[],
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
      const handleEvent = (event: ChatEvent) => {
        callbacks.onEvent?.(event);
        // Invalidate queries on first event as well
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
        attachments,
        onChunk: appendAssistantChunk,
        onEvent: handleEvent,
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

      sseStream.close();
      try {
        sseStream.start(params);
      } catch (error) {
        callbacks.onError?.(error);
      }
    },
    [queryClient, sseStream],
  );

  // Optimistically mark a conversation as having an in-progress response
  const markConversationAsStreaming = useCallback(
    (targetConversationId: string) => {
      // Add this conversation to all resume-check query results optimistically
      queryClient.setQueriesData<string[]>({ queryKey: ["resume-check"] }, (old) => {
        if (!old) return [targetConversationId];
        if (old.includes(targetConversationId)) return old;
        return [...old, targetConversationId];
      });
    },
    [queryClient],
  );

  const controller = useMemo<ConversationController>(
    () => ({
      startStream: async (targetConversationId, text, callbacks, attachments) => {
        markConversationAsStreaming(targetConversationId);
        const streamAttachments = attachments?.map((a) => ({ attachmentId: a.attachmentId }));
        startEventStream(targetConversationId, text, true, callbacks, streamAttachments);
      },
      resumeStream: async (targetConversationId, callbacks) => {
        markConversationAsStreaming(targetConversationId);
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
        sseStream.close();
        void queryClient.invalidateQueries({ queryKey: ["conversation-path-messages", targetConversationId] });
        void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
      },
      selectConversation: async (id) => {
        onSelectConversationId?.(id);
      },
    }),
    [markConversationAsStreaming, onSelectConversationId, queryClient, sseStream, startEventStream],
  );

  return (
    <Conversation.Root
      controller={controller}
      conversationId={conversationId}
      conversationGroupId={conversationGroupId}
      messages={conversationMessages}
      currentUserId={currentUserId}
    >
      <ChatPanelContent
        conversationId={conversationId}
        isResolvedConversation={isResolvedConversation}
        resumableConversationIds={resumableConversationIds}
        pendingForkRef={pendingForkRef}
        hasResumedRef={hasResumedRef}
        onSelectConversationId={onSelectConversationId}
        queryClient={queryClient}
        canceling={canceling}
        forksQuery={forksQuery}
        conversationQuery={conversationQuery}
        entriesWithForks={entriesWithForks}
        onDeleteConversation={onDeleteConversation}
        currentUserId={currentUserId}
        currentUser={currentUser}
      />
    </Conversation.Root>
  );
}
