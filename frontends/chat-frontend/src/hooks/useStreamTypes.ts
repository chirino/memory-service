import type { ChatEvent } from "@/components/conversation";

// Rich event types from the backend stream
export type StreamEvent = ChatEvent;

export type StreamAttachmentRef = {
  attachmentId: string;
};

export type StreamStartParams = {
  sessionId: string;
  text: string;
  resetResume: boolean;
  attachments?: StreamAttachmentRef[];
  forkedAtConversationId?: string;
  forkedAtEntryId?: string;
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
