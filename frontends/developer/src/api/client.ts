import { client } from "./generated/client.gen";

// Configure the API client
// In development, Vite proxy handles /v1/* requests to localhost:8082
// In production, requests go to the same origin (handled by reverse proxy)
client.setConfig({
  baseUrl: "",
});

// Store for access token (will be set by auth context)
let accessToken = "";

export function setAccessToken(token: string) {
  accessToken = token;
}

export function getAccessToken() {
  return accessToken;
}

// Add auth token interceptor
client.interceptors.request.use((request) => {
  if (accessToken) {
    request.headers.set("Authorization", `Bearer ${accessToken}`);
  }
  return request;
});

export { client };

// Re-export generated types and query options
export * from "./generated/types.gen";
export * from "./generated/@tanstack/react-query.gen";

// Made with Bob
