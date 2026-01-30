import { useState, useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import { X, Users } from "lucide-react";
import type { AccessLevel } from "@/client";
import {
  useMemberships,
  useShareConversation,
  useUpdateMembership,
  useRemoveMembership,
  usePendingTransferForConversation,
  useCreateTransfer,
  useAcceptTransfer,
  useDeleteTransfer,
  canManageMembers,
  getAssignableAccessLevels,
} from "@/hooks/useSharing";
import { MemberListItem } from "./member-list-item";
import { AddMemberForm } from "./add-member-form";
import { TransferConfirmModal } from "./transfer-confirm-modal";
import { IncomingTransferBanner } from "./incoming-transfer-banner";

type ShareModalProps = {
  isOpen: boolean;
  onClose: () => void;
  conversationId: string;
  conversationTitle?: string | null;
  currentUserId: string;
};

export function ShareModal({
  isOpen,
  onClose,
  conversationId,
  conversationTitle,
  currentUserId,
}: ShareModalProps) {
  const modalRef = useRef<HTMLDivElement>(null);
  const [transferTargetUserId, setTransferTargetUserId] = useState<
    string | null
  >(null);
  const [error, setError] = useState<string | null>(null);

  // Queries
  const membershipsQuery = useMemberships(conversationId);
  const pendingTransferQuery = usePendingTransferForConversation(conversationId);

  // Mutations
  const shareConversation = useShareConversation();
  const updateMembership = useUpdateMembership();
  const removeMembership = useRemoveMembership();
  const createTransfer = useCreateTransfer();
  const acceptTransfer = useAcceptTransfer();
  const deleteTransfer = useDeleteTransfer();

  // Derive current user's access level
  const currentUserMembership = membershipsQuery.data?.find(
    (m) => m.userId === currentUserId,
  );
  const currentUserAccessLevel = currentUserMembership?.accessLevel;
  const canManage = canManageMembers(currentUserAccessLevel);
  const assignableLevels = getAssignableAccessLevels(currentUserAccessLevel);

  // Check if current user is recipient of pending transfer
  const pendingTransfer = pendingTransferQuery.data;
  const isTransferRecipient =
    pendingTransfer && pendingTransfer.toUserId === currentUserId;

  // Existing user IDs for validation
  const existingUserIds = membershipsQuery.data?.map((m) => m.userId!) ?? [];

  // Close on escape key (but not when transfer confirm modal is open)
  useEffect(() => {
    function handleEscape(event: KeyboardEvent) {
      // Don't close if the transfer confirm modal is open
      if (transferTargetUserId !== null) {
        return;
      }
      if (event.key === "Escape") {
        onClose();
      }
    }

    if (isOpen) {
      document.addEventListener("keydown", handleEscape);
      return () => {
        document.removeEventListener("keydown", handleEscape);
      };
    }
  }, [isOpen, onClose, transferTargetUserId]);

  // Close when clicking outside (but not when transfer confirm modal is open)
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      // Don't close if the transfer confirm modal is open
      if (transferTargetUserId !== null) {
        return;
      }
      if (
        modalRef.current &&
        !modalRef.current.contains(event.target as Node)
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
  }, [isOpen, onClose, transferTargetUserId]);

  // Reset state when modal closes
  // Note: This effect syncs UI state (error/transfer state) with modal visibility
  useEffect(() => {
    if (!isOpen) {
      setTransferTargetUserId(null); // eslint-disable-line react-hooks/set-state-in-effect
      setError(null);
    }
  }, [isOpen]);

  // Handlers
  const handleAddMember = async (userId: string, accessLevel: AccessLevel) => {
    setError(null);
    try {
      await shareConversation.mutateAsync({
        conversationId,
        userId,
        accessLevel,
      });
    } catch (err) {
      const errorMessage =
        err instanceof Error ? err.message : "Failed to add member";
      setError(errorMessage);
    }
  };

  const handleAccessLevelChange = async (
    userId: string,
    accessLevel: AccessLevel,
  ) => {
    setError(null);
    try {
      await updateMembership.mutateAsync({
        conversationId,
        userId,
        accessLevel,
      });
    } catch (err) {
      const errorMessage =
        err instanceof Error ? err.message : "Failed to update access level";
      setError(errorMessage);
    }
  };

  const handleRemoveMember = async (userId: string) => {
    setError(null);
    try {
      await removeMembership.mutateAsync({
        conversationId,
        userId,
      });
    } catch (err) {
      const errorMessage =
        err instanceof Error ? err.message : "Failed to remove member";
      setError(errorMessage);
    }
  };

  const handleTransferOwnership = (userId: string) => {
    setTransferTargetUserId(userId);
  };

  const handleConfirmTransfer = async () => {
    if (!transferTargetUserId) return;
    setError(null);
    try {
      await createTransfer.mutateAsync({
        conversationId,
        newOwnerUserId: transferTargetUserId,
      });
      setTransferTargetUserId(null);
    } catch (err) {
      const errorMessage =
        err instanceof Error ? err.message : "Failed to initiate transfer";
      setError(errorMessage);
    }
  };

  const handleAcceptTransfer = async () => {
    if (!pendingTransfer) return;
    setError(null);
    try {
      await acceptTransfer.mutateAsync({ transferId: pendingTransfer.id });
    } catch (err) {
      const errorMessage =
        err instanceof Error ? err.message : "Failed to accept transfer";
      setError(errorMessage);
    }
  };

  const handleDeclineTransfer = async () => {
    if (!pendingTransfer) return;
    setError(null);
    try {
      await deleteTransfer.mutateAsync({ transferId: pendingTransfer.id });
    } catch (err) {
      const errorMessage =
        err instanceof Error ? err.message : "Failed to decline transfer";
      setError(errorMessage);
    }
  };

  const handleCancelTransfer = async () => {
    if (!pendingTransfer) return;
    setError(null);
    try {
      await deleteTransfer.mutateAsync({ transferId: pendingTransfer.id });
    } catch (err) {
      const errorMessage =
        err instanceof Error ? err.message : "Failed to cancel transfer";
      setError(errorMessage);
    }
  };

  if (!isOpen) return null;

  // Sort members: owner first, then by access level, then alphabetically
  const sortedMembers = [...(membershipsQuery.data ?? [])].sort((a, b) => {
    const levelOrder: Record<AccessLevel, number> = {
      owner: 0,
      manager: 1,
      writer: 2,
      reader: 3,
    };
    const aOrder = levelOrder[a.accessLevel!] ?? 4;
    const bOrder = levelOrder[b.accessLevel!] ?? 4;
    if (aOrder !== bOrder) return aOrder - bOrder;
    return (a.userId ?? "").localeCompare(b.userId ?? "");
  });

  const memberCount = membershipsQuery.data?.length ?? 0;
  const hasOtherMembers = memberCount > 1;

  return createPortal(
    <>
      <div className="fixed inset-0 z-50 flex items-center justify-center">
        {/* Backdrop */}
        <div className="absolute inset-0 bg-ink/40" aria-hidden="true" />

        {/* Modal */}
        <div
          ref={modalRef}
          role="dialog"
          aria-modal="true"
          aria-labelledby="share-modal-title"
          className="relative z-10 flex max-h-[85vh] w-full max-w-md flex-col rounded-2xl border border-stone/20 bg-cream shadow-2xl"
        >
          {/* Header */}
          <div className="flex items-center justify-between border-b border-stone/10 px-6 py-4">
            <div>
              <h2
                id="share-modal-title"
                className="font-serif text-xl text-ink"
              >
                {canManage ? "Share" : "Members"}
              </h2>
              {conversationTitle && (
                <p className="mt-0.5 truncate text-sm text-stone">
                  "{conversationTitle}"
                </p>
              )}
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded p-1 text-stone transition-colors hover:bg-mist hover:text-ink"
              aria-label="Close"
            >
              <X className="h-5 w-5" />
            </button>
          </div>

          {/* Content */}
          <div className="flex-1 overflow-y-auto px-6 py-4">
            {/* Error message */}
            {error && (
              <div className="mb-4 rounded-lg bg-terracotta/10 px-3 py-2 text-sm text-terracotta">
                {error}
              </div>
            )}

            {/* Incoming transfer banner (for recipient) */}
            {isTransferRecipient && pendingTransfer && (
              <div className="mb-4">
                <IncomingTransferBanner
                  transfer={pendingTransfer}
                  onAccept={handleAcceptTransfer}
                  onDecline={handleDeclineTransfer}
                  isAccepting={acceptTransfer.isPending}
                  isDeclining={deleteTransfer.isPending}
                />
              </div>
            )}

            {/* Add member form (for owners/managers) */}
            {canManage && (
              <div className="mb-6">
                <AddMemberForm
                  onAdd={handleAddMember}
                  allowedLevels={assignableLevels}
                  isAdding={shareConversation.isPending}
                  existingUserIds={existingUserIds}
                  currentUserId={currentUserId}
                />
              </div>
            )}

            {/* Section header */}
            <div className="mb-3 flex items-center gap-2">
              <Users className="h-4 w-4 text-stone" />
              <span className="text-sm font-medium text-stone">
                People with access
              </span>
            </div>

            {/* Member list */}
            {membershipsQuery.isLoading ? (
              <div className="space-y-3">
                {[1, 2, 3].map((i) => (
                  <div
                    key={i}
                    className="h-16 animate-pulse rounded-lg bg-mist"
                  />
                ))}
              </div>
            ) : sortedMembers.length === 0 ? (
              <p className="py-4 text-center text-sm text-stone">
                No members found
              </p>
            ) : (
              <div className="space-y-1">
                {sortedMembers.map((membership) => (
                  <MemberListItem
                    key={membership.userId}
                    membership={membership}
                    currentUserId={currentUserId}
                    currentUserAccessLevel={currentUserAccessLevel!}
                    pendingTransfer={pendingTransfer}
                    onAccessLevelChange={handleAccessLevelChange}
                    onRemove={handleRemoveMember}
                    onTransferOwnership={handleTransferOwnership}
                    onCancelTransfer={handleCancelTransfer}
                    isUpdating={
                      updateMembership.isPending || removeMembership.isPending
                    }
                  />
                ))}
              </div>
            )}

            {/* Empty state message */}
            {!hasOtherMembers && !membershipsQuery.isLoading && canManage && (
              <div className="mt-4 rounded-lg border border-stone/10 bg-mist/30 px-4 py-3 text-center">
                <p className="text-sm text-stone">
                  This conversation is private.
                </p>
                <p className="mt-0.5 text-sm text-stone">
                  Add people above to collaborate.
                </p>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Transfer confirmation modal */}
      <TransferConfirmModal
        isOpen={transferTargetUserId !== null}
        onClose={() => setTransferTargetUserId(null)}
        onConfirm={handleConfirmTransfer}
        recipientUserId={transferTargetUserId ?? ""}
        isLoading={createTransfer.isPending}
      />
    </>,
    document.body,
  );
}
