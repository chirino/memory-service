import { createElement } from "react";
import { cn } from "@/lib/utils";
import { getRenderer, hasCustomRenderer } from "./index";
import { JsonRenderer } from "./JsonRenderer";
import type { ViewMode } from "./useContentViewMode";

interface ContentRendererProps {
  content: unknown[];
  contentType: string;
  /** Optional view mode - if provided, the parent controls the toggle */
  viewMode?: ViewMode;
}

/**
 * Main wrapper component that delegates to the appropriate content type renderer.
 *
 * Includes wrapper styling:
 * - JSON mode: console-code rounded-lg p-3 (code block appearance)
 * - Rendered mode: no background (clean appearance for custom renderers)
 *
 * @param viewMode - When provided, controls which view to show. When omitted,
 *                   defaults to 'rendered' for custom renderers or JSON for unknown types.
 */
export function ContentRenderer({
  content,
  contentType,
  viewMode,
}: ContentRendererProps) {
  const hasCustom = hasCustomRenderer(contentType);
  const isRawMode = viewMode === "raw";

  // If no custom renderer exists, always show JSON with code block styling
  if (!hasCustom) {
    return (
      <div className="console-code overflow-x-auto rounded-lg p-3">
        <JsonRenderer content={content} contentType={contentType} />
      </div>
    );
  }

  return (
    <div
      className={cn(
        "overflow-x-auto",
        isRawMode ? "console-code rounded-lg p-3" : "",
      )}
    >
      {isRawMode ? (
        <JsonRenderer content={content} contentType={contentType} />
      ) : (
        createElement(getRenderer(contentType), { content, contentType })
      )}
    </div>
  );
}

// Made with Bob
