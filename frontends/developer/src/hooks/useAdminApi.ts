import { useQuery, useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  adminListConversationsOptions,
  adminGetConversationOptions,
  adminGetEntriesOptions,
  adminUpdateConversationMutation,
  adminListMemoriesOptions,
  adminGetMemoryOptions,
  adminListConversations,
  adminListMemories,
  type AdminConversation,
  type AdminMemoryItem,
  type Entry,
} from "@/api/client";

// Conversations
export function useAdminConversations(params?: {
  userId?: string;
  archived?: "include" | "exclude" | "only";
  ancestry?: "all" | "roots" | "children";
  limit?: number;
  afterCursor?: string;
}) {
  return useQuery(
    adminListConversationsOptions({
      query: params,
    })
  );
}

export function useAdminConversationsInfinite(params?: {
  userId?: string;
  archived?: "include" | "exclude" | "only";
  ancestry?: "all" | "roots" | "children";
  limit?: number;
}) {
  const limit = params?.limit ?? 50;
  return useInfiniteQuery({
    queryKey: ["adminListConversations", params],
    initialPageParam: null as string | null,
    queryFn: async ({ pageParam }) => {
      const { data } = await adminListConversations({
        query: {
          ...params,
          limit,
          afterCursor: pageParam ?? undefined,
        },
        throwOnError: true,
      });
      return data;
    },
    getNextPageParam: (lastPage) => lastPage.afterCursor ?? undefined,
  });
}

export function useAdminConversation(conversationId: string) {
  return useQuery(
    adminGetConversationOptions({
      path: { id: conversationId },
    })
  );
}

export function useAdminConversationEntries(conversationId: string, params?: {
  forks?: "all" | "none";
  limit?: number;
  afterCursor?: string;
}) {
  return useQuery(
    adminGetEntriesOptions({
      path: { id: conversationId },
      query: params,
    })
  );
}

export function useArchiveConversation() {
  const queryClient = useQueryClient();
  return useMutation({
    ...adminUpdateConversationMutation(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminListConversations"] });
      queryClient.invalidateQueries({ queryKey: ["adminGetConversation"] });
    },
  });
}

export function useUnarchiveConversation() {
  const queryClient = useQueryClient();
  return useMutation({
    ...adminUpdateConversationMutation(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminListConversations"] });
      queryClient.invalidateQueries({ queryKey: ["adminGetConversation"] });
    },
  });
}

// Memories
export function useAdminMemories(params?: {
  namespacePrefix?: string[];
  keyPrefix?: string;
  limit?: number;
  afterCursor?: string;
}) {
  return useQuery(
    adminListMemoriesOptions({
      query: params,
    })
  );
}

export function useAdminMemoriesInfinite(params?: {
  namespacePrefix?: string[];
  keyPrefix?: string;
  limit?: number;
}) {
  const limit = params?.limit ?? 50;
  return useInfiniteQuery({
    queryKey: ["adminListMemories", params],
    initialPageParam: null as string | null,
    queryFn: async ({ pageParam }) => {
      const { data } = await adminListMemories({
        query: {
          ...params,
          limit,
          afterCursor: pageParam ?? undefined,
        },
        throwOnError: true,
      });
      return data;
    },
    getNextPageParam: (lastPage) => lastPage.afterCursor ?? undefined,
  });
}

export function useAdminMemory(memoryId: string) {
  return useQuery(
    adminGetMemoryOptions({
      path: { id: memoryId },
    })
  );
}

// Re-export types with aliases for backward compatibility
export type { AdminConversation, Entry as AdminConversationEntry };
export type AdminMemory = AdminMemoryItem;

// Made with Bob
