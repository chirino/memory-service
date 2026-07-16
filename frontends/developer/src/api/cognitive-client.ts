// Cognitive Memory API Client
// Separate client for the cognitive-memory service
// Base URL is configured at runtime via config.json (cognitiveApiUrl)
// Falls back to empty string (relative URLs) if not configured

import { getConfig } from "../lib/config";

let cognitiveBaseUrl: string | null = null;

async function getCognitiveBaseUrl(): Promise<string> {
  if (cognitiveBaseUrl === null) {
    const config = await getConfig();
    cognitiveBaseUrl = config.cognitiveApiUrl || "";
  }
  return cognitiveBaseUrl;
}

export interface CognitiveProcess {
  id: string;
  displayName: string;
  description: string;
  state: "ENABLED" | "DISABLED";
}

export interface CognitiveProcessDetails extends CognitiveProcess {
  details: {
    mode?: string;
    lastRunTime?: string;
    lastRunStatus?: string;
    lastRunUserId?: string;
    eventStreamConnected?: boolean;
    eventsAccepted?: number;
    activeWindows?: number;
    totalQueues?: number;
    activeQueues?: number;
    pendingJobs?: number;
    resourceTypes?: Record<string, {
      type: string;
      provider?: string;
      modelName?: string;
      model?: string;
      endpoint?: string;
      prompt?: string;
    }>;
  };
}

export async function fetchCognitiveProcesses(): Promise<CognitiveProcess[]> {
  const baseUrl = await getCognitiveBaseUrl();
  const response = await fetch(`${baseUrl}/api/processes/`);
  if (!response.ok) {
    throw new Error(`Failed to fetch processes: ${response.statusText}`);
  }
  return response.json();
}

export async function fetchCognitiveProcessDetails(processId: string): Promise<CognitiveProcessDetails> {
  const baseUrl = await getCognitiveBaseUrl();
  const response = await fetch(`${baseUrl}/api/processes/${processId}`);
  if (!response.ok) {
    throw new Error(`Failed to fetch process details: ${response.statusText}`);
  }
  return response.json();
}

// Made with Bob
