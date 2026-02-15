import { useState } from "react";
import { UserPlus } from "lucide-react";
import type { AccessLevel } from "@/client";
import { AccessLevelSelect } from "./access-level-select";

type AddMemberFormProps = {
  onAdd: (userId: string, accessLevel: AccessLevel) => void;
  allowedLevels: AccessLevel[];
  isAdding?: boolean;
  existingUserIds: string[];
  currentUserId: string;
};

export function AddMemberForm({
  onAdd,
  allowedLevels,
  isAdding = false,
  existingUserIds,
  currentUserId,
}: AddMemberFormProps) {
  const [userId, setUserId] = useState("");
  const [accessLevel, setAccessLevel] = useState<AccessLevel>(
    allowedLevels.includes("reader") ? "reader" : allowedLevels[0],
  );
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    const trimmedUserId = userId.trim();

    if (!trimmedUserId) {
      setError("Please enter a user ID");
      return;
    }

    if (trimmedUserId === currentUserId) {
      setError("You cannot add yourself");
      return;
    }

    if (existingUserIds.includes(trimmedUserId)) {
      setError("This user already has access");
      return;
    }

    onAdd(trimmedUserId, accessLevel);
    setUserId("");
    setError(null);
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-3">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <div className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2">
            <UserPlus className="h-4 w-4 text-stone" />
          </div>
          <input
            type="text"
            value={userId}
            onChange={(e) => {
              setUserId(e.target.value);
              setError(null);
            }}
            placeholder="Enter user ID"
            disabled={isAdding}
            className="w-full rounded-lg border border-stone/20 bg-cream py-2.5 pl-10 pr-3 text-sm text-ink placeholder-stone transition-colors focus:border-sage focus:outline-none focus:ring-1 focus:ring-sage disabled:opacity-50"
          />
        </div>

        <AccessLevelSelect
          value={accessLevel}
          onChange={setAccessLevel}
          allowedLevels={allowedLevels}
          disabled={isAdding}
          openDirection="down"
        />

        <button
          type="submit"
          disabled={isAdding || !userId.trim()}
          className="rounded-lg bg-ink px-4 py-2.5 text-sm font-medium text-cream transition-colors hover:bg-ink/90 disabled:opacity-50"
        >
          {isAdding ? "Adding..." : "Add"}
        </button>
      </div>

      {error && <p className="text-xs text-terracotta">{error}</p>}
    </form>
  );
}
