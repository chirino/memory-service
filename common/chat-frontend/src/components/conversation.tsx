/* eslint-disable react-refresh/only-export-components */
/**
 * Headless Conversation Primitives
 *
 * A Radix-style headless component library for AI chat UIs.
 *
 * Architecture:
 * - Reducer (conversationReducer): Raw state updates only, no reconciliation logic
 * - Selector hooks (useConversationMessages): ID-based echo detection, message deduplication
 * - Controller: Injected backend integration (streaming, etc.)
 *
 * Key design decisions:
 * - StreamId guards prevent stale chunks from previous streams being applied
 * - Assistant message IDs are generated upfront (not in reducer) for deterministic behavior
 * - Concurrent send/resume calls are guarded by streaming phase checks
 * - ID-based echo detection (not content) correctly handles duplicate user messages
 * - ConversationInput submits exactly once with the actual textarea value at event time
 * - useConversationInput().submit() accepts optional value to avoid stale closures
 * - useOptionalConversationContext() allows safe context access outside Root
 * - Initial mount skips effects to avoid race condition with reducer initial state
 * - MessageContext provides generic extension point for message-level UI customization
 *
 * Accessibility:
 * - Viewport: role="log", aria-live="polite", aria-relevant="additions"
 * - Messages container: role="list", aria-label="Conversation messages"
 * - Message items: role="listitem", aria-live="polite" for streaming, aria-busy for pending
 * - Input: aria-label, aria-disabled when streaming
 * - data-state attributes for consumer styling hooks
 */
import type React from "react";
import {
  createContext,
  forwardRef,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useReducer,
  useRef,
  type ReactNode,
} from "react";
import { Slot } from "@radix-ui/react-slot";

type ConversationAuthor = "user" | "assistant" | "system";

export type ConversationMessage = {
  id: string;
  conversationId: string;
  author: ConversationAuthor;
  content: string;
  createdAt?: string;
  // The user ID of who sent this message (for user messages in shared conversations)
  userId?: string | null;
  // Optional explicit lineage hints; useful for the first message of a forked conversation.
  previousMessageId?: string | null;
  forkedFrom?: {
    conversationId: string;
    messageId: string | null;
  };
};

export type ConversationStreamPhase = "idle" | "sending" | "streaming" | "completed" | "canceled" | "error";

export type ConversationController = {
  startStream: (conversationId: string, text: string, callbacks: StreamCallbacks) => void | Promise<void>;
  resumeStream: (conversationId: string, callbacks: StreamCallbacks) => void | Promise<void>;
  cancelStream: (conversationId: string) => void | Promise<void>;
  selectConversation: (conversationId: string) => void | Promise<void>;
  // Optional ID factory for generating message IDs; defaults to timestamp-based IDs
  idFactory?: () => string;
};

export type StreamCallbacks = {
  onChunk?: (chunk: string) => void;
  onComplete?: () => void;
  onError?: (error: unknown) => void;
  onCancel?: () => void;
};

export type RenderableConversationMessage = ConversationMessage & {
  displayState: "stable" | "pending" | "streaming" | "canceled" | "error";
  previousMessageId: string | null;
};

type StreamingSnapshot = {
  phase: ConversationStreamPhase;
  error: string | null;
  userMessage: ConversationMessage | null;
  assistantMessage: ConversationMessage | null;
  // Stream identity to prevent stale chunks from previous streams being applied
  streamId: string | null;
  // Assistant message ID generated upfront (not in reducer) for deterministic behavior
  pendingAssistantId: string | null;
};

type ConversationState = {
  conversationId: string | null;
  conversationGroupId: string | null;
  baseMessages: ConversationMessage[];
  inputValue: string;
  streaming: StreamingSnapshot;
};

type ConversationAction =
  | { type: "SET_CONVERSATION"; conversationId: string | null; conversationGroupId: string | null }
  | { type: "SET_MESSAGES"; messages: ConversationMessage[] }
  | { type: "SET_INPUT"; value: string }
  | { type: "SEND_START"; userMessage: ConversationMessage; streamId: string; pendingAssistantId: string }
  | { type: "RESUME_START"; streamId: string; pendingAssistantId: string }
  | { type: "STREAM_CHUNK"; chunk: string; streamId: string }
  | { type: "STREAM_COMPLETE"; streamId: string }
  | { type: "STREAM_CANCEL"; streamId: string }
  | { type: "STREAM_ERROR"; error: string; streamId: string }
  | { type: "RESET_STREAM" };

type ConversationContextValue = {
  state: ConversationState;
  controller: ConversationController;
  currentUserId: string | null | undefined;
  sendMessage: (text: string) => void;
  resumeStream: (options?: { conversationId?: string | null }) => void;
  cancelStream: () => void;
  selectConversation: (conversationId: string) => void;
  setInputValue: (value: string) => void;
};

type ConversationRootProps = {
  controller: ConversationController;
  conversationId: string | null;
  conversationGroupId: string | null;
  messages?: ConversationMessage[];
  initialInputValue?: string;
  children: ReactNode;
  // Current user ID for optimistic message attribution
  currentUserId?: string | null;
};

type AsChildProps = {
  asChild?: boolean;
};

type ConversationMessagesProps = {
  children?: ReactNode | ((messages: RenderableConversationMessage[]) => ReactNode);
} & AsChildProps;

type ConversationMessageProps = {
  message: RenderableConversationMessage;
  children?: ReactNode;
} & AsChildProps;

type ConversationInputProps = {
  onSubmit?: (value: string) => void;
} & React.TextareaHTMLAttributes<HTMLTextAreaElement> &
  AsChildProps;

type ConversationActionsProps = {
  children: (options: {
    send: (text: string) => void;
    resume: (options?: { conversationId?: string | null }) => void;
    cancel: () => void;
    state: StreamingSnapshot;
    conversationId: string | null;
  }) => ReactNode;
};

const ConversationContext = createContext<ConversationContextValue | null>(null);
const MessageContext = createContext<RenderableConversationMessage | null>(null);

const initialStreamingState: StreamingSnapshot = {
  phase: "idle",
  error: null,
  userMessage: null,
  assistantMessage: null,
  streamId: null,
  pendingAssistantId: null,
};

/**
 * Generates a unique stream ID for tracking active streams.
 * Used to prevent stale chunks from previous streams being applied.
 */
function generateStreamId(): string {
  return `stream-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
}

/**
 * Conversation reducer - manages raw state updates only.
 * Reconciliation logic (echo detection, message deduplication) is handled
 * in the useConversationMessages selector hook.
 *
 * Invariants:
 * - Only one streaming assistant message at a time
 */
function conversationReducer(state: ConversationState, action: ConversationAction): ConversationState {
  switch (action.type) {
    case "SET_CONVERSATION": {
      // Only reset streaming state if conversationId actually changed (not just groupId)
      // This prevents resetting an active stream when only the groupId is updated
      const conversationIdChanged = state.conversationId !== action.conversationId;
      const shouldResetStream = conversationIdChanged && state.streaming.streamId;

      return {
        ...state,
        conversationId: action.conversationId,
        conversationGroupId: action.conversationGroupId,
        baseMessages: conversationIdChanged ? [] : state.baseMessages,
        streaming: shouldResetStream ? initialStreamingState : state.streaming,
        inputValue: conversationIdChanged ? "" : state.inputValue, // Clear draft input to prevent leakage between conversations
      };
    }
    case "SET_MESSAGES":
      // Raw state update only; reconciliation happens in useConversationMessages
      return { ...state, baseMessages: action.messages };
    case "SET_INPUT":
      return { ...state, inputValue: action.value };
    case "SEND_START":
      return {
        ...state,
        inputValue: "",
        streaming: {
          phase: "sending",
          error: null,
          userMessage: action.userMessage,
          assistantMessage: null,
          streamId: action.streamId,
          pendingAssistantId: action.pendingAssistantId,
        },
      };
    case "RESUME_START":
      return {
        ...state,
        streaming: {
          phase: "streaming",
          error: null,
          userMessage: null,
          assistantMessage: null,
          streamId: action.streamId,
          pendingAssistantId: action.pendingAssistantId,
        },
      };
    case "STREAM_CHUNK": {
      // Guard: only apply chunks if they match the active stream ID
      if (state.streaming.streamId !== action.streamId) {
        return state;
      }
      // Use pre-generated ID from pendingAssistantId
      const assistantId = state.streaming.pendingAssistantId;
      const assistant =
        state.streaming.assistantMessage ??
        (state.conversationId && assistantId
          ? {
              id: assistantId,
              author: "assistant" as const,
              conversationId: state.conversationId,
              content: "",
            }
          : null);
      if (!assistant) {
        return state;
      }
      return {
        ...state,
        streaming: {
          ...state.streaming,
          phase: "streaming",
          assistantMessage: { ...assistant, content: `${assistant.content}${action.chunk}` },
        },
      };
    }
    case "STREAM_COMPLETE": {
      // Guard: only complete if stream ID matches
      if (state.streaming.streamId !== action.streamId) {
        return state;
      }
      return {
        ...state,
        streaming: { ...state.streaming, phase: "completed" },
      };
    }
    case "STREAM_CANCEL": {
      // Guard: only cancel if stream ID matches
      if (state.streaming.streamId !== action.streamId) {
        return state;
      }
      return {
        ...state,
        streaming: { ...state.streaming, phase: "canceled" },
      };
    }
    case "STREAM_ERROR": {
      // Guard: only error if stream ID matches
      if (state.streaming.streamId !== action.streamId) {
        return state;
      }
      return {
        ...state,
        streaming: { ...state.streaming, phase: "error", error: action.error },
      };
    }
    case "RESET_STREAM":
      return { ...state, streaming: initialStreamingState };
    default:
      return state;
  }
}

function useConversationProviderValue(props: ConversationRootProps): ConversationContextValue {
  const {
    controller,
    conversationId,
    conversationGroupId,
    messages = [],
    initialInputValue = "",
    currentUserId,
  } = props;

  // Track whether component has mounted to skip prop-sync effects on initial render.
  // The reducer already receives initial props, so we only need to sync on updates.
  // This pattern is deterministic: the empty-deps effect runs once after first render,
  // setting hasMountedRef.current = true before any subsequent renders.
  const hasMountedRef = useRef(false);
  useEffect(() => {
    hasMountedRef.current = true;
  }, []);

  const [state, dispatch] = useReducer(conversationReducer, {
    conversationId,
    conversationGroupId,
    baseMessages: messages,
    inputValue: initialInputValue,
    streaming: initialStreamingState,
  });

  // Track current stream ID in a ref to pass to callbacks
  const currentStreamIdRef = useRef<string | null>(null);

  // Ref for conversation ID to guard async operations against conversation switches
  const conversationIdRef = useRef(state.conversationId);
  useLayoutEffect(() => {
    conversationIdRef.current = state.conversationId;
  }, [state.conversationId]);

  // Prop-sync effects: only run after initial mount since reducer already has initial props
  useEffect(() => {
    if (!hasMountedRef.current) return;
    if (state.conversationId === conversationId && state.conversationGroupId === conversationGroupId) {
      return;
    }
    dispatch({ type: "SET_CONVERSATION", conversationId, conversationGroupId });
  }, [conversationGroupId, conversationId, state.conversationGroupId, state.conversationId]);

  useEffect(() => {
    if (!hasMountedRef.current) return;
    dispatch({ type: "SET_MESSAGES", messages });
  }, [messages]);

  // Clear streaming state when streaming ends.
  // When completed, reset stream so the streaming message disappears and
  // the final message from /entries takes over.
  useEffect(() => {
    if (
      state.streaming.phase === "completed" ||
      state.streaming.phase === "canceled" ||
      state.streaming.phase === "error"
    ) {
      currentStreamIdRef.current = null;
      // Reset streaming state so the temporary streaming message disappears.
      // The entries query refresh will provide the final message.
      dispatch({ type: "RESET_STREAM" });
    }
  }, [state.streaming.phase]);

  const setInputValue = useCallback((value: string) => {
    dispatch({ type: "SET_INPUT", value });
  }, []);

  const handleStreamCallbacks = useCallback((streamId: string) => {
    return {
      onChunk: (chunk: string) => dispatch({ type: "STREAM_CHUNK", chunk, streamId }),
      onComplete: () => dispatch({ type: "STREAM_COMPLETE", streamId }),
      onCancel: () => dispatch({ type: "STREAM_CANCEL", streamId }),
      onError: (error: unknown) => {
        const message = error instanceof Error ? error.message : String(error ?? "Streaming failed");
        dispatch({ type: "STREAM_ERROR", error: message, streamId });
      },
    };
  }, []);

  const sendMessage = useCallback(
    (text: string) => {
      if (!state.conversationId) {
        return;
      }
      // Guard against concurrent sends
      if (state.streaming.phase === "sending" || state.streaming.phase === "streaming") {
        return;
      }
      const trimmed = text.trim();
      if (!trimmed) {
        return;
      }
      // Generate IDs upfront using controller's idFactory if available
      const idFactory = controller.idFactory ?? (() => `msg-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`);
      const streamId = generateStreamId();
      const pendingAssistantId = idFactory();
      currentStreamIdRef.current = streamId;
      const userMessage: ConversationMessage = {
        id: idFactory(),
        conversationId: state.conversationId,
        author: "user",
        content: trimmed,
        createdAt: new Date().toISOString(),
        userId: currentUserId,
      };
      dispatch({ type: "SEND_START", userMessage, streamId, pendingAssistantId });
      const callbacks = handleStreamCallbacks(streamId);
      // Wrap in try-catch to handle both sync throws and async rejections
      try {
        void Promise.resolve(controller.startStream(state.conversationId, trimmed, callbacks)).catch((error) => {
          callbacks.onError?.(error);
        });
      } catch (error) {
        callbacks.onError?.(error);
      }
    },
    [controller, currentUserId, handleStreamCallbacks, state.conversationId, state.streaming.phase],
  );

  // Helper function to resume after state update - uses dispatch directly to avoid closure issues
  const resumeStreamAfterStateUpdate = useCallback(
    (targetConversationId: string) => {
      // Use dispatch with a function to get current state
      // This ensures we're using the latest state after SET_CONVERSATION
      const idFactory = controller.idFactory ?? (() => `msg-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`);
      const streamId = generateStreamId();
      const pendingAssistantId = idFactory();
      currentStreamIdRef.current = streamId;
      dispatch({
        type: "RESUME_START",
        streamId,
        pendingAssistantId,
      });
      const callbacks = handleStreamCallbacks(streamId);
      try {
        void Promise.resolve(controller.resumeStream(targetConversationId, callbacks)).catch((error) =>
          callbacks.onError?.(error),
        );
      } catch (error) {
        callbacks.onError?.(error);
      }
    },
    [controller, handleStreamCallbacks],
  );

  const resumeStream = useCallback(
    (options?: { conversationId?: string | null }) => {
      // Use provided conversationId or fall back to state.conversationId
      const targetConversationId = options?.conversationId ?? state.conversationId;
      if (!targetConversationId) {
        return;
      }

      // If the target conversationId is different from state, update state first
      // This ensures the streaming state is associated with the correct conversation
      if (targetConversationId !== conversationIdRef.current) {
        // Update conversation state first, which will reset any existing stream
        dispatch({
          type: "SET_CONVERSATION",
          conversationId: targetConversationId,
          conversationGroupId: state.conversationGroupId,
        });
        // Schedule resume to run after state update using requestAnimationFrame
        // This ensures the reducer has processed the SET_CONVERSATION action
        requestAnimationFrame(() => {
          // Re-check using ref to ensure we're still on the same conversation
          if (conversationIdRef.current !== targetConversationId) {
            return;
          }
          // Now proceed with resume - use the ref to get current state
          // We'll check the actual state in the next render cycle
          resumeStreamAfterStateUpdate(targetConversationId);
        });
        return;
      }

      // Guard against concurrent streams using current state
      if (state.streaming.phase === "sending" || state.streaming.phase === "streaming") {
        return;
      }

      const idFactory = controller.idFactory ?? (() => `msg-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`);
      const streamId = generateStreamId();
      const pendingAssistantId = idFactory();
      currentStreamIdRef.current = streamId;
      dispatch({
        type: "RESUME_START",
        streamId,
        pendingAssistantId,
      });
      const callbacks = handleStreamCallbacks(streamId);
      // Wrap in try-catch to handle both sync throws and async rejections
      try {
        void Promise.resolve(controller.resumeStream(targetConversationId, callbacks)).catch((error) =>
          callbacks.onError?.(error),
        );
      } catch (error) {
        callbacks.onError?.(error);
      }
    },
    [
      controller,
      handleStreamCallbacks,
      state.conversationId,
      state.conversationGroupId,
      state.streaming.phase,
      resumeStreamAfterStateUpdate,
    ],
  );

  const cancelStream = useCallback(() => {
    if (!state.conversationId) {
      return;
    }
    const { phase } = state.streaming;
    // Only cancel remotely when actively sending/streaming
    if (phase !== "sending" && phase !== "streaming") {
      dispatch({ type: "RESET_STREAM" });
      return;
    }
    // Use the authoritative streamId from state, falling back to ref only if needed
    const streamId = state.streaming.streamId ?? currentStreamIdRef.current;
    if (!streamId) {
      dispatch({ type: "RESET_STREAM" });
      return;
    }

    // Wrap in try-catch to handle sync throws from controller
    try {
      void Promise.resolve(controller.cancelStream(state.conversationId))
        .catch(() => {
          // Ignore errors from cancel - we still want to update local state
        })
        .finally(() => {
          dispatch({ type: "STREAM_CANCEL", streamId });
        });
    } catch {
      // Sync throw from controller - still cancel locally
      dispatch({ type: "STREAM_CANCEL", streamId });
    }
  }, [controller, state.conversationId, state.streaming]);

  const selectConversation = useCallback(
    (id: string) => {
      try {
        void Promise.resolve(controller.selectConversation(id)).catch(() => {
          // Swallow errors to prevent unhandled rejections in UI triggers
        });
      } catch {
        // Swallow sync throws to keep UI responsive
      }
    },
    [controller],
  );

  return useMemo(
    () => ({
      state,
      controller,
      currentUserId,
      sendMessage,
      resumeStream,
      cancelStream,
      selectConversation,
      setInputValue,
    }),
    [state, controller, currentUserId, sendMessage, resumeStream, cancelStream, selectConversation, setInputValue],
  );
}

function useConversationContext() {
  const context = useContext(ConversationContext);
  if (!context) {
    throw new Error("Conversation components must be used within <Conversation.Root>");
  }
  return context;
}

/**
 * Optional-safe version of useConversationContext.
 * Returns null if used outside Conversation.Root instead of throwing.
 */
export function useOptionalConversationContext() {
  return useContext(ConversationContext);
}

/**
 * Hook to access the current message context when used within Conversation.Message.
 * Returns null if used outside a message context.
 * Useful for building custom message-level UI extensions.
 */
export function useMessageContext() {
  return useContext(MessageContext);
}

/**
 * Selector hook that reconciles base messages with streaming state.
 * Handles echo detection (user/assistant message deduplication) that was
 * previously in the reducer.
 *
 * IMPORTANT: previousMessageId inference assumes messages are provided in chronological order.
 * If messages arrive out of order, the inferred lineage may be incorrect.
 * To avoid this, either:
 * - Ensure the backend provides messages sorted by creation time
 * - Provide explicit previousMessageId on each message from the backend
 * The inference only applies when previousMessageId is undefined; explicit values are preserved.
 */
export function useConversationMessages() {
  const { state } = useConversationContext();
  const { baseMessages, streaming } = state;

  // Deduplicate base messages by ID
  const dedupedBase = useMemo(() => {
    const seen = new Set<string>();
    return baseMessages.filter((msg) => {
      if (seen.has(msg.id)) {
        return false;
      }
      seen.add(msg.id);
      return true;
    });
  }, [baseMessages]);

  const messages = useMemo<RenderableConversationMessage[]>(() => {
    const lastByConversation = new Map<string, string | null>();
    const items: RenderableConversationMessage[] = dedupedBase.map((msg) => {
      const previous =
        msg.previousMessageId === undefined
          ? (lastByConversation.get(msg.conversationId) ?? null)
          : msg.previousMessageId;
      lastByConversation.set(msg.conversationId, msg.id);
      return {
        ...msg,
        previousMessageId: previous,
        displayState: "stable",
      };
    });

    // Reconciliation: Check if streaming user message is already in base messages (content-based echo detection)
    // Uses content matching (normalized/trimmed text) instead of ID matching because backends that don't
    // echo client-generated IDs will assign new IDs to user messages, making ID-based deduplication ineffective.
    // This only applies to user-authored pending messages; assistant messages use ID-based matching since
    // resume operations can replace messages by ID.
    if (streaming.userMessage) {
      const normalizedPending = streaming.userMessage.content.trim();
      // If the backend already echoed this user message and the stream is finished,
      // avoid rendering the optimistic copy to prevent post-response duplicates.
      const hasEchoInBase = dedupedBase.some(
        (msg) =>
          msg.author === "user" &&
          msg.conversationId === streaming.userMessage?.conversationId &&
          msg.content.trim() === normalizedPending,
      );
      const isActiveStream = streaming.phase === "sending" || streaming.phase === "streaming";
      if (hasEchoInBase && !isActiveStream) {
        return items;
      }
      // Only treat it as an echo if the immediately preceding message is the same user text.
      // This still suppresses back-to-back duplicates from the backend echo, but allows a user
      // to send the same message again after an assistant turn.
      const lastMessage = items[items.length - 1];
      const isBackToBackDuplicate =
        lastMessage &&
        lastMessage.author === "user" &&
        lastMessage.conversationId === streaming.userMessage.conversationId &&
        lastMessage.content.trim() === normalizedPending;

      if (!isBackToBackDuplicate) {
        const previous =
          streaming.userMessage.previousMessageId === undefined
            ? (lastByConversation.get(streaming.userMessage.conversationId) ?? null)
            : streaming.userMessage.previousMessageId;
        const nextUser = {
          ...streaming.userMessage,
          previousMessageId: previous,
          displayState: "pending",
        } as const;
        items.push(nextUser);
        lastByConversation.set(streaming.userMessage.conversationId, streaming.userMessage.id);
      }
    }

    // Reconciliation: Check if streaming assistant message is already in base messages (ID-based echo detection)
    // Handle all streaming phases (sending, streaming, error, canceled) when we have a pendingAssistantId
    // This ensures we show placeholder messages even if error/cancel happens before first chunk
    // Show streaming assistant message during active streaming phases.
    // The streaming message is temporary - once streaming completes, it's removed
    // and the final message from /entries takes its place.
    if (
      (streaming.phase === "sending" || streaming.phase === "streaming") &&
      streaming.streamId &&
      state.conversationId
    ) {
      const assistantId = streaming.pendingAssistantId;
      const assistantMessage: ConversationMessage = streaming.assistantMessage ?? {
        id: assistantId ?? `temp-${streaming.streamId}`,
        author: "assistant" as const,
        conversationId: state.conversationId,
        content: "",
      };

      const previous =
        assistantMessage.previousMessageId === undefined
          ? (lastByConversation.get(assistantMessage.conversationId) ?? null)
          : assistantMessage.previousMessageId;
      const streamingEntry: RenderableConversationMessage = {
        ...assistantMessage,
        previousMessageId: previous,
        displayState: streaming.phase === "sending" ? "pending" : "streaming",
      };

      items.push(streamingEntry);
    }

    return items;
  }, [
    dedupedBase,
    streaming.assistantMessage,
    streaming.phase,
    streaming.userMessage,
    streaming.streamId,
    streaming.pendingAssistantId,
    state.conversationId,
  ]);

  return {
    conversationId: state.conversationId,
    conversationGroupId: state.conversationGroupId,
    messages,
  };
}

export function useConversationStreaming() {
  const { state, sendMessage, resumeStream, cancelStream } = useConversationContext();
  const isBusy = state.streaming.phase === "sending" || state.streaming.phase === "streaming";

  return {
    streaming: state.streaming,
    conversationId: state.conversationId,
    conversationGroupId: state.conversationGroupId,
    isBusy,
    sendMessage,
    resumeStream,
    cancelStream,
  };
}

export function useConversationInput() {
  const { state, setInputValue, sendMessage } = useConversationContext();
  return {
    value: state.inputValue,
    setValue: setInputValue,
    // Accept optional value to avoid stale closure issues; falls back to current state
    submit: (value?: string) => sendMessage(value ?? state.inputValue),
    reset: () => setInputValue(""),
  };
}

function ConversationRoot(props: ConversationRootProps) {
  const value = useConversationProviderValue(props);
  return <ConversationContext.Provider value={value}>{props.children}</ConversationContext.Provider>;
}

ConversationRoot.displayName = "ConversationRoot";

const ConversationViewport = forwardRef<HTMLDivElement, AsChildProps & React.HTMLAttributes<HTMLDivElement>>(
  ({ asChild, ...props }, ref) => {
    const { streaming } = useConversationStreaming();
    const Comp = asChild ? Slot : "div";
    // role="log" is appropriate for chat-like content where new messages appear
    // aria-live="polite" announces new content without interrupting the user
    return (
      <Comp role="log" aria-live="polite" aria-relevant="additions" data-state={streaming.phase} ref={ref} {...props} />
    );
  },
);

ConversationViewport.displayName = "ConversationViewport";

const ConversationMessages = forwardRef<
  HTMLDivElement,
  ConversationMessagesProps & Omit<React.HTMLAttributes<HTMLDivElement>, "children">
>(({ asChild, children, ...props }, ref) => {
  const { messages } = useConversationMessages();
  const Comp = asChild ? Slot : "div";

  // role="list" provides semantic structure for screen readers
  if (typeof children === "function") {
    return (
      <Comp
        role="list"
        aria-label="Conversation messages"
        data-state={messages.length ? "rendered" : "empty"}
        ref={ref}
        {...props}
      >
        {children(messages)}
      </Comp>
    );
  }
  return (
    <Comp
      role="list"
      aria-label="Conversation messages"
      data-state={messages.length ? "rendered" : "empty"}
      ref={ref}
      {...props}
    >
      {children}
    </Comp>
  );
});

ConversationMessages.displayName = "ConversationMessages";

const ConversationMessage = forwardRef<HTMLDivElement, ConversationMessageProps & React.HTMLAttributes<HTMLDivElement>>(
  ({ asChild, message, children, ...props }, ref) => {
    const Comp = asChild ? Slot : "div";
    // Determine aria-live based on message state:
    // - "polite" for streaming content so screen readers announce updates
    // - "off" for stable content to avoid re-announcing
    const isStreaming = message.displayState === "streaming" || message.displayState === "pending";
    const ariaLive = isStreaming ? "polite" : "off";
    // Provide descriptive label for screen readers
    const authorLabel = message.author === "user" ? "You" : message.author === "assistant" ? "Assistant" : "System";

    return (
      <MessageContext.Provider value={message}>
        <Comp
          role="listitem"
          aria-label={`${authorLabel} message`}
          aria-live={ariaLive}
          aria-busy={isStreaming}
          data-author={message.author}
          data-state={message.displayState === "stable" ? "completed" : message.displayState}
          data-conversation-id={message.conversationId}
          ref={ref}
          {...props}
        >
          {children ?? message.content}
        </Comp>
      </MessageContext.Provider>
    );
  },
);

ConversationMessage.displayName = "ConversationMessage";

const ConversationInput = forwardRef<HTMLTextAreaElement, ConversationInputProps>(
  ({ asChild, onSubmit, onKeyDown, ...props }, ref) => {
    const { value, setValue } = useConversationInput();
    const { streaming, sendMessage } = useConversationStreaming();
    const Comp = asChild ? Slot : "textarea";

    // Fixed double-submit: Submit exactly once using the passed value.
    // If onSubmit is provided, delegate entirely to it (it may call sendMessage itself).
    // Otherwise, call sendMessage directly with the actual value at submit time.
    const handleSubmit = useCallback(
      (submittedValue: string) => {
        const trimmed = submittedValue.trim();
        if (!trimmed) {
          return;
        }
        if (onSubmit) {
          // Delegate entirely to consumer's onSubmit with the submitted value
          onSubmit(trimmed);
        } else {
          // Use internal sendMessage with the actual submitted value
          sendMessage(trimmed);
        }
      },
      [onSubmit, sendMessage],
    );

    const handleKeyDown = useCallback(
      (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
        onKeyDown?.(event);
        if (event.defaultPrevented) {
          return;
        }
        if (event.key === "Enter" && !event.shiftKey) {
          event.preventDefault();
          // Use currentTarget.value to get the actual value at event time
          handleSubmit(event.currentTarget.value);
        }
      },
      [handleSubmit, onKeyDown],
    );

    const isBusy = streaming.phase === "sending" || streaming.phase === "streaming";

    return (
      <Comp
        ref={ref}
        value={value}
        onKeyDown={handleKeyDown}
        onChange={(event: React.ChangeEvent<HTMLTextAreaElement>) => setValue(event.target.value)}
        aria-label="Message input"
        aria-describedby={props["aria-describedby"]}
        aria-disabled={isBusy}
        data-state={streaming.phase}
        {...props}
      />
    );
  },
);

ConversationInput.displayName = "ConversationInput";

function ConversationActions({ children }: ConversationActionsProps) {
  const { streaming, sendMessage, resumeStream, cancelStream, conversationId } = useConversationStreaming();
  return (
    <>
      {children({
        send: sendMessage,
        resume: resumeStream,
        cancel: cancelStream,
        state: streaming,
        conversationId,
      })}
    </>
  );
}

ConversationActions.displayName = "ConversationActions";

export const Conversation = {
  Root: ConversationRoot,
  Viewport: ConversationViewport,
  Messages: ConversationMessages,
  Message: ConversationMessage,
  Input: ConversationInput,
  Actions: ConversationActions,
};

export { useConversationContext };
