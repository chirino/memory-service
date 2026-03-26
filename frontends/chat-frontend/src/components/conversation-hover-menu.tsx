import { Archive, ArchiveRestore } from "lucide-react";

type ConversationHoverMenuProps = {
  isArchived?: boolean;
  onArchive?: () => void;
  onUnarchive?: () => void;
};

export function ConversationHoverMenu({ isArchived, onArchive, onUnarchive }: ConversationHoverMenuProps) {
  return (
    <div className="pointer-events-none absolute right-2 top-2 flex gap-1 opacity-0 transition-opacity group-hover:opacity-100">
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          if (isArchived) {
            onUnarchive?.();
          } else {
            onArchive?.();
          }
        }}
        className={`pointer-events-auto rounded-lg border border-stone/10 bg-cream/90 p-1.5 text-stone transition-colors ${
          isArchived ? "hover:border-sage/30 hover:text-sage" : "hover:border-terracotta/30 hover:text-terracotta"
        }`}
        title={isArchived ? "Unarchive" : "Archive"}
      >
        {isArchived ? <ArchiveRestore className="h-3.5 w-3.5" /> : <Archive className="h-3.5 w-3.5" />}
      </button>
    </div>
  );
}
