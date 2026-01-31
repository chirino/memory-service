import { useState, useRef, useEffect } from "react";
import { MoreVertical, Trash2, Crown, User, Clock } from "lucide-react";
import type { AccessLevel, ConversationMembership, OwnershipTransfer } from "@/client";
import { AccessLevelSelect, AccessLevelBadge } from "./access-level-select";
import {
  canModifyMember,
  canTransferOwnership,
  getAssignableAccessLevels,
} from "@/hooks/useSharing";

type MemberListItemProps = {
  membership: ConversationMembership;
  currentUserId: string;
  currentUserAccessLevel: AccessLevel;
  pendingTransfer: OwnershipTransfer | null;
  onAccessLevelChange: (userId: string, level: AccessLevel) => void;
  onRemove: (userId: string) => void;
  onTransferOwnership: (userId: string) => void;
  onCancelTransfer: () => void;
  isUpdating?: boolean;
};

function formatDate(dateString?: string): string {
  if (!dateString) return "";
  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(date);
}

export function MemberListItem({
  membership,
  currentUserId,
  currentUserAccessLevel,
  pendingTransfer,
  onAccessLevelChange,
  onRemove,
  onTransferOwnership,
  onCancelTransfer,
  isUpdating = false,
}: MemberListItemProps) {
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  const isOwner = membership.accessLevel === "owner";
  const isCurrentUser = membership.userId === currentUserId;
  const canModify = canModifyMember(currentUserAccessLevel, membership.accessLevel);
  const canTransfer = canTransferOwnership(currentUserAccessLevel);
  const assignableLevels = getAssignableAccessLevels(currentUserAccessLevel);

  // Check if this member is the recipient of a pending transfer
  const isPendingTransferRecipient =
    pendingTransfer && pendingTransfer.toUserId === membership.userId;

  // Close menu when clicking outside
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setMenuOpen(false);
      }
    }

    if (menuOpen) {
      document.addEventListener("mousedown", handleClickOutside);
      return () => {
        document.removeEventListener("mousedown", handleClickOutside);
      };
    }
  }, [menuOpen]);

  // Close on escape key
  useEffect(() => {
    function handleEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setMenuOpen(false);
      }
    }

    if (menuOpen) {
      document.addEventListener("keydown", handleEscape);
      return () => {
        document.removeEventListener("keydown", handleEscape);
      };
    }
  }, [menuOpen]);

  const showOverflowMenu = !isOwner && (canModify || (canTransfer && !isCurrentUser));

  return (
    <div
      className={`rounded-lg p-3 transition-colors ${
        isUpdating ? "opacity-60" : "hover:bg-mist/50"
      }`}
    >
      <div className="flex items-start gap-3">
        {/* Avatar placeholder */}
        <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full bg-mist">
          <User className="h-5 w-5 text-stone" />
        </div>

        {/* User info */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <p className="truncate text-sm font-medium text-ink">
              {membership.userId}
            </p>
            {isCurrentUser && (
              <span className="rounded-full bg-mist px-2 py-0.5 text-xs text-stone">
                you
              </span>
            )}
          </div>
          <p className="mt-0.5 text-xs text-stone">
            Added {formatDate(membership.createdAt)}
          </p>

          {/* Pending transfer banner for this member */}
          {isPendingTransferRecipient && (
            <div className="mt-2 rounded-lg border border-terracotta/30 bg-terracotta/5 px-3 py-2">
              <div className="flex items-center gap-2">
                <Clock className="h-4 w-4 text-terracotta" />
                <span className="text-xs font-medium text-terracotta">
                  Transfer pending
                </span>
              </div>
              <p className="mt-1 text-xs text-stone">
                Waiting for acceptance
              </p>
              {canTransfer && (
                <button
                  type="button"
                  onClick={onCancelTransfer}
                  className="mt-2 rounded-lg border border-stone/20 px-2.5 py-1 text-xs font-medium text-stone transition-colors hover:bg-mist hover:text-ink"
                >
                  Cancel Transfer
                </button>
              )}
            </div>
          )}
        </div>

        {/* Access level and actions */}
        <div className="flex items-center gap-2">
          {/* Access level display or dropdown */}
          {isOwner ? (
            <div className="flex items-center gap-1.5">
              <span className="text-sm text-terracotta">Owner</span>
              <Crown className="h-4 w-4 text-terracotta" />
            </div>
          ) : canModify && !isPendingTransferRecipient ? (
            <AccessLevelSelect
              value={membership.accessLevel!}
              onChange={(level) =>
                onAccessLevelChange(membership.userId!, level)
              }
              allowedLevels={assignableLevels}
              disabled={isUpdating}
            />
          ) : (
            <AccessLevelBadge level={membership.accessLevel!} />
          )}

          {/* Overflow menu */}
          {showOverflowMenu && !isPendingTransferRecipient && (
            <div ref={menuRef} className="relative">
              <button
                type="button"
                onClick={() => setMenuOpen(!menuOpen)}
                className="rounded p-1 text-stone transition-colors hover:bg-mist hover:text-ink"
                aria-label="Member options"
              >
                <MoreVertical className="h-4 w-4" />
              </button>

              {menuOpen && (
                <div className="absolute bottom-full right-0 z-50 mb-1 w-48 overflow-hidden rounded-xl border border-stone/20 bg-cream shadow-xl">
                  <div className="py-1">
                    {canModify && (
                      <button
                        type="button"
                        onClick={() => {
                          onRemove(membership.userId!);
                          setMenuOpen(false);
                        }}
                        className="flex w-full items-center gap-3 px-3 py-2.5 text-left text-sm text-ink transition-colors hover:bg-mist"
                      >
                        <Trash2 className="h-4 w-4 text-terracotta" />
                        Remove
                      </button>
                    )}
                    {canTransfer && !isCurrentUser && !pendingTransfer && (
                      <button
                        type="button"
                        onClick={() => {
                          onTransferOwnership(membership.userId!);
                          setMenuOpen(false);
                        }}
                        className="flex w-full items-center gap-3 px-3 py-2.5 text-left text-sm text-ink transition-colors hover:bg-mist"
                      >
                        <Crown className="h-4 w-4 text-terracotta" />
                        Transfer ownership
                      </button>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
