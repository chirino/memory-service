/**
 * Returns the URL only when it resolves to http: or https:; relative URLs
 * resolve against the current origin. Attachment hrefs and server-returned
 * download URLs end up in window.open, <img src>, and download anchors, so
 * javascript:, data:, and other schemes must be rejected.
 */
export function safeAttachmentUrl(rawUrl: string | undefined): string | undefined {
  if (!rawUrl) return undefined;
  try {
    const parsed = new URL(rawUrl, window.location.origin);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return undefined;
    }
    return parsed.toString();
  } catch {
    return undefined;
  }
}

/**
 * Returns true for image content types that may be previewed inline.
 * image/svg+xml is excluded because SVG can carry script.
 */
export function isInlineImageContentType(contentType: string | undefined): boolean {
  const normalized = contentType?.toLowerCase().split(";")[0]?.trim();
  return normalized?.split("/")[0] === "image" && normalized !== "image/svg+xml";
}
