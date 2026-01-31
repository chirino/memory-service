import { Sparkles, Trash2 } from "lucide-react";

type ConversationHoverMenuProps = {
  onIndex?: () => void;
  onDelete?: () => void;
};

export function ConversationHoverMenu({ onIndex, onDelete }: ConversationHoverMenuProps) {
  return (
    <div className="pointer-events-none absolute right-2 top-2 flex gap-1 opacity-0 transition-opacity group-hover:opacity-100">
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          onIndex?.();
        }}
        className="pointer-events-auto rounded-lg border border-stone/10 bg-cream/90 p-1.5 text-stone transition-colors hover:border-sage/30 hover:text-sage"
        title="Index conversation"
      >
        <Sparkles className="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          onDelete?.();
        }}
        className="pointer-events-auto rounded-lg border border-stone/10 bg-cream/90 p-1.5 text-stone transition-colors hover:border-terracotta/30 hover:text-terracotta"
        title="Delete conversation"
      >
        <Trash2 className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}
