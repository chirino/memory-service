import { useEffect, useRef, useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getAccessToken } from "@/lib/auth";

interface SSEEvent {
  event: string;
  kind: string;
  data: Record<string, unknown>;
}

// Module-level set of conversation IDs with in-progress responses.
// Mutated by SSE events; components observe via the "streaming-conversations" query.
const streamingConversations = new Set<string>();

/**
 * useEventStream subscribes to the memory service SSE event stream
 * and invalidates React Query caches when relevant events arrive.
 */
export function useEventStream() {
  const queryClient = useQueryClient();
  const controllerRef = useRef<AbortController | null>(null);

  const handleEvent = useCallback(
    (msg: SSEEvent) => {
      switch (msg.kind) {
        case "conversation":
          queryClient.invalidateQueries({ queryKey: ["conversations"] });
          if (msg.event === "created") {
            queryClient.invalidateQueries({ queryKey: ["conversation-sidebar-children"] });
          }
          if (msg.data?.conversation) {
            queryClient.invalidateQueries({
              queryKey: ["conversation", msg.data.conversation],
            });
          }
          break;
        case "entry":
          if (msg.data?.conversation) {
            queryClient.invalidateQueries({
              queryKey: ["conversation-path-messages", msg.data.conversation],
            });
          }
          break;
        case "response": {
          const conversationId = msg.data?.conversation as string | undefined;
          if (conversationId) {
            queryClient.invalidateQueries({
              queryKey: ["conversation", conversationId],
            });

            // Track response in-progress state.
            if (msg.event === "started") {
              streamingConversations.add(conversationId);
              queryClient.setQueryData<Set<string>>(["streaming-conversations"], new Set(streamingConversations));
            } else if (msg.event === "completed" || msg.event === "failed") {
              streamingConversations.delete(conversationId);
              queryClient.setQueryData<Set<string>>(["streaming-conversations"], new Set(streamingConversations));
              // Optimistically remove from all resume-check queries so the
              // spinner stops immediately without waiting for a refetch.
              queryClient.setQueriesData<string[]>({ queryKey: ["resume-check"] }, (old) =>
                old?.filter((id) => id !== conversationId),
              );
              // Refetch entries now that the response is done.
              queryClient.invalidateQueries({
                queryKey: ["conversation-path-messages", conversationId],
              });
            }
          }
          break;
        }
        case "membership":
          queryClient.invalidateQueries({ queryKey: ["conversations"] });
          break;
        case "stream":
          if (msg.event === "invalidate") {
            // Server signals possible missed events — broad cache refresh.
            queryClient.invalidateQueries();
          }
          break;
      }
    },
    [queryClient],
  );

  useEffect(() => {
    const token = getAccessToken();
    if (!token) return;

    const controller = new AbortController();
    controllerRef.current = controller;

    const connect = () => {
      const headers: Record<string, string> = {
        Accept: "text/event-stream",
      };
      const currentToken = getAccessToken();
      if (currentToken) {
        headers["Authorization"] = `Bearer ${currentToken}`;
      }

      fetch("/v1/events", {
        headers,
        signal: controller.signal,
      })
        .then(async (response) => {
          if (!response.ok || !response.body) {
            if (!controller.signal.aborted) {
              queryClient.invalidateQueries(); // heal missed events on failed reconnect
              setTimeout(connect, 5000);
            }
            return;
          }

          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";

          while (true) {
            const { done, value } = await reader.read();
            if (done) break;

            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split("\n");
            buffer = lines.pop() ?? "";

            for (const line of lines) {
              if (!line || line.startsWith(":")) continue; // skip empty lines and comments (keepalive)
              if (line.startsWith("data:")) {
                const json = line.startsWith("data: ") ? line.slice(6) : line.slice(5);
                try {
                  const msg: SSEEvent = JSON.parse(json);
                  handleEvent(msg);
                } catch {
                  // ignore malformed events
                }
              }
            }
          }

          // Stream ended — reconnect after brief delay.
          if (!controller.signal.aborted) {
            queryClient.invalidateQueries(); // heal missed events
            setTimeout(connect, 2000);
          }
        })
        .catch(() => {
          // Network error — reconnect with backoff.
          if (!controller.signal.aborted) {
            setTimeout(connect, 5000);
          }
        });
    };

    connect();

    // Also refetch on window refocus.
    const onFocus = () => queryClient.invalidateQueries();
    window.addEventListener("focus", onFocus);

    return () => {
      controller.abort();
      controllerRef.current = null;
      window.removeEventListener("focus", onFocus);
    };
  }, [handleEvent, queryClient]);
}

/**
 * Returns a reactive Set of conversation IDs that currently have
 * an in-progress response (between started and completed/failed events).
 */
export function useStreamingConversations(): Set<string> {
  const { data } = useQuery<Set<string>>({
    queryKey: ["streaming-conversations"],
    queryFn: () => new Set(streamingConversations),
    staleTime: Infinity, // only updated via setQueryData from SSE events
  });
  return data ?? new Set();
}
