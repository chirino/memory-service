import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isInlineImageContentType, safeAttachmentUrl } from "./attachment-urls";

describe("safeAttachmentUrl", () => {
  beforeEach(() => {
    vi.stubGlobal("window", { location: { origin: "https://developer.example" } });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("allows http and https URLs", () => {
    expect(safeAttachmentUrl("http://files.example/a.png")).toBe("http://files.example/a.png");
    expect(safeAttachmentUrl("https://files.example/a.png?sig=1")).toBe("https://files.example/a.png?sig=1");
  });

  it("rejects script-capable and non-http schemes", () => {
    expect(safeAttachmentUrl("javascript:alert(1)")).toBeUndefined();
    expect(safeAttachmentUrl("JavaScript:alert(1)")).toBeUndefined();
    expect(safeAttachmentUrl("data:text/html,<script>alert(1)</script>")).toBeUndefined();
    expect(safeAttachmentUrl("data:image/png;base64,AAAA")).toBeUndefined();
    expect(safeAttachmentUrl("blob:https://developer.example/123")).toBeUndefined();
    expect(safeAttachmentUrl("ftp://files.example/a.png")).toBeUndefined();
  });

  it("resolves root-relative URLs against the current origin", () => {
    expect(safeAttachmentUrl("/v1/attachments/download/token/a.png")).toBe(
      "https://developer.example/v1/attachments/download/token/a.png",
    );
  });

  it("returns undefined for missing URLs", () => {
    expect(safeAttachmentUrl(undefined)).toBeUndefined();
    expect(safeAttachmentUrl("")).toBeUndefined();
  });
});

describe("isInlineImageContentType", () => {
  it("previews raster image types", () => {
    expect(isInlineImageContentType("image/png")).toBe(true);
    expect(isInlineImageContentType("IMAGE/JPEG; charset=binary")).toBe(true);
  });

  it("does not preview SVG inline", () => {
    expect(isInlineImageContentType("image/svg+xml")).toBe(false);
    expect(isInlineImageContentType("Image/SVG+XML; charset=utf-8")).toBe(false);
  });

  it("does not preview non-image or missing types", () => {
    expect(isInlineImageContentType("application/pdf")).toBe(false);
    expect(isInlineImageContentType("text/html")).toBe(false);
    expect(isInlineImageContentType(undefined)).toBe(false);
  });
});
