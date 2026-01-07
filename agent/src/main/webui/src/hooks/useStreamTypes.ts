export type StreamStartParams = {
  sessionId: string;
  text: string;
  resumePosition: number;
  resetResume: boolean;
  onChunk: (chunk: string) => void;
  onReplayFailed: () => void;
  onCleanEnd: () => void;
};

export interface StreamClient {
  start: (params: StreamStartParams) => void;
  close: () => void;
}
