import { client } from "./generated/client.gen";

// Configure the API client
// In development, Vite proxy handles /v1/* requests to localhost:8082
// In production, requests go to the same origin (handled by reverse proxy)
client.setConfig({
  baseUrl: "",
});

// Store the active browser credential (set by the auth context).
let accessToken = "";
let apiKey = "";

export function setAccessToken(token: string) {
  accessToken = token;
  apiKey = "";
}

export function setApiKey(key: string) {
  apiKey = key;
  accessToken = "";
}

export function clearCredentials() {
  accessToken = "";
  apiKey = "";
}

export function applyAuthHeaders(headers: Headers) {
  headers.delete("Authorization");
  headers.delete("X-API-Key");
  if (apiKey) {
    headers.set("X-API-Key", apiKey);
  } else if (accessToken) {
    headers.set("Authorization", `Bearer ${accessToken}`);
  }
  return headers;
}

export function getAuthHeaders() {
  return applyAuthHeaders(new Headers());
}

// Add the selected credential to generated client requests.
client.interceptors.request.use((request) => {
  applyAuthHeaders(request.headers);
  return request;
});

export { client };

// Re-export generated types and query options
export * from "./generated/types.gen";
export * from "./generated/@tanstack/react-query.gen";
export * from "./generated/sdk.gen";

// Made with Bob
