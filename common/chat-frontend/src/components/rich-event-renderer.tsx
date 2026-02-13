/**
 * Rich Event Renderer - Renders rich event streams from AI responses
 *
 * Handles event types:
 * - PartialResponse: Text chunks from the main response
 * - PartialThinking: Reasoning/thinking chunks (collapsible)
 * - BeforeToolExecution: Tool call pending indicator
 * - ToolExecuted: Tool execution result
 */
import { useState } from "react";
import { Streamdown } from "streamdown";
import { ChevronDown, ChevronRight, Loader2, Wrench, CheckCircle2, Brain } from "lucide-react";
import type { ChatEvent } from "@/components/conversation";

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

export type RichEventRendererProps = {
  events: ChatEvent[];
  isStreaming?: boolean;
};

/**
 * Main component that renders a list of chat events with appropriate styling.
 * Adjacent text events (PartialResponse) are merged for cleaner display.
 */
export function RichEventRenderer({ events, isStreaming = false }: RichEventRendererProps) {
  // Group adjacent PartialResponse events together for cleaner rendering
  const groupedEvents = groupAdjacentTextEvents(events);

  return (
    <div className="rich-events flex flex-col gap-2">
      {groupedEvents.map((group, i) => (
        <EventBlock key={i} group={group} isStreaming={isStreaming && i === groupedEvents.length - 1} />
      ))}
    </div>
  );
}

type EventGroup =
  | { type: "text"; content: string }
  | { type: "thinking"; content: string }
  | { type: "tool-pending"; id?: string; toolName: string; input?: unknown }
  | { type: "tool-result"; id?: string; toolName: string; input?: unknown; output?: unknown }
  | { type: "content-fetched"; source?: string; content?: string }
  | { type: "other"; event: ChatEvent };

function groupAdjacentTextEvents(events: ChatEvent[]): EventGroup[] {
  const groups: EventGroup[] = [];
  let currentTextBuffer = "";
  let currentThinkingBuffer = "";

  // Build a lookup of completed tool executions by id so we can merge them
  // with their corresponding BeforeToolExecution events.
  const completedTools = new Map<string, { toolName: string; input?: unknown; output?: unknown }>();
  const consumedToolExecutedIds = new Set<string>();
  for (const event of events) {
    if (event.eventType === "ToolExecuted" && event.id) {
      completedTools.set(event.id, { toolName: event.toolName, input: event.input, output: event.output });
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
        const completed = event.id ? completedTools.get(event.id) : undefined;
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
          groups.push({ type: "tool-pending", id: event.id, toolName: event.toolName, input: event.input });
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
        groups.push({ type: "tool-result", id: event.id, toolName: event.toolName, input: event.input, output: event.output });
        break;
      case "ContentFetched":
        flushText();
        flushThinking();
        groups.push({ type: "content-fetched", source: event.source, content: event.content });
        break;
      case "IntermediateResponse":
        // Intermediate responses are typically text-like
        if (event.chunk) {
          flushThinking();
          currentTextBuffer += event.chunk;
        }
        break;
      case "ChatCompleted":
        // Completion markers don't need visual representation
        break;
      default:
        flushText();
        flushThinking();
        groups.push({ type: "other", event });
    }
  }

  flushText();
  flushThinking();

  return groups;
}

function EventBlock({ group, isStreaming }: { group: EventGroup; isStreaming: boolean }) {
  switch (group.type) {
    case "text":
      return <Streamdown isAnimating={isStreaming}>{group.content}</Streamdown>;
    case "thinking":
      return <ThinkingSection content={group.content} isStreaming={isStreaming} />;
    case "tool-pending":
      return <ToolCallPending name={group.toolName} input={group.input} />;
    case "tool-result":
      return <ToolCallResult name={group.toolName} input={group.input} output={group.output} />;
    case "content-fetched":
      return <ContentFetchedBlock source={group.source} content={group.content} />;
    default:
      return null;
  }
}

type ThinkingSectionProps = {
  content: string;
  isStreaming?: boolean;
};

/**
 * Collapsible section for displaying AI thinking/reasoning content.
 */
function ThinkingSection({ content, isStreaming = false }: ThinkingSectionProps) {
  const [isExpanded, setIsExpanded] = useState(false);

  if (!content.trim()) {
    return null;
  }

  return (
    <div className="my-2 rounded-lg border border-stone/20 bg-mist/50">
      <button
        type="button"
        onClick={() => setIsExpanded(!isExpanded)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-stone transition-colors hover:bg-mist"
      >
        {isExpanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
        <Brain className="h-4 w-4 text-sage" />
        <span className="font-medium">Thinking</span>
        {isStreaming && <Loader2 className="ml-auto h-3 w-3 animate-spin text-stone/50" />}
      </button>
      {isExpanded && (
        <div className="border-t border-stone/10 px-3 py-2">
          <div className="whitespace-pre-wrap text-sm italic text-stone/80">
            <Streamdown isAnimating={isStreaming}>{content}</Streamdown>
          </div>
        </div>
      )}
    </div>
  );
}

type ToolCallPendingProps = {
  name: string;
  input?: unknown;
};

/**
 * Displays a tool call in progress with spinning indicator.
 */
function ToolCallPending({ name }: ToolCallPendingProps) {
  return (
    <div className="my-2 rounded-lg border border-sage/30 bg-sage/5">
      <div className="flex items-center gap-2 px-3 py-2 text-sm">
        <Loader2 className="h-4 w-4 animate-spin text-sage" />
        <Wrench className="h-4 w-4 text-sage" />
        <span className="font-medium text-ink">{getToolDisplayName(name)}</span>
      </div>
    </div>
  );
}

type ToolCallResultProps = {
  name: string;
  input?: unknown;
  output?: unknown;
};

/**
 * Displays a completed tool call with expandable input/output.
 */
function ToolCallResult({ name }: ToolCallResultProps) {
  return (
    <div className="my-2 rounded-lg border border-sage/30 bg-sage/10">
      <div className="flex items-center gap-2 px-3 py-2 text-sm">
        <CheckCircle2 className="h-4 w-4 text-sage" />
        <Wrench className="h-4 w-4 text-sage" />
        <span className="font-medium text-ink">{getToolDisplayName(name)}</span>
        <span className="text-xs text-stone">completed</span>
      </div>
    </div>
  );
}

type ContentFetchedBlockProps = {
  source?: string;
  content?: string;
};

/**
 * Displays RAG content retrieval information.
 */
function ContentFetchedBlock({ source, content }: ContentFetchedBlockProps) {
  const [isExpanded, setIsExpanded] = useState(false);

  return (
    <div className="my-2 rounded-lg border border-stone/20 bg-mist/30">
      <button
        type="button"
        onClick={() => content && setIsExpanded(!isExpanded)}
        disabled={!content}
        className={`flex w-full items-center gap-2 px-3 py-2 text-left text-sm ${
          content ? "cursor-pointer hover:bg-mist/50" : "cursor-default"
        }`}
      >
        <span className="text-xs text-stone">📄 Retrieved content</span>
        {source && <span className="truncate text-xs font-medium text-ink">{source}</span>}
        {content &&
          (isExpanded ? (
            <ChevronDown className="ml-auto h-4 w-4 text-stone" />
          ) : (
            <ChevronRight className="ml-auto h-4 w-4 text-stone" />
          ))}
      </button>
      {isExpanded && content && (
        <div className="border-t border-stone/10 px-3 py-2">
          <pre className="whitespace-pre-wrap text-xs text-stone/80">{content}</pre>
        </div>
      )}
    </div>
  );
}

