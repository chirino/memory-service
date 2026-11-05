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
    <div className="group relative">
      <CopyButton
        value={formatJson(content)}
        iconSize={3.5}
        className="absolute right-0 top-0 z-10 opacity-0 transition-opacity group-hover:opacity-100"
      />
      <JsonHighlight value={content} className="overflow-x-auto whitespace-pre-wrap font-mono text-sm" />
    </div>
  );
}

// Made with Bob
