/**
 * Conversations UI - Tailwind-styled wrapper components for Conversation primitives
 *
 * Provides a shadcn-style UI layer over the headless Conversation components.
 * This module handles the visual presentation while Conversation handles state management.
 */
import { forwardRef } from "react";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import {
  Conversation,
  type RenderableConversationMessage,
  useConversationInput,
  useConversationStreaming,
} from "@/components/conversation";
import { Streamdown } from "streamdown";
import type React from "react";

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
        className={`flex-1 overflow-y-auto px-6 py-4 ${className ?? ""}`}
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
            return (
              <div className={`mx-auto flex max-w-2xl flex-col gap-3 ${className ?? ""}`}>
                {children(items)}
              </div>
            );
          }
          return (
            <div className={`mx-auto flex max-w-2xl flex-col gap-3 ${className ?? ""}`}>
              {children}
            </div>
          );
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
};

/**
 * Basic message row component with bubble styling.
 * Renders user/assistant messages with appropriate alignment and colors.
 * Does not include fork/edit UI - those should be added by consumers via the overlay prop.
 */
function ConversationsUIMessageRow({ message, children, className, overlay }: ConversationsUIMessageRowProps) {
  const isUser = message.author === "user";
  const messageStateClass =
    message.displayState === "pending"
      ? "opacity-70"
      : message.displayState === "streaming"
        ? "opacity-90"
        : "";

  return (
    <Conversation.Message message={message} asChild>
      <div className={`flex ${isUser ? "justify-end" : "justify-start"} ${className ?? ""}`}>
        <div className={`relative flex max-w-[80%] flex-col gap-1 ${isUser ? "items-end" : "items-start"}`}>
          <div
            className={`group relative rounded-lg px-3 py-2 text-sm ${messageStateClass} ${
              isUser ? "bg-primary text-primary-foreground" : "bg-muted text-foreground"
            }`}
          >
            {children ?? <Streamdown isAnimating={message.displayState === "streaming"}>{message.content}</Streamdown>}
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

/**
 * Empty state card shown when there are no messages.
 */
function ConversationsUIEmptyState({
  title = "No messages yet",
  description = "Type a message below to start chatting with your agent.",
  className,
}: ConversationsUIEmptyStateProps) {
  return (
    <Card className={`mx-auto max-w-xl border-dashed ${className ?? ""}`}>
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
    </Card>
  );
}

type ConversationsUIComposerProps = {
  placeholder?: string;
  disabled?: boolean;
  cancelDisabled?: boolean;
  onCancel?: () => void;
  cancelLabel?: string;
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
  placeholder = "Type your message…",
  disabled = false,
  cancelDisabled = false,
  onCancel,
  cancelLabel = "Stop",
  sendLabel = "Send",
  stopLabel = "Stop",
  className,
  inputClassName,
}: ConversationsUIComposerProps) {
  const { value, submit } = useConversationInput();
  const { streaming, cancelStream, isBusy, conversationId } = useConversationStreaming();

  const handleCancel = () => {
    if (isBusy) {
      cancelStream();
    }
    onCancel?.();
  };

  return (
    <div className={`border-t bg-background px-6 py-3 ${className ?? ""}`}>
      <div className="mx-auto flex max-w-2xl flex-col gap-2">
        <Conversation.Input
          placeholder={placeholder}
          rows={3}
          disabled={disabled}
          className={`w-full resize-none rounded-md border px-3 py-2 text-sm outline-none ring-offset-background focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:opacity-50 ${inputClassName ?? ""}`}
        />
        <div className="flex justify-end gap-2">
          {isBusy ? (
            <Button size="sm" variant="outline" onClick={handleCancel} disabled={cancelDisabled || !conversationId}>
              {stopLabel}
            </Button>
          ) : (
            <Button size="sm" onClick={() => submit()} disabled={disabled || !value.trim() || !conversationId}>
              {sendLabel}
            </Button>
          )}
        </div>
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
