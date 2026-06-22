// Content Renderer Registry
// Maps content types to their specialized renderer components

import type { ComponentType } from "react";
import { HistoryRenderer } from "./HistoryRenderer";
import { JsonRenderer } from "./JsonRenderer";

export type ContentRendererProps = {
	content: unknown[];
	contentType: string;
};

type RendererComponent = ComponentType<ContentRendererProps>;

// Registry mapping content types to their renderers
const rendererRegistry: Record<string, RendererComponent> = {
	history: HistoryRenderer,
	"history/lc4j": HistoryRenderer,
};

/**
 * Get the renderer component for a given content type.
 * Returns JsonRenderer if no specialized renderer exists.
 */
export function getRenderer(contentType: string): RendererComponent {
	return rendererRegistry[contentType] ?? JsonRenderer;
}

/**
 * Check if a content type has a custom renderer.
 */
export function hasCustomRenderer(contentType: string): boolean {
	return contentType in rendererRegistry;
}

// Export components
export { ContentRenderer } from "./ContentRenderer";
export { HistoryRenderer } from "./HistoryRenderer";
export { JsonRenderer } from "./JsonRenderer";
export type { ViewMode } from "./useContentViewMode";
export { useContentViewMode } from "./useContentViewMode";
export { ViewToggle } from "./ViewToggle";

// Made with Bob