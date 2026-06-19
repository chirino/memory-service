import {
  Brain,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Download,
  ExternalLink,
  FileText,
  Film,
  Image,
  Loader2,
  Volume2,
  Wrench,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Streamdown } from "streamdown";
import { cn } from "@/lib/utils";
import { JsonHighlight } from "./JsonHighlight";
import type { ContentRendererProps } from "./index";

/**
 * Maps tool IDs to human-readable labels for display.
 * When a tool event's name matches a key here, the label is shown instead.
 */
const TOOL_DISPLAY_LABELS: Record<string, string> = {
  generateImage: "Generating an image...",
};

function getToolDisplayName(toolName: string): string {
  return TOOL_DISPLAY_LABELS[toolName] ?? toolName;
}

/**
 * Rich event types from AI responses (LangChain4j ChatEvent format).
 */
type ChatEvent =
  | { eventType: "PartialResponse"; chunk: string }
  | { eventType: "PartialThinking"; chunk: string }
  | {
      eventType: "BeforeToolExecution";
      id?: string;
      toolName: string;
      input?: unknown;
    }
  | {
      eventType: "ToolExecuted";
      id?: string;
      toolName: string;
      input?: unknown;
      output?: unknown;
    }
  | { eventType: "ContentFetched"; source?: string; content?: string }
  | { eventType: "IntermediateResponse"; chunk?: string }
  | { eventType: "ChatCompleted"; finishReason?: string };

/**
 * Attachment content structure from history entries.
 */
interface Attachment {
  href?: string;
  attachmentId?: string;
  contentType: string;
  name?: string;
  description?: string;
  size?: number;
  sha256?: string;
}

/**
 * Message content structure.
 * Messages have a role (USER or AI), text content, optional attachments,
 * and optional rich events from AI streaming responses.
 */
interface Message {
  role: "USER" | "AI";
  text?: string;
  attachments?: Attachment[];
  events?: ChatEvent[];
}

/**
 * Renderer for the "history" and "history/lc4j" content types.
 * Displays messages as chat bubbles with USER messages on the right
 * and AI messages on the left. When rich events are present, renders
 * tool calls, thinking sections, and other event types inline.
 */
export function HistoryRenderer({ content }: ContentRendererProps) {
  // Type guard to validate message structure
  const isValidMessage = (item: unknown): item is Message => {
    if (typeof item !== "object" || item === null) return false;
    const obj = item as Record<string, unknown>;
    if (obj.role !== "USER" && obj.role !== "AI") return false;
    // A message is valid if it has text, attachments, or events
    const hasText = typeof obj.text === "string";
    const hasAttachments =
      Array.isArray(obj.attachments) && obj.attachments.length > 0;
    const hasEvents = Array.isArray(obj.events) && obj.events.length > 0;
    return hasText || hasAttachments || hasEvents;
  };

  // Filter to only valid messages
  const messages = (content as unknown[]).filter(isValidMessage);

  if (messages.length === 0) {
    return (
      <div className="text-sm text-muted-foreground italic">
        No valid messages to display
      </div>
    );
  }

  return (
    <div className="space-y-3 py-2">
      {messages.map((message, index) => (
        <MessageBubble key={index} message={message} />
      ))}
    </div>
  );
}

/**
 * Extracts the attachment UUID from an href like "/v1/attachments/{uuid}".
 */
function extractAttachmentId(attachment: Attachment): string | undefined {
  if (attachment.attachmentId) return attachment.attachmentId;
  if (!attachment.href) return undefined;
  const match = attachment.href.match(/\/v1\/attachments\/([0-9a-f-]{36})$/i);
  return match ? match[1] : undefined;
}

/**
 * Fetches a signed download URL for an attachment via the admin API.
 * For external URLs (no attachment ID), returns the href directly.
 */
async function fetchSignedDownloadUrl(
  attachment: Attachment,
): Promise<string | undefined> {
  const attachmentId = extractAttachmentId(attachment);
  if (!attachmentId) {
    return attachment.href;
  }
  try {
    const response = await fetch(`/v1/admin/attachments/${attachmentId}/download-url`, {
      headers: {
        Authorization: `Bearer ${localStorage.getItem("access_token") || ""}`,
      },
    });
    const data = await response.json();
    return data?.url ?? undefined;
  } catch {
    console.error("Failed to get admin download URL for attachment");
    return undefined;
  }
}

/**
 * Returns the appropriate icon for a file based on its MIME type.
 */
function FileIcon({
  contentType,
  className,
}: {
  contentType: string;
  className?: string;
}) {
  const major = contentType?.split("/")[0];
  if (major === "image") return <Image className={className} />;
  if (major === "video") return <Film className={className} />;
  if (major === "audio") return <Volume2 className={className} />;
  return <FileText className={className} />;
}

/**
 * Compact attachment chip for message bubbles.
 * Shows file-type icon, filename, and preview/download action buttons.
 * Uses the admin download-url endpoint for signed URLs.
 */
function AttachmentPreview({
  attachment,
  isUserMessage,
}: {
  attachment: Attachment;
  isUserMessage?: boolean;
}) {
  const displayName = attachment.name || "Attachment";
  const [isLoading, setIsLoading] = useState(false);
  const hasLink = !!(attachment.href || attachment.attachmentId);

  const handlePreview = async () => {
    if (isLoading) return;
    setIsLoading(true);
    try {
      const url = await fetchSignedDownloadUrl(attachment);
      if (url) {
        window.open(url, "_blank");
      }
    } finally {
      setIsLoading(false);
    }
  };

  const handleDownload = async () => {
    if (isLoading) return;
    setIsLoading(true);
    try {
      const url = await fetchSignedDownloadUrl(attachment);
      if (url) {
        const a = document.createElement("a");
        a.href = url;
        a.download = displayName;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
      }
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div
      className={cn(
        "inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs",
        isUserMessage
          ? "border-primary-foreground/20 bg-primary-foreground/10 text-primary-foreground"
          : "border-muted-foreground/20 bg-background/60 text-foreground",
      )}
    >
      <FileIcon
        contentType={attachment.contentType}
        className="h-3.5 w-3.5 shrink-0 opacity-60"
      />
      <span className="max-w-35 truncate">{displayName}</span>
      {hasLink && (
        <>
          <button
            type="button"
            onClick={handlePreview}
            disabled={isLoading}
            className={cn(
              "ml-0.5 rounded-full p-0.5 transition-colors disabled:opacity-50",
              isUserMessage
                ? "text-primary-foreground/60 hover:bg-primary-foreground/15 hover:text-primary-foreground"
                : "text-muted-foreground/60 hover:bg-foreground/10 hover:text-foreground",
            )}
            title="Preview"
          >
            <ExternalLink className="h-3 w-3" />
          </button>
          <button
            type="button"
            onClick={handleDownload}
            disabled={isLoading}
            className={cn(
              "rounded-full p-0.5 transition-colors disabled:opacity-50",
              isUserMessage
                ? "text-primary-foreground/60 hover:bg-primary-foreground/15 hover:text-primary-foreground"
                : "text-muted-foreground/60 hover:bg-foreground/10 hover:text-foreground",
            )}
            title="Download"
          >
            <Download className="h-3 w-3" />
          </button>
        </>
      )}
    </div>
  );
}

/**
 * Returns true if the attachment is an image based on its contentType.
 */
function isImageAttachment(attachment: Attachment): boolean {
  return attachment.contentType?.split("/")[0] === "image";
}

/**
 * Inline image preview for image attachments in message bubbles.
 * Renders the image with max 50% width and hover overlay buttons
 * for opening in a new tab or downloading.
 */
function ImageAttachmentPreview({
  attachment,
  isUserMessage,
}: {
  attachment: Attachment;
  isUserMessage?: boolean;
}) {
  const [imageUrl, setImageUrl] = useState<string | undefined>(undefined);
  const [isLoading, setIsLoading] = useState(false);
  const displayName = attachment.name || "Image";

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const url = await fetchSignedDownloadUrl(attachment);
      if (!cancelled && url) {
        setImageUrl(url);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [attachment]);

  const handlePreview = async () => {
    if (isLoading) return;
    setIsLoading(true);
    try {
      const url = await fetchSignedDownloadUrl(attachment);
      if (url) window.open(url, "_blank");
    } finally {
      setIsLoading(false);
    }
  };

  const handleDownload = async () => {
    if (isLoading) return;
    setIsLoading(true);
    try {
      const url = await fetchSignedDownloadUrl(attachment);
      if (url) {
        const a = document.createElement("a");
        a.href = url;
        a.download = displayName;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
      }
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="group/img relative inline-block max-w-[50%]">
      {imageUrl ? (
        <img
          src={imageUrl}
          alt={attachment.description || displayName}
          className="rounded-lg"
          style={{ maxWidth: "100%", height: "auto" }}
        />
      ) : (
        <div
          className={cn(
            "flex h-24 w-32 items-center justify-center rounded-lg",
            isUserMessage
              ? "bg-primary-foreground/10"
              : "bg-foreground/5",
          )}
        >
          <Image
            className={cn(
              "h-6 w-6 animate-pulse",
              isUserMessage
                ? "text-primary-foreground/40"
                : "text-muted-foreground/40",
            )}
          />
        </div>
      )}
      {/* Hover overlay with action buttons */}
      <div className="absolute left-1.5 top-1.5 flex gap-1 opacity-0 transition-opacity group-hover/img:opacity-100">
        <button
          type="button"
          onClick={handlePreview}
          disabled={isLoading}
          className="rounded-md bg-black/60 p-1.5 text-white backdrop-blur-sm transition-colors hover:bg-black/80 disabled:opacity-50"
          title="Open in new tab"
        >
          <ExternalLink className="h-3.5 w-3.5" />
        </button>
        <button
          type="button"
          onClick={handleDownload}
          disabled={isLoading}
          className="rounded-md bg-black/60 p-1.5 text-white backdrop-blur-sm transition-colors hover:bg-black/80 disabled:opacity-50"
          title="Download"
        >
          <Download className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}

/**
 * Individual message bubble component.
 * Renders rich events when available, otherwise falls back to plain text.
 */
function MessageBubble({ message }: { message: Message }) {
  const isUser = message.role === "USER";

  return (
    <div className={cn("flex", isUser ? "justify-end" : "justify-start")}>
      <div
        className={cn(
          "max-w-[80%] rounded-2xl px-4 py-2",
          isUser
            ? "bg-primary text-primary-foreground rounded-br-md"
            : "bg-muted text-foreground rounded-bl-md",
        )}
      >
        <div
          className={cn(
            "text-[10px] font-medium uppercase tracking-wide mb-1",
            isUser ? "text-primary-foreground/70" : "text-muted-foreground",
          )}
        >
          {message.role}
        </div>
        {message.attachments &&
          message.attachments.length > 0 &&
          (() => {
            const nonImageAtts = message.attachments.filter(
              (a) => !isImageAttachment(a),
            );
            const imageAtts = message.attachments.filter((a) =>
              isImageAttachment(a),
            );
            return (
              <>
                {nonImageAtts.length > 0 && (
                  <div className="mb-2 flex flex-wrap gap-1.5">
                    {nonImageAtts.map((att, i) => {
                      const key =
                        att.attachmentId ??
                        att.name ??
                        `att-${i}`;
                      return (
                        <AttachmentPreview
                          key={key}
                          attachment={att}
                          isUserMessage={isUser}
                        />
                      );
                    })}
                  </div>
                )}
                {imageAtts.length > 0 && (
                  <div className="mb-2 flex flex-wrap gap-2">
                    {imageAtts.map((att, i) => {
                      const key =
                        att.attachmentId ??
                        att.name ??
                        `img-${i}`;
                      return (
                        <ImageAttachmentPreview
                          key={key}
                          attachment={att}
                          isUserMessage={isUser}
                        />
                      );
                    })}
                  </div>
                )}
              </>
            );
          })()}
        {message.events && message.events.length > 0 ? (
          <div className="text-sm prose prose-sm max-w-none dark:prose-invert">
            <RichEventRenderer events={message.events} />
          </div>
        ) : message.text ? (
          <div className="text-sm prose prose-sm max-w-none dark:prose-invert">
            <Streamdown>{message.text}</Streamdown>
          </div>
        ) : null}
      </div>
    </div>
  );
}

// ─── Rich Event Rendering ────────────────────────────────────────────

type EventGroup =
  | { type: "text"; content: string }
  | { type: "thinking"; content: string }
  | { type: "tool-pending"; id?: string; toolName: string; input?: unknown }
  | {
      type: "tool-result";
      id?: string;
      toolName: string;
      input?: unknown;
      output?: unknown;
    }
  | { type: "content-fetched"; source?: string; content?: string };

function groupAdjacentTextEvents(events: ChatEvent[]): EventGroup[] {
  const groups: EventGroup[] = [];
  let currentTextBuffer = "";
  let currentThinkingBuffer = "";

  // Build a lookup of completed tool executions by id so we can merge them
  // with their corresponding BeforeToolExecution events.
  const completedTools = new Map<
    string,
    { toolName: string; input?: unknown; output?: unknown }
  >();
  const consumedToolExecutedIds = new Set<string>();
  for (const event of events) {
    if (event.eventType === "ToolExecuted" && event.id) {
      completedTools.set(event.id, {
        toolName: event.toolName,
        input: event.input,
        output: event.output,
      });
    }
  }

  const flushText = () => {
    if (currentTextBuffer) {
      groups.push({ type: "text", content: currentTextBuffer });
      currentTextBuffer = "";
    }
  };

  const flushThinking = () => {
    if (currentThinkingBuffer) {
      groups.push({ type: "thinking", content: currentThinkingBuffer });
      currentThinkingBuffer = "";
    }
  };

  for (const event of events) {
    switch (event.eventType) {
      case "PartialResponse":
        flushThinking();
        currentTextBuffer += event.chunk;
        break;
      case "PartialThinking":
        flushText();
        currentThinkingBuffer += event.chunk;
        break;
      case "BeforeToolExecution": {
        flushText();
        flushThinking();
        // If we have a matching ToolExecuted, render as completed directly
        const completed = event.id
          ? completedTools.get(event.id)
          : undefined;
        if (completed && event.id) {
          consumedToolExecutedIds.add(event.id);
          groups.push({
            type: "tool-result",
            id: event.id,
            toolName: completed.toolName,
            input: completed.input,
            output: completed.output,
          });
        } else {
          groups.push({
            type: "tool-pending",
            id: event.id,
            toolName: event.toolName,
            input: event.input,
          });
        }
        break;
      }
      case "ToolExecuted":
        // Skip if already consumed by a matching BeforeToolExecution
        if (event.id && consumedToolExecutedIds.has(event.id)) {
          break;
        }
        flushText();
        flushThinking();
        groups.push({
          type: "tool-result",
          id: event.id,
          toolName: event.toolName,
          input: event.input,
          output: event.output,
        });
        break;
      case "ContentFetched":
        flushText();
        flushThinking();
        groups.push({
          type: "content-fetched",
          source: event.source,
          content: event.content,
        });
        break;
      case "IntermediateResponse":
        if (event.chunk) {
          flushThinking();
          currentTextBuffer += event.chunk;
        }
        break;
      case "ChatCompleted":
        break;
    }
  }

  flushText();
  flushThinking();

  return groups;
}

function RichEventRenderer({ events }: { events: ChatEvent[] }) {
  const groupedEvents = groupAdjacentTextEvents(events);

  return (
    <div className="flex flex-col gap-2">
      {groupedEvents.map((group, i) => (
        <EventBlock key={i} group={group} />
      ))}
    </div>
  );
}

function EventBlock({ group }: { group: EventGroup }) {
  switch (group.type) {
    case "text":
      return <Streamdown>{group.content}</Streamdown>;
    case "thinking":
      return <ThinkingSection content={group.content} />;
    case "tool-pending":
      return <ToolCallPending name={group.toolName} input={group.input} />;
    case "tool-result":
      return (
        <ToolCallResult
          name={group.toolName}
          input={group.input}
          output={group.output}
        />
      );
    case "content-fetched":
      return (
        <ContentFetchedBlock source={group.source} content={group.content} />
      );
    default:
      return null;
  }
}

function ThinkingSection({ content }: { content: string }) {
  const [isExpanded, setIsExpanded] = useState(false);

  if (!content.trim()) return null;

  return (
    <div className="my-2 rounded-lg border border-border bg-muted/50">
      <button
        type="button"
        onClick={() => setIsExpanded(!isExpanded)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-muted-foreground transition-colors hover:bg-muted"
      >
        {isExpanded ? (
          <ChevronDown className="h-4 w-4" />
        ) : (
          <ChevronRight className="h-4 w-4" />
        )}
        <Brain className="h-4 w-4 text-accent-foreground" />
        <span className="font-medium">Thinking</span>
      </button>
      {isExpanded && (
        <div className="border-t border-border px-3 py-2">
          <div className="whitespace-pre-wrap text-sm italic text-muted-foreground">
            <Streamdown>{content}</Streamdown>
          </div>
        </div>
      )}
    </div>
  );
}

function ToolCallPending({ name, input }: { name: string; input?: unknown }) {
  const [isExpanded, setIsExpanded] = useState(false);
  const hasInput = input !== undefined && input !== null;

  return (
    <div className="my-2 rounded-lg border border-success/30 bg-success/5">
      <button
        type="button"
        onClick={() => hasInput && setIsExpanded(!isExpanded)}
        disabled={!hasInput}
        className={cn(
          "flex w-full items-center gap-2 px-3 py-2 text-left text-sm",
          hasInput ? "cursor-pointer hover:bg-success/10" : "cursor-default",
        )}
      >
        <Loader2 className="h-4 w-4 animate-spin text-success" />
        <Wrench className="h-4 w-4 text-success" />
        <span className="font-medium text-foreground">{getToolDisplayName(name)}</span>
        {hasInput &&
          (isExpanded ? (
            <ChevronDown className="ml-auto h-4 w-4 text-muted-foreground" />
          ) : (
            <ChevronRight className="ml-auto h-4 w-4 text-muted-foreground" />
          ))}
      </button>
      {isExpanded && hasInput && (
        <div className="border-t border-success/20 px-3 py-2">
          <div className="text-xs text-muted-foreground">Input:</div>
          <JsonHighlight value={input} />
        </div>
      )}
    </div>
  );
}

function ToolCallResult({
  name,
  input,
  output,
}: {
  name: string;
  input?: unknown;
  output?: unknown;
}) {
  const [isExpanded, setIsExpanded] = useState(false);
  const hasDetails =
    (input !== undefined && input !== null) ||
    (output !== undefined && output !== null);

  return (
    <div className="my-2 rounded-lg border border-success/30 bg-success/10">
      <button
        type="button"
        onClick={() => hasDetails && setIsExpanded(!isExpanded)}
        disabled={!hasDetails}
        className={cn(
          "flex w-full items-center gap-2 px-3 py-2 text-left text-sm",
          hasDetails ? "cursor-pointer hover:bg-success/20" : "cursor-default",
        )}
      >
        <CheckCircle2 className="h-4 w-4 text-success" />
        <Wrench className="h-4 w-4 text-success" />
        <span className="font-medium text-foreground">{getToolDisplayName(name)}</span>
        <span className="text-xs text-muted-foreground">completed</span>
        {hasDetails &&
          (isExpanded ? (
            <ChevronDown className="ml-auto h-4 w-4 text-muted-foreground" />
          ) : (
            <ChevronRight className="ml-auto h-4 w-4 text-muted-foreground" />
          ))}
      </button>
      {isExpanded && (
        <div className="space-y-2 border-t border-success/20 px-3 py-2">
          {input !== undefined && input !== null && (
            <div>
              <div className="text-xs text-muted-foreground">Input:</div>
              <JsonHighlight value={input} />
            </div>
          )}
          {output !== undefined && output !== null && (
            <div>
              <div className="text-xs text-muted-foreground">Output:</div>
              <JsonHighlight value={output} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function ContentFetchedBlock({
  source,
  content,
}: {
  source?: string;
  content?: string;
}) {
  const [isExpanded, setIsExpanded] = useState(false);

  return (
    <div className="my-2 rounded-lg border border-border bg-muted/30">
      <button
        type="button"
        onClick={() => content && setIsExpanded(!isExpanded)}
        disabled={!content}
        className={cn(
          "flex w-full items-center gap-2 px-3 py-2 text-left text-sm",
          content ? "cursor-pointer hover:bg-muted/50" : "cursor-default",
        )}
      >
        <FileText className="h-4 w-4 text-muted-foreground" />
        <span className="text-xs text-muted-foreground">Retrieved content</span>
        {source && (
          <span className="truncate text-xs font-medium text-foreground">
            {source}
          </span>
        )}
        {content &&
          (isExpanded ? (
            <ChevronDown className="ml-auto h-4 w-4 text-muted-foreground" />
          ) : (
            <ChevronRight className="ml-auto h-4 w-4 text-muted-foreground" />
          ))}
      </button>
      {isExpanded && content && (
        <div className="border-t border-border px-3 py-2">
          <pre className="whitespace-pre-wrap text-xs text-muted-foreground">
            {content}
          </pre>
        </div>
      )}
    </div>
  );
}

// Made with Bob