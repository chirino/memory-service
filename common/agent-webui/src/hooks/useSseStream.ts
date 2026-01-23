import { useCallback, useMemo, useRef } from "react";
import type { StreamClient, StreamStartParams } from "./useStreamTypes";

function normalizeEventChunk(chunk: string): string {
  return chunk.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
}

export function useSseStream(): StreamClient {
  const controllerRef = useRef<AbortController | null>(null);
  const closedByClientRef = useRef(false);

  const close = useCallback(() => {
    const controller = controllerRef.current;
    if (controller) {
      closedByClientRef.current = true;
      controller.abort();
      controllerRef.current = null;
    } else {
      closedByClientRef.current = false;
    }
  }, []);

  const start = useCallback(
    (params: StreamStartParams) => {
      if (!params.sessionId) {
        params.onError?.(new Error("SSE stream requires a session id"));
        return;
      }

      const trimmedMessage = params.text?.trim();
      if (!trimmedMessage) {
        params.onError?.(new Error("SSE stream requires a message"));
        return;
      }

      close();
      const controller = new AbortController();
      controllerRef.current = controller;
      closedByClientRef.current = false;

      const run = async () => {
        try {
          const response = await fetch(
            `/customer-support-agent/${encodeURIComponent(params.sessionId)}/sse`,
            {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                Accept: "text/event-stream",
              },
              body: JSON.stringify({ message: trimmedMessage }),
              signal: controller.signal,
            },
          );

          if (!response.ok) {
            throw new Error(`SSE request failed: ${response.status} ${response.statusText}`);
          }

          const reader = response.body?.getReader();
          if (!reader) {
            throw new Error("SSE response stream is unavailable");
          }

          const decoder = new TextDecoder();
          let buffer = "";
          const processEvent = (eventChunk: string) => {
            const lines = normalizeEventChunk(eventChunk).split("\n");
            for (const line of lines) {
              if (!line.startsWith("data:")) {
                continue;
              }
              const payload = line.slice(5).trim();
              if (!payload || payload === "[DONE]") {
                return;
              }
              try {
                const parsed = JSON.parse(payload);
                if (typeof parsed === "string") {
                  params.onChunk(parsed);
                  return;
                }
                if (parsed && typeof parsed.token === "string") {
                  params.onChunk(parsed.token);
                  return;
                }
              } catch {
                // fall through to handle raw payload
              }
              params.onChunk(payload);
              return;
            }
          };

          while (true) {
            const { value, done } = await reader.read();
            if (done) {
              break;
            }
            buffer += normalizeEventChunk(decoder.decode(value, { stream: true }));
            let boundaryIndex: number;
            while ((boundaryIndex = buffer.indexOf("\n\n")) >= 0) {
              const eventChunk = buffer.slice(0, boundaryIndex);
              buffer = buffer.slice(boundaryIndex + 2);
              processEvent(eventChunk);
            }
          }

          if (buffer.trim()) {
            processEvent(buffer);
          }

          params.onCleanEnd();
        } catch (error) {
          if (controller.signal.aborted && closedByClientRef.current) {
            closedByClientRef.current = false;
            return;
          }
          params.onError?.(error);
        } finally {
          if (controllerRef.current === controller) {
            controllerRef.current = null;
          }
          closedByClientRef.current = false;
        }
      };

      void run();
    },
    [close],
  );

  return useMemo(() => ({ start, close }), [close, start]);
}
