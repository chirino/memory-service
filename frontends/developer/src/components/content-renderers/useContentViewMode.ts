import { useState } from "react";
import { hasCustomRenderer } from "./index";

export type ViewMode = "rendered" | "raw";

/**
 * Hook to manage content view mode state.
 * Returns the current mode, setter, and whether a custom renderer exists.
 */
export function useContentViewMode(contentType: string) {
  const [viewMode, setViewMode] = useState<ViewMode>("rendered");
  const hasCustom = hasCustomRenderer(contentType);

  return {
    viewMode,
    setViewMode,
    hasCustomRenderer: hasCustom,
  };
}

// Made with Bob