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
      const { sessionId, text, resetResume, onChunk, onReplayFailed, onCleanEnd, onError } = params;
      close();

      const protocol = window.location.protocol === "https:" ? "wss" : "ws";

      // Determine if this is a resume operation
      // Resume: text is empty and resetResume is false
      const isResume = !text && !resetResume;

      let url: string;
      if (isResume) {
        // Resume WebSocket: /customer-support-agent/{conversationId}/ws/resume
        url = `${protocol}://${window.location.host}/customer-support-agent/${encodeURIComponent(
          sessionId,
        )}/ws/resume`;
      } else {
        // Normal chat WebSocket: /customer-support-agent/{conversationId}/ws
        url = `${protocol}://${window.location.host}/customer-support-agent/${encodeURIComponent(sessionId)}/ws`;
      }

      let socket: WebSocket;
      try {
        socket = new WebSocket(url);
      } catch (error) {
        // WebSocket constructor can throw (e.g., invalid URL)
        onError?.(error);
        return;
      }

      socketRef.current = socket;
      closedByClientRef.current = false;
      let receivedAny = false;

      socket.onopen = () => {
        if (isResume) {
          // Resume WebSocket: server will send tokens and close automatically
          // No need to send anything
        } else if (text) {
          // Normal chat: send user message
          try {
            socket.send(text);
          } catch (error) {
            // send() can throw if socket is not in OPEN state
            onError?.(error);
          }
        }
      };

      socket.onmessage = (event: MessageEvent) => {
        receivedAny = true;
        onChunk(String(event.data ?? ""));
      };

      const handleSocketError = (event: Event) => {
        // WebSocket error event - call onError callback
        const error = event instanceof ErrorEvent ? event.error : new Error("WebSocket error");
        onError?.(error);
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
        const closeEvent = event instanceof CloseEvent ? event : null;
        const cleanClose = closeEvent ? closeEvent.wasClean || closeEvent.code === 1000 : false;
        if (cleanClose) {
          onCleanEnd();
        } else if (closeEvent) {
          // Unclean close - treat as error if we have onError callback
          // Otherwise fall through to onCleanEnd for backwards compatibility
          if (onError) {
            const error = new Error(
              `WebSocket closed unexpectedly: code ${closeEvent.code}, reason: ${closeEvent.reason || "unknown"}`,
            );
            onError(error);
          } else {
            onCleanEnd();
          }
        }
      };

      socket.onerror = handleSocketError;
      socket.onclose = handleSocketClosure;
    },
    [close],
  );

  return useMemo(() => ({ start, close }), [start, close]);
}
