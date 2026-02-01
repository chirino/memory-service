import { useCallback, useMemo, useRef } from "react";
import type { StreamClient, StreamStartParams } from "./useStreamTypes";
import { getAccessToken } from "@/lib/auth";

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
      // Determine if this is a resume operation
      // Resume: text is empty and resetResume is false
      const isResume = !trimmedMessage && !params.resetResume;

      if (!isResume && !trimmedMessage) {
        params.onError?.(new Error("SSE stream requires a message"));
        return;
      }

      close();
      const controller = new AbortController();
      controllerRef.current = controller;
      closedByClientRef.current = false;

      const run = async () => {
        let receivedAnyChunks = false;

        try {
          let url: string;
          let fetchOptions: RequestInit;

          // Build headers with optional Authorization
          const baseHeaders: Record<string, string> = {
            Accept: "text/event-stream",
          };
          const token = getAccessToken();
          if (token) {
            baseHeaders["Authorization"] = `Bearer ${token}`;
          }

          if (isResume) {
            // Resume SSE: GET /v1/conversations/{conversationId}/resume
            url = `/v1/conversations/${encodeURIComponent(params.sessionId)}/resume`;
            fetchOptions = {
              method: "GET",
              headers: baseHeaders,
              signal: controller.signal,
            };
          } else {
            // Normal chat SSE: POST /v1/conversations/{conversationId}/chat
            url = `/v1/conversations/${encodeURIComponent(params.sessionId)}/chat`;
            fetchOptions = {
              method: "POST",
              headers: {
                ...baseHeaders,
                "Content-Type": "application/json",
              },
              body: JSON.stringify({ message: trimmedMessage }),
              signal: controller.signal,
            };
          }

          const response = await fetch(url, fetchOptions);

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
                  receivedAnyChunks = true;
                  params.onChunk(parsed);
                  return;
                }
                if (parsed && typeof parsed.token === "string") {
                  receivedAnyChunks = true;
                  params.onChunk(parsed.token);
                  return;
                }
              } catch {
                // fall through to handle raw payload
              }
              receivedAnyChunks = true;
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

          // If this was a resume and we received no chunks, call onReplayFailed
          if (isResume && !receivedAnyChunks) {
            params.onReplayFailed();
            return;
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
