import { afterEach, describe, expect, it } from "vitest";
import { clearCredentials, getAuthHeaders, setAccessToken, setApiKey } from "./client";

describe("developer API credentials", () => {
  afterEach(clearCredentials);

  it("sends only X-API-Key in local developer mode", () => {
    setAccessToken("oidc-token");
    setApiKey("developer-key");

    const headers = getAuthHeaders();

    expect(headers.get("X-API-Key")).toBe("developer-key");
    expect(headers.get("Authorization")).toBeNull();
    expect(headers.get("X-User-ID")).toBeNull();
  });

  it("sends only a bearer token in OIDC mode", () => {
    setApiKey("developer-key");
    setAccessToken("oidc-token");

    const headers = getAuthHeaders();

    expect(headers.get("Authorization")).toBe("Bearer oidc-token");
    expect(headers.get("X-API-Key")).toBeNull();
    expect(headers.get("X-User-ID")).toBeNull();
  });
});
