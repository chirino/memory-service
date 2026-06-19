import { cn } from "@/lib/utils";

type ViewMode = "rendered" | "raw";

interface ViewToggleProps {
  mode: ViewMode;
  onChange: (mode: ViewMode) => void;
}

/**
 * Toggle component to switch between rendered view and raw JSON view.
 */
export function ViewToggle({ mode, onChange }: ViewToggleProps) {
  return (
    <div className="flex items-center gap-1 text-xs">
      <button
        type="button"
        onClick={() => onChange("rendered")}
        className={cn(
          "px-2 py-1 rounded transition-colors",
          mode === "rendered"
            ? "bg-primary text-primary-foreground"
            : "bg-muted text-muted-foreground hover:bg-muted/80",
        )}
      >
        Rendered
      </button>
      <button
        type="button"
        onClick={() => onChange("raw")}
        className={cn(
          "px-2 py-1 rounded transition-colors",
          mode === "raw"
            ? "bg-primary text-primary-foreground"
            : "bg-muted text-muted-foreground hover:bg-muted/80",
        )}
      >
        JSON
      </button>
    </div>
  );
}

// Made with Bob