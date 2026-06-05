import { getAccessToken } from "@/lib/auth";

export type ProfileContextResponse = {
  exists: boolean;
  generatedAt?: string | null;
  content: string;
  sections?: Record<string, unknown> | null;
  conflicts?: unknown[] | null;
  omitted?: unknown[] | null;
};

export class ProfileContextUnsupportedError extends Error {
  constructor(path: string) {
    super(`${path} is not supported by this agent app`);
    this.name = "ProfileContextUnsupportedError";
  }
}

export class ProfileContextRequestError extends Error {
  readonly status: number;

  constructor(path: string, status: number) {
    super(`${path} failed with HTTP ${status}`);
    this.name = "ProfileContextRequestError";
    this.status = status;
  }
}

export function isProfileContextUnsupported(error: unknown): boolean {
  return error instanceof ProfileContextUnsupportedError;
}

export async function getProfileContext(): Promise<ProfileContextResponse> {
  return requestJson<ProfileContextResponse>("/v1/profile-context");
}

export async function getProfileInputs(): Promise<string[]> {
  const inputs = await requestJson<unknown>("/v1/profile-context/inputs");
  if (!Array.isArray(inputs) || inputs.some((input) => typeof input !== "string")) {
    throw new Error("Profile input response was not a string array");
  }
  return inputs;
}

export async function putProfileInputs(inputs: string[]): Promise<string[]> {
  const accepted = await requestJson<unknown>("/v1/profile-context/inputs", {
    method: "PUT",
    body: JSON.stringify(inputs),
  });
  if (!Array.isArray(accepted) || accepted.some((input) => typeof input !== "string")) {
    throw new Error("Profile input update response was not a string array");
  }
  return accepted;
}

async function requestJson<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = getAccessToken();
  const headers = new Headers(init.headers);
  headers.set("Content-Type", "application/json");
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }

  const response = await fetch(path, {
    ...init,
    headers,
  });

  if (response.status === 404) {
    throw new ProfileContextUnsupportedError(path);
  }
  if (!response.ok) {
    throw new ProfileContextRequestError(path, response.status);
  }

  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.includes("application/json")) {
    throw new Error(`${path} returned a non-JSON response`);
  }

  return (await response.json()) as T;
}
