import { useQuery } from "@tanstack/react-query";

type CurrentUserResponse = {
  userId: string;
};

/**
 * Hook to get the current authenticated user's ID.
 * Uses the /v1/me endpoint to retrieve the user ID from the backend.
 */
export function useCurrentUser() {
  return useQuery({
    queryKey: ["current-user"],
    staleTime: Infinity, // User ID won't change during session
    queryFn: async (): Promise<string | null> => {
      try {
        const response = await fetch("/v1/me", {
          credentials: "include",
        });
        if (!response.ok) {
          return null;
        }
        const data: CurrentUserResponse = await response.json();
        return data.userId ?? null;
      } catch {
        return null;
      }
    },
  });
}
