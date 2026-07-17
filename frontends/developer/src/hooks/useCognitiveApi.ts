import { useQuery } from "@tanstack/react-query";
import { fetchCognitiveProcesses, fetchCognitiveProcessDetails } from "@/api/cognitive-client";

export function useCognitiveProcesses() {
  return useQuery({
    queryKey: ["cognitive-processes"],
    queryFn: fetchCognitiveProcesses,
    staleTime: 30000, // 30 seconds
  });
}

export function useCognitiveProcessDetails(processId: string | undefined) {
  return useQuery({
    queryKey: ["cognitive-process", processId],
    queryFn: () => fetchCognitiveProcessDetails(processId!),
    enabled: !!processId,
    staleTime: 30000, // 30 seconds
  });
}

// Made with Bob
