import { useCallback, useMemo, useRef } from "react";
import type { StreamClient, StreamStartParams } from "./useStreamTypes";

export function useWebSocketStream(): StreamClient {
  const socketRef = useRef<WebSocket | null>(null);
  const closedByClientRef = useRef(false);

  const close = useCallback(() => {
    const socket = socketRef.current;
    if (socket) {
      closedByClientRef.current = true;
      socket.close();
      socketRef.current = null;
    } else {
      closedByClientRef.current = false;
    }
  }, []);

  const start = useCallback(
    (params: StreamStartParams) => {
      const { sessionId, text, resumePosition, resetResume, onChunk, onReplayFailed, onCleanEnd } = params;
      close();

      const protocol = window.location.protocol === "https:" ? "wss" : "ws";

      // Determine if this is a resume operation
      // Resume: text is empty, resumePosition is 0, and resetResume is false
      const isResume = !text && resumePosition === 0 && !resetResume;

      let url: string;
      if (isResume) {
        // Resume WebSocket: /customer-support-agent/{conversationId}/ws/{resumePosition}
        url = `${protocol}://${window.location.host}/customer-support-agent/${encodeURIComponent(
          sessionId,
        )}/ws/${resumePosition}`;
      } else {
        // Normal chat WebSocket: /customer-support-agent/{conversationId}/ws
        url = `${protocol}://${window.location.host}/customer-support-agent/${encodeURIComponent(sessionId)}/ws`;
      }

      const socket = new WebSocket(url);
      socketRef.current = socket;
      closedByClientRef.current = false;
      let receivedAny = false;

      socket.onopen = () => {
        if (isResume) {
          // Resume WebSocket: server will send tokens and close automatically
          // No need to send anything
        } else if (text) {
          // Normal chat: send user message
          socket.send(text);
        }
      };

      socket.onmessage = (event: MessageEvent) => {
        receivedAny = true;
        onChunk(String(event.data ?? ""));
      };

      const handleSocketClosure = (event?: CloseEvent | Event) => {
        if (socketRef.current === socket) {
          socketRef.current = null;
        }
        if (closedByClientRef.current) {
          closedByClientRef.current = false;
          return;
        }
        if (isResume && !receivedAny) {
          // Resume WebSocket closed without sending any tokens
          onReplayFailed();
          return;
        }
        const cleanClose = event instanceof CloseEvent ? event.wasClean || event.code === 1000 : false;
        if (cleanClose) {
          onCleanEnd();
        } else {
        }
      };

      socket.onerror = handleSocketClosure;
      socket.onclose = handleSocketClosure;
    },
    [close],
  );

  return useMemo(() => ({ start, close }), [start, close]);
}
