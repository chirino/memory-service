/* eslint-disable react-refresh/only-export-components */
/**
 * Conversations UI - Tailwind-styled wrapper components for Conversation primitives
 *
 * Provides a shadcn-style UI layer over the headless Conversation components.
 * This module handles the visual presentation while Conversation handles state management.
 */
import { forwardRef, useState, useRef, useEffect } from "react";
import {
  Conversation,
  type RenderableConversationMessage,
  useConversationContext,
  useConversationInput,
  useConversationStreaming,
} from "@/components/conversation";
import { Streamdown } from "streamdown";
import type React from "react";
import { MessageCircle, PenLine, Shuffle, ChevronRight, Send, MoreHorizontal, X } from "lucide-react";

type ConversationsUIViewportProps = React.ComponentProps<typeof Conversation.Viewport>;

/**
 * Styled viewport wrapper for conversation messages.
 * Provides scrolling container with padding.
 */
const ConversationsUIViewport = forwardRef<HTMLDivElement, ConversationsUIViewportProps>(
  ({ className, ...props }, ref) => {
    return (
      <Conversation.Viewport
        ref={ref}
        className={`flex-1 overflow-x-auto overflow-y-auto px-8 ${className ?? ""}`}
        {...props}
      />
    );
  },
);

ConversationsUIViewport.displayName = "ConversationsUIViewport";

type ConversationsUIMessagesProps = React.ComponentProps<typeof Conversation.Messages>;

/**
 * Styled messages container with max-width and gap spacing.
 */
const ConversationsUIMessages = forwardRef<HTMLDivElement, ConversationsUIMessagesProps>(
  ({ className, children, ...props }, ref) => {
    return (
      <Conversation.Messages ref={ref} {...props}>
        {(items) => {
          if (typeof children === "function") {
            return <div className={`mx-auto flex max-w-3xl flex-col py-6 ${className ?? ""}`}>{children(items)}</div>;
          }
          return <div className={`mx-auto flex max-w-3xl flex-col py-6 ${className ?? ""}`}>{children}</div>;
        }}
      </Conversation.Messages>
    );
  },
);

ConversationsUIMessages.displayName = "ConversationsUIMessages";

type ConversationsUIMessageRowProps = {
  message: RenderableConversationMessage;
  children?: React.ReactNode;
  className?: string;
  overlay?: React.ReactNode;
  messageRef?: React.Ref<HTMLDivElement>;
};

/**
 * Formats a timestamp for display above messages.
 * Shows time only (e.g., "10:04 AM") for today, or date and time for older messages.
 */
function formatMessageTime(createdAt?: string): string {
  if (!createdAt) return "";
  const date = new Date(createdAt);
  if (Number.isNaN(date.getTime())) return "";

  const now = new Date();
  const isToday =
    date.getDate() === now.getDate() && date.getMonth() === now.getMonth() && date.getFullYear() === now.getFullYear();

  if (isToday) {
    return date.toLocaleTimeString(undefined, {
      hour: "numeric",
      minute: "2-digit",
    });
  }

  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

/**
 * Basic message row component with bubble styling.
 * Renders user/assistant messages with appropriate alignment and colors.
 * Does not include fork/edit UI - those should be added by consumers via the overlay prop.
 */
function ConversationsUIMessageRow({
  message,
  children,
  className,
  overlay,
  messageRef,
}: ConversationsUIMessageRowProps) {
  const { currentUserId } = useConversationContext();
  const isUser = message.author === "user";
  const isStreaming = message.displayState === "streaming";

  // Determine the display name for the message author (only for user messages)
  const authorName = isUser ? (message.userId === currentUserId ? "You" : message.userId || "User") : null;

  const timestamp = formatMessageTime(message.createdAt);

  return (
    <Conversation.Message message={message} asChild>
      <div ref={messageRef} className={`flex ${isUser ? "justify-end" : "justify-start"} ${className ?? ""}`}>
        <div className={`relative flex flex-col gap-1 ${isUser ? "max-w-[75%] items-end" : "max-w-[85%] items-start"}`}>
          {/* Author and timestamp */}
          {(authorName || timestamp) && (
            <div className={`flex items-center gap-1.5 text-xs text-stone ${isUser ? "pr-1" : "pl-1"}`}>
              {authorName && <span className="font-medium">{authorName}</span>}
              {authorName && timestamp && <span className="text-stone/50">·</span>}
              {timestamp && <span>{timestamp}</span>}
            </div>
          )}
          <div
            className={`group relative px-5 py-3.5 text-[15px] leading-relaxed ${
              isUser ? "rounded-2xl rounded-tr-md bg-ink text-cream" : "rounded-2xl rounded-tl-md bg-mist text-ink"
            }`}
          >
            {children ?? (
              <>
                <Streamdown isAnimating={isStreaming}>{message.content}</Streamdown>
                {isStreaming && (
                  <span className="ml-1 inline-flex">
                    <span className="typing-dot h-1 w-1 rounded-full bg-stone" />
                    <span className="typing-dot ml-0.5 h-1 w-1 rounded-full bg-stone" />
                    <span className="typing-dot ml-0.5 h-1 w-1 rounded-full bg-stone" />
                  </span>
                )}
              </>
            )}
            {overlay}
          </div>
        </div>
      </div>
    </Conversation.Message>
  );
}

type ConversationsUIEmptyStateProps = {
  title?: string;
  description?: string;
  className?: string;
};

const morePrompts = [
  "Explain a big idea using only 7 words.",
  "Invent a new holiday in one sentence.",
  "Write a fortune cookie that feels too accurate.",
  "Describe today's mood as a weather report.",
  "Give me a two-line bedtime story for kids.",
  "Pitch a movie that shouldn't exist (one line).",
  "Create a new slang word and define it.",
  "Write an apology from a villain (one sentence).",
  "Summarize your life philosophy in 10 words.",
  "Tell me a secret a lighthouse would keep.",
  "Make a motivational quote… then ruin it with a footnote.",
  "Describe a city where emotions are currency.",
  "Write a text message that starts a mystery.",
  'Give a "recipe" for courage (3 ingredients).',
  "Explain love like you're a robot with one bug.",
  "Write a one-sentence plot twist.",
  "Give me a tiny poem about unfinished things.",
  "Create a rule for a game nobody has played.",
  "Describe a futuristic product that backfires instantly.",
  "Answer a question I didn't ask (but should have).",
];

/**
 * Empty state shown when there are no messages.
 */
function ConversationsUIEmptyState({
  title = "Start a conversation",
  description = "Ask a question, explore an idea, or get help with code. Your AI assistant is ready.",
  className,
}: ConversationsUIEmptyStateProps) {
  const { sendMessage } = useConversationStreaming();
  const [isPopupOpen, setIsPopupOpen] = useState(false);
  const [popupMaxHeight, setPopupMaxHeight] = useState(320);
  const popupRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);

  // Calculate popup max height based on viewport when opening
  const openPopup = () => {
    const viewportHeight = window.innerHeight;
    const padding = 80; // padding from top and bottom of viewport
    setPopupMaxHeight(Math.min(400, viewportHeight - padding * 2));
    setIsPopupOpen(true);
  };

  // Close popup when clicking outside
  useEffect(() => {
    if (!isPopupOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        popupRef.current &&
        !popupRef.current.contains(event.target as Node) &&
        buttonRef.current &&
        !buttonRef.current.contains(event.target as Node)
      ) {
        setIsPopupOpen(false);
      }
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsPopupOpen(false);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [isPopupOpen]);

  const suggestions = [
    {
      text: "Write me a 4 paragraph essay on the benefits of AI",
      icon: PenLine,
      iconBg: "bg-ink/10",
      iconColor: "text-ink/60",
    },
    {
      text: "Pick a random number between 1 and 100",
      icon: Shuffle,
      iconBg: "bg-sage/20",
      iconColor: "text-sage",
    },
  ];

  const handlePromptSelect = (prompt: string) => {
    setIsPopupOpen(false);
    sendMessage(prompt);
  };

  const showHeader = false;
  return (
    <div className={`flex flex-1 items-center justify-center px-8 ${className ?? ""}`}>
      <div className="max-w-md animate-slide-up text-center">
        {/* Decorative icon */}

        {showHeader && (
          <>
            <div className="mb-8 animate-float">
              <div className="inline-flex h-24 w-24 items-center justify-center rounded-3xl border border-stone/10 bg-mist">
                <MessageCircle className="h-12 w-12 text-sage" strokeWidth={1.5} />
              </div>
            </div>

            <h3 className="mb-3 font-serif text-3xl tracking-tight">{title}</h3>
            <p className="mb-8 text-lg leading-relaxed text-stone">{description}</p>
          </>
        )}

        {/* Suggestion cards */}
        <div className="mb-8 space-y-3">
          <p className="mb-4 text-xs font-medium uppercase tracking-wide text-stone">Try asking</p>

          {suggestions.map((suggestion) => {
            const Icon = suggestion.icon;
            return (
              <button
                key={suggestion.text}
                type="button"
                onClick={() => sendMessage(suggestion.text)}
                className="group w-full rounded-xl border border-transparent bg-mist px-5 py-4 text-left transition-all hover:border-stone/20"
              >
                <div className="flex items-center gap-3">
                  <div
                    className={`flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg ${suggestion.iconBg}`}
                  >
                    <Icon className={`h-4 w-4 ${suggestion.iconColor}`} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium text-ink transition-colors group-hover:text-ink/80">
                      "{suggestion.text}"
                    </p>
                  </div>
                  <ChevronRight className="h-4 w-4 text-stone/40 transition-colors group-hover:text-stone" />
                </div>
              </button>
            );
          })}

          {/* More prompts button */}
          <div className="relative">
            <button
              ref={buttonRef}
              type="button"
              onClick={() => (isPopupOpen ? setIsPopupOpen(false) : openPopup())}
              className="group w-full rounded-xl border border-transparent bg-mist px-5 py-4 text-left transition-all hover:border-stone/20"
            >
              <div className="flex items-center gap-3">
                <div className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg bg-terracotta/20">
                  <MoreHorizontal className="h-4 w-4 text-terracotta" />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-ink transition-colors group-hover:text-ink/80">More ...</p>
                </div>
                <ChevronRight
                  className={`h-4 w-4 text-stone/40 transition-all group-hover:text-stone ${isPopupOpen ? "rotate-90" : ""}`}
                />
              </div>
            </button>

            {/* Popup menu - centered in viewport */}
            {isPopupOpen && (
              <>
                {/* Backdrop */}
                <div className="fixed inset-0 z-[99] bg-ink/20" onClick={() => setIsPopupOpen(false)} />
                <div
                  ref={popupRef}
                  className="fixed left-1/2 top-1/2 z-[100] w-[90vw] max-w-md -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border border-stone/20 bg-cream shadow-xl"
                  style={{ maxHeight: popupMaxHeight }}
                >
                  <div className="sticky top-0 flex items-center justify-between border-b border-stone/10 bg-cream px-4 py-3">
                    <span className="text-xs font-medium uppercase tracking-wide text-stone">Choose a prompt</span>
                    <button
                      type="button"
                      onClick={() => setIsPopupOpen(false)}
                      className="rounded-full p-1 text-stone hover:bg-mist hover:text-ink"
                    >
                      <X className="h-4 w-4" />
                    </button>
                  </div>
                  <div className="p-2">
                    {morePrompts.map((prompt) => (
                      <button
                        key={prompt}
                        type="button"
                        onClick={() => handlePromptSelect(prompt)}
                        className="w-full rounded-lg px-3 py-2.5 text-left text-sm text-ink transition-colors hover:bg-mist"
                      >
                        {prompt}
                      </button>
                    ))}
                  </div>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

type ConversationsUIComposerProps = {
  placeholder?: string;
  disabled?: boolean;
  cancelDisabled?: boolean;
  onCancel?: () => void;
  sendLabel?: string;
  stopLabel?: string;
  className?: string;
  inputClassName?: string;
};

/**
 * Composer component with textarea input and action buttons.
 * Handles send/stop button logic based on streaming state.
 */
function ConversationsUIComposer({
  placeholder = "Type a message...",
  disabled = false,
  cancelDisabled = false,
  onCancel,
  sendLabel = "Send",
  stopLabel = "Stop",
  className,
  inputClassName,
}: ConversationsUIComposerProps) {
  const { value, submit } = useConversationInput();
  const { cancelStream, isBusy, conversationId } = useConversationStreaming();

  const handleCancel = () => {
    if (isBusy) {
      cancelStream();
    }
    onCancel?.();
  };

  const canSend = !disabled && value.trim() && conversationId;

  return (
    <div className={`border-t border-stone/10 bg-cream px-8 py-5 ${className ?? ""}`}>
      <div className="mx-auto max-w-3xl">
        <div className="relative">
          <Conversation.Input
            placeholder={placeholder}
            rows={3}
            disabled={disabled}
            className={`w-full resize-none rounded-2xl border border-transparent bg-mist px-5 py-4 pr-24 text-[15px] transition-colors placeholder:text-stone/60 focus:border-stone/20 focus:outline-none disabled:opacity-50 ${inputClassName ?? ""}`}
          />
          <div className="absolute bottom-3 right-3 flex items-center gap-2">
            {isBusy && (
              <button
                type="button"
                onClick={handleCancel}
                disabled={cancelDisabled || !conversationId}
                className="rounded-full border border-terracotta/30 px-4 py-2 text-sm text-terracotta transition-colors hover:bg-terracotta/10 disabled:opacity-50"
              >
                {stopLabel}
              </button>
            )}
            <button
              type="button"
              onClick={() => submit()}
              disabled={!canSend || isBusy}
              className="rounded-xl bg-ink p-2.5 text-cream shadow-lg shadow-ink/10 transition-colors hover:bg-ink/90 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Send className="h-5 w-5" />
              <span className="sr-only">{sendLabel}</span>
            </button>
          </div>
        </div>
        <p className="mt-2 text-center text-xs text-stone">Press Enter to send, Shift+Enter for new line</p>
      </div>
    </div>
  );
}

export const ConversationsUI = {
  Viewport: ConversationsUIViewport,
  Messages: ConversationsUIMessages,
  MessageRow: ConversationsUIMessageRow,
  EmptyState: ConversationsUIEmptyState,
  Composer: ConversationsUIComposer,
};
