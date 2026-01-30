import { useEffect, useRef } from "react";
import { X, Crown } from "lucide-react";

type TransferConfirmModalProps = {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: () => void;
  recipientUserId: string;
  isLoading?: boolean;
};

export function TransferConfirmModal({
  isOpen,
  onClose,
  onConfirm,
  recipientUserId,
  isLoading = false,
}: TransferConfirmModalProps) {
  const modalRef = useRef<HTMLDivElement>(null);

  // Close on escape key
  useEffect(() => {
    function handleEscape(event: KeyboardEvent) {
      if (event.key === "Escape" && !isLoading) {
        onClose();
      }
    }

    if (isOpen) {
      document.addEventListener("keydown", handleEscape);
      return () => {
        document.removeEventListener("keydown", handleEscape);
      };
    }
  }, [isOpen, isLoading, onClose]);

  // Close when clicking outside
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (
        modalRef.current &&
        !modalRef.current.contains(event.target as Node) &&
        !isLoading
      ) {
        onClose();
      }
    }

    if (isOpen) {
      document.addEventListener("mousedown", handleClickOutside);
      return () => {
        document.removeEventListener("mousedown", handleClickOutside);
      };
    }
  }, [isOpen, isLoading, onClose]);

  // Focus trap
  useEffect(() => {
    if (isOpen && modalRef.current) {
      const focusableElements = modalRef.current.querySelectorAll(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      );
      const firstElement = focusableElements[0] as HTMLElement;
      firstElement?.focus();
    }
  }, [isOpen]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-ink/40" aria-hidden="true" />

      {/* Modal */}
      <div
        ref={modalRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="transfer-modal-title"
        className="relative z-10 w-full max-w-md animate-slide-up rounded-2xl border border-stone/20 bg-cream p-6 shadow-2xl"
      >
        {/* Close button */}
        <button
          type="button"
          onClick={onClose}
          disabled={isLoading}
          className="absolute right-4 top-4 rounded p-1 text-stone transition-colors hover:bg-mist hover:text-ink disabled:opacity-50"
          aria-label="Close"
        >
          <X className="h-5 w-5" />
        </button>

        {/* Icon */}
        <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-terracotta/10">
          <Crown className="h-6 w-6 text-terracotta" />
        </div>

        {/* Content */}
        <h2
          id="transfer-modal-title"
          className="font-serif text-xl text-ink"
        >
          Transfer ownership
        </h2>

        <p className="mt-3 text-sm text-stone">
          Transfer ownership to{" "}
          <span className="font-medium text-ink">{recipientUserId}</span>?
        </p>

        <div className="mt-4 rounded-lg bg-mist/50 p-3">
          <ul className="space-y-1.5 text-xs text-stone">
            <li>They will need to accept the transfer.</li>
            <li>You will become a Manager after they accept.</li>
          </ul>
        </div>

        {/* Actions */}
        <div className="mt-6 flex items-center justify-end gap-3">
          <button
            type="button"
            onClick={onClose}
            disabled={isLoading}
            className="rounded-lg px-4 py-2.5 text-sm font-medium text-stone transition-colors hover:bg-mist hover:text-ink disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={isLoading}
            className="rounded-lg bg-terracotta px-4 py-2.5 text-sm font-medium text-cream transition-colors hover:bg-terracotta/90 disabled:opacity-50"
          >
            {isLoading ? "Requesting..." : "Request Transfer"}
          </button>
        </div>
      </div>
    </div>
  );
}
