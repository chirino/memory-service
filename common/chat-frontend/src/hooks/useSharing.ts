import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { SharingService } from "@/client";
import type { AccessLevel, ConversationMembership, OwnershipTransfer } from "@/client";

// Response types for the API calls
type MembershipsResponse = {
  data?: ConversationMembership[];
};

type TransfersResponse = {
  data?: OwnershipTransfer[];
};

/**
 * Hook to fetch conversation memberships
 */
export function useMemberships(conversationId: string | null) {
  return useQuery({
    queryKey: ["memberships", conversationId],
    enabled: Boolean(conversationId),
    queryFn: async (): Promise<ConversationMembership[]> => {
      if (!conversationId) return [];
      const response = (await SharingService.listConversationMemberships({
        conversationId,
      })) as unknown as MembershipsResponse;
      return response.data ?? [];
    },
  });
}

/**
 * Hook to share a conversation with a new user
 */
export function useShareConversation() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({
      conversationId,
      userId,
      accessLevel,
    }: {
      conversationId: string;
      userId: string;
      accessLevel: AccessLevel;
    }) => {
      return SharingService.shareConversation({
        conversationId,
        requestBody: { userId, accessLevel },
      });
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["memberships", variables.conversationId],
      });
    },
  });
}

/**
 * Hook to update a member's access level
 */
export function useUpdateMembership() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({
      conversationId,
      userId,
      accessLevel,
    }: {
      conversationId: string;
      userId: string;
      accessLevel: AccessLevel;
    }) => {
      return SharingService.updateConversationMembership({
        conversationId,
        userId,
        requestBody: { accessLevel },
      });
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["memberships", variables.conversationId],
      });
    },
  });
}

/**
 * Hook to remove a member from a conversation
 */
export function useRemoveMembership() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ conversationId, userId }: { conversationId: string; userId: string }) => {
      return SharingService.deleteConversationMembership({
        conversationId,
        userId,
      });
    },
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: ["memberships", variables.conversationId],
      });
    },
  });
}

/**
 * Hook to list pending ownership transfers
 */
export function usePendingTransfers(role?: "sender" | "recipient" | "all") {
  return useQuery({
    queryKey: ["pending-transfers", role],
    queryFn: async (): Promise<OwnershipTransfer[]> => {
      const response = (await SharingService.listPendingTransfers({
        role,
      })) as unknown as TransfersResponse;
      return response.data ?? [];
    },
  });
}

/**
 * Hook to get a pending transfer for a specific conversation
 */
export function usePendingTransferForConversation(conversationId: string | null) {
  const transfersQuery = usePendingTransfers();

  const pendingTransfer = transfersQuery.data?.find((t) => t.conversationId === conversationId);

  return {
    ...transfersQuery,
    data: pendingTransfer ?? null,
  };
}

/**
 * Hook to create an ownership transfer
 */
export function useCreateTransfer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ conversationId, newOwnerUserId }: { conversationId: string; newOwnerUserId: string }) => {
      return SharingService.createOwnershipTransfer({
        requestBody: { conversationId, newOwnerUserId },
      });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["pending-transfers"],
      });
    },
  });
}

/**
 * Hook to accept an ownership transfer
 */
export function useAcceptTransfer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ transferId }: { transferId: string }) => {
      return SharingService.acceptTransfer({ transferId });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["pending-transfers"],
      });
      // Also invalidate memberships since ownership changed
      void queryClient.invalidateQueries({
        queryKey: ["memberships"],
      });
      // Invalidate conversations list as ownership may have changed
      void queryClient.invalidateQueries({
        queryKey: ["conversations"],
      });
    },
  });
}

/**
 * Hook to delete (cancel or decline) an ownership transfer
 */
export function useDeleteTransfer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ transferId }: { transferId: string }) => {
      return SharingService.deleteTransfer({ transferId });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["pending-transfers"],
      });
    },
  });
}

/**
 * Utility to determine if a user can manage members
 */
export function canManageMembers(accessLevel: AccessLevel | undefined): boolean {
  return accessLevel === "owner" || accessLevel === "manager";
}

/**
 * Utility to determine if a user can transfer ownership
 */
export function canTransferOwnership(accessLevel: AccessLevel | undefined): boolean {
  return accessLevel === "owner";
}

/**
 * Utility to determine what access levels a user can assign to others
 * Managers can only assign writer/reader
 * Owners can assign any level except owner
 */
export function getAssignableAccessLevels(currentUserAccessLevel: AccessLevel | undefined): AccessLevel[] {
  if (currentUserAccessLevel === "owner") {
    return ["manager", "writer", "reader"];
  }
  if (currentUserAccessLevel === "manager") {
    return ["writer", "reader"];
  }
  return [];
}

/**
 * Utility to determine if a user can modify another member's access
 */
export function canModifyMember(
  currentUserAccessLevel: AccessLevel | undefined,
  targetMemberAccessLevel: AccessLevel | undefined,
): boolean {
  if (!currentUserAccessLevel || !targetMemberAccessLevel) return false;

  // Can't modify yourself through this mechanism
  // Owner can modify anyone except other owners (there's only one owner)
  if (currentUserAccessLevel === "owner") {
    return targetMemberAccessLevel !== "owner";
  }

  // Manager can only modify writers and readers
  if (currentUserAccessLevel === "manager") {
    return targetMemberAccessLevel === "writer" || targetMemberAccessLevel === "reader";
  }

  return false;
}
