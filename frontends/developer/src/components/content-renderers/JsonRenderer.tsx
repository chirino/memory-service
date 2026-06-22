import { CopyButton } from "@/components/ui/copy-button";
import { JsonHighlight, formatJson } from "./JsonHighlight";
import type { ContentRendererProps } from "./index";

/**
 * Default renderer that displays content as formatted JSON with syntax highlighting.
 * Used as fallback for unknown content types.
 * Includes a copy button that appears on hover.
 */
export function JsonRenderer({ content }: ContentRendererProps) {
  return (
    <div className="relative group">
      <CopyButton
        value={formatJson(content)}
        iconSize={3.5}
        className="absolute top-0 right-0 opacity-0 group-hover:opacity-100 transition-opacity z-10"
      />
      <JsonHighlight
        value={content}
        className="text-sm whitespace-pre-wrap font-mono overflow-x-auto"
      />
    </div>
  );
}

// Made with Bob