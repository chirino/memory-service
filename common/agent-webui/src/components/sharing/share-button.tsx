import { useState } from "react";
import { Users } from "lucide-react";
import { ShareModal } from "./share-modal";

type ShareButtonProps = {
  conversationId: string | null;
  conversationTitle?: string | null;
  currentUserId: string;
  disabled?: boolean;
};

export function ShareButton({
  conversationId,
  conversationTitle,
  currentUserId,
  disabled = false,
}: ShareButtonProps) {
  const [isModalOpen, setIsModalOpen] = useState(false);

  const handleClick = () => {
    if (conversationId) {
      setIsModalOpen(true);
    }
  };

  return (
    <>
      <button
        type="button"
        onClick={handleClick}
        disabled={disabled || !conversationId}
        className="flex items-center gap-2 rounded-lg border border-stone/20 px-3 py-2 text-sm text-stone transition-colors hover:bg-mist hover:text-ink disabled:opacity-50"
        title="Share conversation"
      >
        <Users className="h-4 w-4" />
        <span>Share</span>
      </button>

      {conversationId && (
        <ShareModal
          isOpen={isModalOpen}
          onClose={() => setIsModalOpen(false)}
          conversationId={conversationId}
          conversationTitle={conversationTitle}
          currentUserId={currentUserId}
        />
      )}
    </>
  );
}
