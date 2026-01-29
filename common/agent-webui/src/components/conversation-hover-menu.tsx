import { Sparkles, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";

type ConversationHoverMenuProps = {
  onIndex?: () => void;
  onDelete?: () => void;
};

export function ConversationHoverMenu({ onIndex, onDelete }: ConversationHoverMenuProps) {
  return (
    <div className="pointer-events-none absolute right-2 top-1 flex gap-1 opacity-0 transition-opacity duration-150 group-focus-within:opacity-100 group-hover:opacity-100">
      <div className="pointer-events-auto rounded-md border border-border/60 bg-popover/90 p-0.5 shadow-lg backdrop-blur-md transition-colors duration-150">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={(event) => {
            event.stopPropagation();
            onIndex?.();
          }}
          className="h-7 px-2 text-[11px]"
          aria-label="Index conversation"
        >
          <Sparkles className="h-3 w-3" />
          Index
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={(event) => {
            event.stopPropagation();
            onDelete?.();
          }}
          className="h-7 px-2 text-[11px]"
          aria-label="Delete conversation"
        >
          <Trash2 className="h-3 w-3" />
          Delete
        </Button>
      </div>
    </div>
  );
}
