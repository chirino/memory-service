import { Crown } from "lucide-react";
import type { OwnershipTransfer } from "@/client";

type IncomingTransferBannerProps = {
  transfer: OwnershipTransfer;
  onAccept: () => void;
  onDecline: () => void;
  isAccepting?: boolean;
  isDeclining?: boolean;
};

export function IncomingTransferBanner({
  transfer,
  onAccept,
  onDecline,
  isAccepting = false,
  isDeclining = false,
}: IncomingTransferBannerProps) {
  const isLoading = isAccepting || isDeclining;

  return (
    <div className="rounded-xl border border-terracotta/30 bg-terracotta/5 p-4">
      <div className="flex items-start gap-3">
        <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full bg-terracotta/10">
          <Crown className="h-5 w-5 text-terracotta" />
        </div>

        <div className="min-w-0 flex-1">
          <h3 className="text-sm font-medium text-ink">
            Ownership transfer request
          </h3>
          <p className="mt-1 text-sm text-stone">
            <span className="font-medium text-ink">{transfer.fromUserId}</span>{" "}
            wants to transfer ownership to you
          </p>

          <div className="mt-4 flex items-center gap-3">
            <button
              type="button"
              onClick={onDecline}
              disabled={isLoading}
              className="rounded-lg border border-stone/20 px-3 py-2 text-sm font-medium text-stone transition-colors hover:bg-mist hover:text-ink disabled:opacity-50"
            >
              {isDeclining ? "Declining..." : "Decline"}
            </button>
            <button
              type="button"
              onClick={onAccept}
              disabled={isLoading}
              className="rounded-lg bg-terracotta px-3 py-2 text-sm font-medium text-cream transition-colors hover:bg-terracotta/90 disabled:opacity-50"
            >
              {isAccepting ? "Accepting..." : "Accept Ownership"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
