import { useState } from "react";
import { Crown, ChevronDown, ChevronUp, ExternalLink, X } from "lucide-react";
import type { OwnershipTransfer } from "@/client";
import { usePendingTransfers, useAcceptTransfer, useDeleteTransfer } from "@/hooks/useSharing";

type PendingTransfersPanelProps = {
  onNavigateToConversation?: (conversationId: string) => void;
};

type TransferItemProps = {
  transfer: OwnershipTransfer;
  onAccept: () => void;
  onDecline: () => void;
  onNavigate?: () => void;
  isAccepting: boolean;
  isDeclining: boolean;
};

function TransferItem({ transfer, onAccept, onDecline, onNavigate, isAccepting, isDeclining }: TransferItemProps) {
  const isLoading = isAccepting || isDeclining;

  return (
    <div className="rounded-lg border border-stone/20 bg-cream p-3">
      <div className="flex items-start gap-3">
        <div className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-terracotta/10">
          <Crown className="h-4 w-4 text-terracotta" />
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <p className="text-sm font-medium text-ink">From {transfer.fromUserId}</p>
            {onNavigate && (
              <button
                type="button"
                onClick={onNavigate}
                className="rounded p-0.5 text-stone transition-colors hover:bg-mist hover:text-ink"
                title="Go to conversation"
              >
                <ExternalLink className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
          <p className="mt-0.5 truncate text-xs text-stone">Conversation: {transfer.conversationId?.slice(0, 8)}...</p>

          <div className="mt-2 flex items-center gap-2">
            <button
              type="button"
              onClick={onDecline}
              disabled={isLoading}
              className="rounded-md border border-stone/20 px-2 py-1 text-xs font-medium text-stone transition-colors hover:bg-mist hover:text-ink disabled:opacity-50"
            >
              {isDeclining ? "..." : "Decline"}
            </button>
            <button
              type="button"
              onClick={onAccept}
              disabled={isLoading}
              className="rounded-md bg-terracotta px-2 py-1 text-xs font-medium text-cream transition-colors hover:bg-terracotta/90 disabled:opacity-50"
            >
              {isAccepting ? "..." : "Accept"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

export function PendingTransfersPanel({ onNavigateToConversation }: PendingTransfersPanelProps) {
  const [isExpanded, setIsExpanded] = useState(true);
  const [isDismissed, setIsDismissed] = useState(false);
  const [processingTransferId, setProcessingTransferId] = useState<string | null>(null);

  const transfersQuery = usePendingTransfers("recipient");
  const acceptTransfer = useAcceptTransfer();
  const deleteTransfer = useDeleteTransfer();

  const pendingTransfers = transfersQuery.data ?? [];

  // Don't render if dismissed or no transfers
  if (isDismissed || pendingTransfers.length === 0) {
    return null;
  }

  const handleAccept = async (transferId: string) => {
    setProcessingTransferId(transferId);
    try {
      await acceptTransfer.mutateAsync({ transferId });
    } finally {
      setProcessingTransferId(null);
    }
  };

  const handleDecline = async (transferId: string) => {
    setProcessingTransferId(transferId);
    try {
      await deleteTransfer.mutateAsync({ transferId });
    } finally {
      setProcessingTransferId(null);
    }
  };

  return (
    <div className="border-b border-stone/10 bg-terracotta/5">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-2">
        <button
          type="button"
          onClick={() => setIsExpanded(!isExpanded)}
          className="flex flex-1 items-center gap-2 text-left"
        >
          <Crown className="h-4 w-4 text-terracotta" />
          <span className="text-sm font-medium text-ink">Pending transfers ({pendingTransfers.length})</span>
          {isExpanded ? <ChevronUp className="h-4 w-4 text-stone" /> : <ChevronDown className="h-4 w-4 text-stone" />}
        </button>
        <button
          type="button"
          onClick={() => setIsDismissed(true)}
          className="rounded p-1 text-stone transition-colors hover:bg-mist hover:text-ink"
          title="Dismiss"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* Content */}
      {isExpanded && (
        <div className="space-y-2 px-4 pb-3">
          {pendingTransfers.map((transfer) => (
            <TransferItem
              key={transfer.id}
              transfer={transfer}
              onAccept={() => handleAccept(transfer.id!)}
              onDecline={() => handleDecline(transfer.id!)}
              onNavigate={
                onNavigateToConversation && transfer.conversationId
                  ? () => onNavigateToConversation(transfer.conversationId!)
                  : undefined
              }
              isAccepting={processingTransferId === transfer.id && acceptTransfer.isPending}
              isDeclining={processingTransferId === transfer.id && deleteTransfer.isPending}
            />
          ))}
        </div>
      )}
    </div>
  );
}
