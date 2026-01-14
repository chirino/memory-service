import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Streamdown } from "streamdown";
import type { ApiError, Conversation, ConversationForkSummary, Message } from "@/client";
import { ConversationsService } from "@/client";
import { useWebSocketStream } from "@/hooks/useWebSocketStream";
import type { StreamStartParams } from "@/hooks/useStreamTypes";

type ChatMessage = {
  id: string;
  author: "user" | "assistant";
  content: string;
  raw?: Message;
};

const START_ANCHOR = "__start__";

type ListUserMessagesResponse = {
  data?: Message[];
  nextCursor?: string | null;
};

type ListConversationForksResponse = {
  data?: ConversationForkSummary[];
};

type ChatPanelProps = {
  conversationId: string | null;
  onSelectConversationId?: (conversationId: string) => void;
  resumableConversationIds?: Set<string>;
  knownConversationIds?: Set<string>;
};

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

function buildForkPointKey(conversationId: string | null | undefined, previousMessageId: string | null): string | null {
  if (!conversationId) {
    return null;
  }
  return `${conversationId}:${previousMessageId ?? START_ANCHOR}`;
}

export function ChatPanel({ conversationId, onSelectConversationId, knownConversationIds }: ChatPanelProps) {
  const [streamingUpdates, setStreamingUpdates] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [forking, setForking] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const [editingMessage, setEditingMessage] = useState<{ id: string; conversationId: string } | null>(null);
  const [editingText, setEditingText] = useState("");
  const [openForkMenuMessageKey, setOpenForkMenuMessageKey] = useState<string | null>(null);
  const [openForkMenuMessageId, setOpenForkMenuMessageId] = useState<string | null>(null);
  const [forkLabels, setForkLabels] = useState<Record<string, string>>({});
  const firstChunkEmittedRef = useRef<Record<string, boolean>>({});
  const hasResumedRef = useRef<Record<string, boolean>>({});
  const streamingConversationRef = useRef<string | null>(null);
  const pendingSendRef = useRef<{
    id: string;
    conversationId: string;
    content: string;
    afterId: string | null;
  } | null>(null);
  const startEventStreamRef = useRef<
    | ((
        targetConversationId: string,
        text: string,
        resumePosition?: number,
        resetResume?: boolean,
        reason?: string,
      ) => void)
    | null
  >(null);
  const pendingForkRef = useRef<{ conversationId: string; message: string } | null>(null);
  const webSocketStream = useWebSocketStream();
  const queryClient = useQueryClient();

  // Reset hasResumedRef when conversationId changes to ensure we can resume when switching back
  // Use useLayoutEffect to clear synchronously before other effects run
  const previousConversationIdRef = useRef<string | null>(null);
  useLayoutEffect(() => {
    if (previousConversationIdRef.current !== conversationId) {
      // Conversation changed - clear the resume flag for the previous conversation
      if (previousConversationIdRef.current) {
        delete hasResumedRef.current[previousConversationIdRef.current];
      }
      previousConversationIdRef.current = conversationId;
      // Also clear for the new conversation to ensure we can resume
      if (conversationId) {
        delete hasResumedRef.current[conversationId];
      }
    }
  }, [conversationId]);

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

  const conversationQuery = useQuery<Conversation, ApiError, Conversation>({
    queryKey: ["conversation", conversationId],
    enabled: isResolvedConversation,
    queryFn: async (): Promise<Conversation> => {
      const convo = (await ConversationsService.getConversation({
        conversationId: conversationId!,
      })) as Conversation;
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

  const baseMessages = useMemo<ChatMessage[]>(() => {
    const messages = messagesQuery.data ?? [];
    return messages
      .filter((msg) => !!msg.id)
      .map((msg) => ({
        id: msg.id!,
        author: messageAuthor(msg),
        content: messageText(msg),
        raw: msg,
      }));
  }, [messagesQuery.data]);

  useEffect(() => {
    if (!conversationId) {
      return;
    }
    const messages = messagesQuery.data ?? [];
    if (messages.length === 0) {
      return;
    }
    const pending = pendingSendRef.current;
    if (!pending || pending.conversationId !== conversationId) {
      return;
    }
    const anchorIndex = pending.afterId ? messages.findIndex((msg) => msg.id === pending.afterId) : -1;
    const candidates = anchorIndex >= 0 ? messages.slice(anchorIndex + 1) : messages;
    const match = candidates.find(
      (msg) => messageAuthor(msg) === "user" && messageText(msg).trim() === pending.content,
    );
    if (!match) {
      return;
    }
    setStreamingUpdates((prev) => prev.filter((msg) => msg.id !== pending.id));
    pendingSendRef.current = null;
  }, [conversationId, messagesQuery.data]);

  const conversationMetaById = useMemo(() => {
    const map = new Map<string, { forkedAtConversationId: string | null; forkedAtMessageId: string | null }>();
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

  const conversationMessagesByConversation = useMemo(() => {
    const map = new Map<string, ChatMessage[]>();
    for (const message of baseMessages) {
      const conversationKey = message.raw?.conversationId;
      if (!conversationKey) {
        continue;
      }
      const list = map.get(conversationKey) ?? [];
      list.push(message);
      map.set(conversationKey, list);
    }
    return map;
  }, [baseMessages]);

  const previousMessageId = useCallback(
    (conversationKey: string, messageId: string) => {
      const list = conversationMessagesByConversation.get(conversationKey);
      if (!list) {
        return null;
      }
      const index = list.findIndex((message) => message.id === messageId);
      if (index <= 0) {
        return null;
      }
      return list[index - 1]?.id ?? null;
    },
    [conversationMessagesByConversation],
  );

  const forksByPointKey = useMemo(() => {
    const grouped: Record<string, ConversationForkSummary[]> = {};
    for (const fork of forksQuery.data ?? []) {
      if (!fork.forkedAtConversationId || !fork.conversationId) {
        continue;
      }
      const key = buildForkPointKey(fork.forkedAtConversationId, fork.forkedAtMessageId ?? null);
      if (!key) {
        continue;
      }
      grouped[key] = grouped[key] ?? [];
      grouped[key].push(fork);
    }
    return grouped;
  }, [conversationId, conversationMessagesByConversation, forksQuery.data]);

  const forkedAtMessageId = conversationQuery.data?.forkedAtMessageId ?? null;
  const forkedAtConversationId = conversationQuery.data?.forkedAtConversationId ?? null;
  const conversationGroupId = conversationQuery.data?.conversationGroupId ?? null;
  const currentForkPointKey = useMemo(() => {
    if (!forkedAtConversationId) {
      return null;
    }
    const key = buildForkPointKey(forkedAtConversationId, forkedAtMessageId ?? null);
    return key;
  }, [conversationId, forkedAtConversationId, forkedAtMessageId]);
  const userMessageIndexById = useMemo(() => {
    const indexById = new Map<string, number>();
    let index = 0;
    baseMessages.forEach((message) => {
      if (message.author !== "user") {
        return;
      }
      indexById.set(message.id, index);
      index += 1;
    });
    return indexById;
  }, [baseMessages]);

  const getForksAtPoint = useCallback(
    (conversationKey: string, previousId: string | null) => {
      const key = buildForkPointKey(conversationKey, previousId);
      if (!key) {
        return { key: null, forks: [] as ConversationForkSummary[] };
      }
      return { key, forks: forksByPointKey[key] ?? [] };
    },
    [forksByPointKey],
  );

  // Forks are grouped by the previous history message in the parent conversation.

  // Merge base messages from query with streaming updates.
  // Only replace the last assistant message when streaming starts with an assistant chunk
  // (resume replay). For new sends, keep history and append the new user + assistant stream.
  const displayedMessages = useMemo(() => {
    if (streamingUpdates.length === 0) {
      return baseMessages;
    }

    // If the last base message is from assistant and we have streaming updates,
    // replace it (streaming is replaying/updating it)
    if (baseMessages.length > 0) {
      const lastBase = baseMessages[baseMessages.length - 1];
      if (lastBase.author === "assistant" && streamingUpdates[0]?.author === "assistant") {
        return [...baseMessages.slice(0, -1), ...streamingUpdates];
      }
    }

    // Otherwise, append streaming updates
    return [...baseMessages, ...streamingUpdates];
  }, [baseMessages, streamingUpdates]);

  useEffect(() => {
    setStreamingUpdates([]);
    setInput("");
    setSending(false);
    setForking(false);
    setEditingMessage(null);
    setEditingText("");
    setOpenForkMenuMessageKey(null);
    firstChunkEmittedRef.current = {};
    streamingConversationRef.current = null;
    pendingSendRef.current = null;
    webSocketStream.close();

    if (!conversationId) {
      return;
    }

    const pendingFork = pendingForkRef.current;
    if (!pendingFork || pendingFork.conversationId !== conversationId) {
      return;
    }

    const trimmed = pendingFork.message.trim();
    pendingForkRef.current = null;
    if (!trimmed) {
      return;
    }
    const pendingUserId = `user-${Date.now()}`;
    pendingSendRef.current = {
      id: pendingUserId,
      conversationId,
      content: trimmed,
      afterId: null,
    };
    setStreamingUpdates([
      {
        id: pendingUserId,
        author: "user",
        content: trimmed,
      },
    ]);
    setSending(true);
    const startFn = startEventStreamRef.current;
    if (startFn) {
      startFn(conversationId, trimmed, 0, true);
    } else {
      streamingConversationRef.current = null;
      setSending(false);
    }
  }, [conversationId, webSocketStream]);

  // Cleanup: close WebSocket when component unmounts
  // This ensures the connection is closed if user navigates away or component is removed
  useEffect(() => {
    return () => {
      streamingConversationRef.current = null;
      webSocketStream.close();
    };
    // webSocketStream is stable (memoized), so we only need cleanup on unmount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const startEventStream = useCallback(
    (targetConversationId: string, text: string, resumePosition = 0, resetResume = false) => {
      streamingConversationRef.current = targetConversationId;
      setSending(true);

      const appendAssistantChunk = (chunk: string) => {
        if (!chunk.length) {
          return;
        }
        setStreamingUpdates((prev) => {
          // If we have a streaming assistant message, append to it
          if (prev.length > 0 && prev[prev.length - 1].author === "assistant") {
            const last = prev[prev.length - 1];
            return [
              ...prev.slice(0, -1),
              {
                ...last,
                content: last.content + chunk,
              },
            ];
          }
          // No existing streaming assistant message, add new one
          return [
            ...prev,
            {
              id: `assistant-${Date.now()}`,
              author: "assistant",
              content: chunk,
            },
          ];
        });
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
          // If replay fails, just refetch messages to get the current state
          streamingConversationRef.current = null;
          setSending(false);
          void queryClient.invalidateQueries({ queryKey: ["messages", targetConversationId] });
        },
        onCleanEnd: () => {
          // Stream ended cleanly, refetch messages and invalidate resume check
          streamingConversationRef.current = null;
          setSending(false);
          void queryClient.invalidateQueries({ queryKey: ["messages", targetConversationId] });
          void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
        },
      };

      webSocketStream.close();
      webSocketStream.start(params);
    },
    [webSocketStream, queryClient],
  );

  // Store the latest startEventStream in a ref so the effect can use it without depending on it
  startEventStreamRef.current = startEventStream;

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

  const handleEditStart = useCallback(
    (message: ChatMessage) => {
      const targetConversationId = message.raw?.conversationId ?? conversationId;
      if (!message.id || !targetConversationId) {
        return;
      }
      setEditingMessage({ id: message.id, conversationId: targetConversationId });
      setEditingText(message.content);
      setOpenForkMenuMessageKey(null);
      setOpenForkMenuMessageId(null);
    },
    [conversationId],
  );

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
      })) as Conversation;

      if (!response?.id) {
        return;
      }

      pendingForkRef.current = {
        conversationId: response.id,
        message: trimmed,
      };
      setEditingMessage(null);
      setEditingText("");
      setOpenForkMenuMessageKey(null);
      setOpenForkMenuMessageId(null);
      onSelectConversationId?.(response.id);
      void queryClient.invalidateQueries({ queryKey: ["conversations"] });
      void queryClient.invalidateQueries({ queryKey: ["conversation-forks", editingMessage.conversationId] });
    } catch (error) {
      void error;
    } finally {
      setForking(false);
    }
  }, [editingMessage, editingText, onSelectConversationId, queryClient]);

  const handleForkSelect = useCallback(
    (forkConversationId: string) => {
      if (!forkConversationId) {
        return;
      }
      setOpenForkMenuMessageKey(null);
      setOpenForkMenuMessageId(null);
      onSelectConversationId?.(forkConversationId);
    },
    [onSelectConversationId],
  );

  useEffect(() => {
    if (!openForkMenuMessageKey) {
      return;
    }
    const forks = forksByPointKey[openForkMenuMessageKey] ?? [];
    const parentEntryNeeded = Boolean(currentForkPointKey && openForkMenuMessageKey === currentForkPointKey);
    const forksWithParent =
      parentEntryNeeded && forkedAtConversationId
        ? [
            ...forks,
            {
              conversationId: forkedAtConversationId,
              forkedAtMessageId: forkedAtMessageId ?? undefined,
              forkedAtConversationId: undefined,
              conversationGroupId: conversationGroupId ?? undefined,
              createdAt: undefined,
              title: undefined,
            },
          ]
        : forks;
    const missing = forksWithParent.filter((fork) => fork.conversationId && !forkLabels[fork.conversationId]);
    if (!missing.length) {
      return;
    }
    let cancelled = false;
    void Promise.all(
      missing.map(async (fork) => {
        try {
          const response = (await ConversationsService.listConversationMessages({
            conversationId: fork.conversationId!,
            limit: 200,
            channel: "history",
          })) as unknown as ListUserMessagesResponse;
          const messages = Array.isArray(response.data) ? response.data : [];
          const userIndex =
            openForkMenuMessageId && userMessageIndexById.has(openForkMenuMessageId)
              ? userMessageIndexById.get(openForkMenuMessageId)
              : undefined;
          const label = selectForkLabel(messages, userIndex);
          return { id: fork.conversationId!, label };
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
    currentForkPointKey,
    forkLabels,
    forkedAtConversationId,
    forkedAtMessageId,
    forksByPointKey,
    openForkMenuMessageId,
    openForkMenuMessageKey,
    conversationGroupId,
    userMessageIndexById,
  ]);

  const handleSendMessage = () => {
    const trimmed = input.trim();
    if (!trimmed) {
      return;
    }

    if (forking || editingMessage) {
      return;
    }

    if (!conversationId) {
      return;
    }

    const userMessage: ChatMessage = {
      id: `user-${Date.now()}`,
      author: "user",
      content: trimmed,
    };
    const lastBaseId = baseMessages.length > 0 ? baseMessages[baseMessages.length - 1].id : null;
    pendingSendRef.current = {
      id: userMessage.id,
      conversationId,
      content: trimmed,
      afterId: lastBaseId ?? null,
    };

    // Add user message to streaming updates (will be merged with base messages)
    setStreamingUpdates((prev) => [...prev, userMessage]);
    setOpenForkMenuMessageKey(null);
    setOpenForkMenuMessageId(null);
    setInput("");
    setSending(true);

    // Start new stream from position 0 with user's message
    // setSending(false) will be called in onCleanEnd or onReplayFailed
    // resetResume=true means don't try to resume, start fresh
    startEventStream(conversationId, trimmed, 0, true);
  };

  const handleCancelResponse = useCallback(async () => {
    if (!conversationId || !sending) {
      return;
    }
    setCanceling(true);
    try {
      await ConversationsService.cancelConversationResponse({ conversationId });
    } catch (error) {
      void error;
    } finally {
      setCanceling(false);
    }
    streamingConversationRef.current = null;
    webSocketStream.close();
    setSending(false);
    void queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
    void queryClient.invalidateQueries({ queryKey: ["resume-check"] });
  }, [conversationId, sending, queryClient, webSocketStream]);

  const handleKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      handleSendMessage();
    }
  };

  const isBusy = sending || forking || canceling;

  // Auto-resume logic: always attempt to resume when switching to a conversation
  // We don't wait for messages to load - the resume WebSocket will handle cases where
  // there's nothing to resume (it will just close cleanly)
  useEffect(() => {
    if (!conversationId) {
      return;
    }

    if (streamingConversationRef.current === conversationId) {
      return;
    }

    if (pendingForkRef.current?.conversationId === conversationId) {
      return;
    }

    // Don't auto-resume if we're currently sending/streaming a message
    // This prevents race conditions where resume-check gets invalidated during an active stream
    if (sending || forking) {
      return;
    }

    // Use a local flag to prevent multiple resume attempts in the same effect run
    // The ref is cleared above when conversationId changes, so this check prevents
    // the effect from attempting resume multiple times if it runs multiple times
    // for the same conversationId in the same mount
    if (hasResumedRef.current[conversationId]) {
      return;
    }

    // Always attempt resume when switching to a conversation
    // Don't wait for messages to load - if there's nothing to resume, the WebSocket will close cleanly
    // Set the flag immediately to prevent duplicate attempts
    hasResumedRef.current[conversationId] = true;
    // Start stream from position 0 with empty text to replay the last message
    // The stream will update the last assistant message via the onChunk callback
    // No need to filter it out - the stream will replace/update it as chunks arrive
    // resetResume=false means we want to resume from position 0, not reset
    const startFn = startEventStreamRef.current;
    if (!startFn) {
      // Reset the flag if we can't start
      delete hasResumedRef.current[conversationId];
      return;
    }
    startFn(conversationId, "", 0, false, "auto resume");

    // Note: This effect syncs with external system (WebSocket) by starting the stream
    // State updates happen via callbacks from the WebSocket, not directly in the effect
    // We attempt resume immediately when conversationId changes, regardless of message loading state
    // The resume WebSocket will handle the case where there's nothing to resume gracefully
    // We use startEventStreamRef instead of startEventStream in dependencies to avoid
    // the effect re-running when startEventStream is recreated
    // The hasResumedRef is cleared by useLayoutEffect when conversationId changes, allowing
    // resume when switching back to a conversation
  }, [conversationId, sending, forking]);

  return (
    <main className="flex flex-1 flex-col bg-muted/20">
      <div className="border-b px-6 py-4">
        <h2 className="text-lg font-semibold">Chat with your agent</h2>
        <p className="text-xs text-muted-foreground">
          Start a new chat or select a conversation from the left to continue.
        </p>
      </div>

      <div className="flex-1 overflow-y-auto px-6 py-4">
        {displayedMessages.length === 0 ? (
          <Card className="mx-auto max-w-xl border-dashed">
            <CardHeader>
              <CardTitle className="text-base">No messages yet</CardTitle>
              <CardDescription>Type a message below to start chatting with your agent.</CardDescription>
            </CardHeader>
          </Card>
        ) : (
          <div className="mx-auto flex max-w-2xl flex-col gap-3">
            {displayedMessages.map((message, index) => {
              const isLastMessage = index === displayedMessages.length - 1;
              const isStreaming = sending && isLastMessage && message.author === "assistant";
              const isUser = message.author === "user";
              const isEditing =
                editingMessage?.id === message.id && editingMessage?.conversationId === message.raw?.conversationId;
              const messageConversationId = message.raw?.conversationId ?? null;
              const messageId = message.raw?.id ?? null;
              const previousId =
                messageConversationId && messageId ? previousMessageId(messageConversationId, messageId) : null;
              const meta = messageConversationId ? conversationMetaById.get(messageConversationId) : null;
              const pointConversationId =
                previousId === null && meta?.forkedAtConversationId
                  ? meta.forkedAtConversationId
                  : messageConversationId;
              const pointPreviousId =
                previousId === null && meta?.forkedAtConversationId ? (meta.forkedAtMessageId ?? null) : previousId;
              const { key: forkKey, forks: forkOptions } =
                pointConversationId && messageId
                  ? getForksAtPoint(pointConversationId, pointPreviousId)
                  : { key: null, forks: [] };
              const showParentEntry = Boolean(forkKey && currentForkPointKey && forkKey === currentForkPointKey);
              const shouldShowForkMenu = Boolean(forkKey && (forkOptions.length > 0 || showParentEntry));
              const forkOptionsWithParent = (() => {
                if (!shouldShowForkMenu) {
                  return [];
                }
                const entries = [...forkOptions];
                if (showParentEntry && forkedAtConversationId) {
                  entries.push({
                    conversationId: forkedAtConversationId,
                    forkedAtConversationId: undefined,
                    forkedAtMessageId: forkedAtMessageId ?? undefined,
                    conversationGroupId: conversationGroupId ?? undefined,
                    createdAt: undefined,
                    title: undefined,
                  });
                }
                if (conversationId) {
                  entries.push({
                    conversationId,
                    forkedAtConversationId: forkedAtConversationId ?? null,
                    forkedAtMessageId: forkedAtMessageId ?? undefined,
                    conversationGroupId: conversationGroupId ?? undefined,
                    createdAt: undefined,
                    title: undefined,
                  });
                }
                const seen = new Set<string>();
                return entries.filter((entry) => {
                  if (!entry.conversationId || seen.has(entry.conversationId)) {
                    return false;
                  }
                  seen.add(entry.conversationId);
                  return true;
                });
              })();
              const isForkMenuOpen = forkKey !== null && openForkMenuMessageKey === forkKey;
              return (
                <div key={message.id} className={`flex ${isUser ? "justify-end" : "justify-start"}`}>
                  <div className={`relative flex max-w-[80%] flex-col gap-1 ${isUser ? "items-end" : "items-start"}`}>
                    {isEditing ? (
                      <div className="w-full rounded-lg border bg-background px-3 py-2 text-sm shadow-sm">
                        <textarea
                          value={editingText}
                          onChange={(event) => setEditingText(event.target.value)}
                          rows={3}
                          className="w-full resize-none rounded-md border px-3 py-2 text-sm outline-none ring-offset-background focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                        />
                        <div className="mt-2 flex justify-end gap-2">
                          <Button size="sm" variant="outline" onClick={handleEditCancel} disabled={isBusy}>
                            Cancel
                          </Button>
                          <Button size="sm" onClick={handleForkSend} disabled={isBusy || !editingText.trim()}>
                            Send
                          </Button>
                        </div>
                      </div>
                    ) : (
                      <div
                        className={`group relative rounded-lg px-3 py-2 text-sm ${
                          isUser ? "bg-primary text-primary-foreground" : "bg-muted text-foreground"
                        }`}
                      >
                        <Streamdown isAnimating={isStreaming}>{message.content}</Streamdown>
                        {isUser && message.raw?.id ? (
                          <div className="absolute -top-2 right-0 flex translate-y-[-50%] opacity-0 transition-opacity group-hover:opacity-100">
                            <button
                              type="button"
                              onClick={() => handleEditStart(message)}
                              disabled={isBusy}
                              className="rounded-full border bg-background px-2 py-0.5 text-[10px] font-medium text-foreground shadow-sm disabled:opacity-50"
                            >
                              Edit
                            </button>
                          </div>
                        ) : null}
                      </div>
                    )}
                    {shouldShowForkMenu && forkOptionsWithParent.length > 0 && !isEditing ? (
                      <div className="relative w-full text-right">
                        <button
                          type="button"
                          className="text-xs font-medium text-muted-foreground hover:text-foreground"
                          onClick={() => {
                            if (!forkKey) {
                              return;
                            }
                            setOpenForkMenuMessageKey((prev) => {
                              if (prev === forkKey) {
                                setOpenForkMenuMessageId(null);
                                return null;
                              }
                              setOpenForkMenuMessageId(message.id);
                              return forkKey;
                            });
                          }}
                        >
                          Forks ({forkOptionsWithParent.length})
                        </button>
                        {isForkMenuOpen ? (
                          <div className="absolute right-0 z-10 mt-2 w-64 rounded-md border bg-background p-2 text-xs shadow-sm">
                            {forkOptionsWithParent.map((fork) => {
                              const isActive = fork.conversationId === conversationId;
                              const fallbackLabel = isActive ? message.content : "Loading fork message...";
                              const label = forkLabels[fork.conversationId ?? ""] ?? fallbackLabel;
                              return (
                                <button
                                  key={fork.conversationId}
                                  type="button"
                                  onClick={() => handleForkSelect(fork.conversationId!)}
                                  className={`flex w-full items-center justify-between rounded-md px-2 py-2 text-left transition-colors ${
                                    isActive ? "bg-muted text-foreground" : "hover:bg-muted/60"
                                  }`}
                                >
                                  <div className="flex flex-col">
                                    <span className="text-sm font-medium">{formatForkLabel(label)}</span>
                                    <span className="text-xs text-muted-foreground">
                                      {formatForkTimestamp(fork.createdAt)}
                                    </span>
                                  </div>
                                  {isActive ? (
                                    <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-semibold text-primary">
                                      Active
                                    </span>
                                  ) : null}
                                </button>
                              );
                            })}
                          </div>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      <div className="border-t bg-background px-6 py-3">
        <div className="mx-auto flex max-w-2xl flex-col gap-2">
          <textarea
            value={input}
            onChange={(event) => setInput(event.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Type your message…"
            rows={3}
            disabled={!conversationId || isBusy || Boolean(editingMessage)}
            className="w-full resize-none rounded-md border px-3 py-2 text-sm outline-none ring-offset-background focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:opacity-50"
          />
          <div className="flex justify-end gap-2">
            {sending ? (
              <Button
                size="sm"
                variant="outline"
                onClick={handleCancelResponse}
                disabled={canceling || !conversationId}
              >
                Stop
              </Button>
            ) : (
              <Button
                size="sm"
                onClick={handleSendMessage}
                disabled={isBusy || !input.trim() || !conversationId || Boolean(editingMessage)}
              >
                Send
              </Button>
            )}
          </div>
        </div>
      </div>
    </main>
  );
}
