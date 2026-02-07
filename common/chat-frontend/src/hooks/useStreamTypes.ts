// Rich event types from the backend stream
export type StreamEvent =
  | { eventType: "PartialResponse"; chunk: string }
  | { eventType: "PartialThinking"; chunk: string }
  | { eventType: "BeforeToolExecution"; toolName: string; input?: unknown }
  | { eventType: "ToolExecuted"; toolName: string; input?: unknown; output?: unknown }
  | { eventType: "ContentFetched"; source?: string; content?: string }
  | { eventType: "IntermediateResponse"; chunk?: string }
  | { eventType: "ChatCompleted"; finishReason?: string };

export type StreamAttachmentRef = {
  attachmentId: string;
};

export type StreamStartParams = {
  sessionId: string;
  text: string;
  resetResume: boolean;
  attachments?: StreamAttachmentRef[];
  onChunk: (chunk: string) => void;
  onEvent?: (event: StreamEvent) => void;
  onReplayFailed: () => void;
  onCleanEnd: () => void;
  onError?: (error: unknown) => void;
};

export interface StreamClient {
  start: (params: StreamStartParams) => void;
  close: () => void;
}
