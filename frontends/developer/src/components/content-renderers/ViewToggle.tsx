import { cn } from "@/lib/utils";
import type { ViewMode } from "./useContentViewMode";

interface ViewToggleProps {
  mode: ViewMode;
  onChange: (mode: ViewMode) => void;
}

/**
 * Toggle component to switch between rendered view and raw JSON view.
 */
export function ViewToggle({ mode, onChange }: ViewToggleProps) {
  return (
    <div className="console-segmented text-xs">
      <button
        type="button"
        onClick={() => onChange("rendered")}
        className={cn(
          "rounded-md px-2.5 py-1 transition-colors",
          mode === "rendered"
            ? "bg-sage-soft text-primary"
            : "text-muted-foreground hover:bg-sage-soft/40",
        )}
      >
        Rendered
      </button>
      <button
        type="button"
        onClick={() => onChange("raw")}
        className={cn(
          "rounded-md px-2.5 py-1 transition-colors",
          mode === "raw"
            ? "bg-sage-soft text-primary"
            : "text-muted-foreground hover:bg-sage-soft/40",
        )}
      >
        JSON
      </button>
    </div>
  );
}

// Made with Bob
