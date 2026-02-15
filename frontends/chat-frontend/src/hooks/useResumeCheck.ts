import { useQuery } from "@tanstack/react-query";
import type { ApiError } from "@/client";
import { getAccessToken } from "@/lib/auth";

/**
 * Hook to check which conversations can be resumed.
 * @param conversationIds Array of conversation IDs to check
 * @returns Query result with array of conversation IDs that can be resumed
 */
export function useResumeCheck(conversationIds: string[]) {
  return useQuery<string[], ApiError, string[]>({
    queryKey: ["resume-check", conversationIds.sort().join(",")],
    queryFn: async (): Promise<string[]> => {
      if (conversationIds.length === 0) {
        return [];
      }
      // Use relative URL since ResumeResource is in the agent app
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
      };
      const token = getAccessToken();
      if (token) {
        headers["Authorization"] = `Bearer ${token}`;
      }
      const response = await fetch("/v1/conversations/resume-check", {
        method: "POST",
        headers,
        body: JSON.stringify(conversationIds),
      });

      if (!response.ok) {
        throw new Error(`Resume check failed: ${response.status} ${response.statusText}`);
      }

      const data = await response.json();
      return Array.isArray(data) ? data : [];
    },
    enabled: conversationIds.length > 0,
    staleTime: 5000, // Consider data fresh for 5 seconds
  });
}
