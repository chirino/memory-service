import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Pencil, Plus, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  getProfileContext,
  getProfileInputs,
  isProfileContextUnsupported,
  putProfileInputs,
  type ProfileContextResponse,
} from "@/lib/profile-context";

type ProfileContextPanelProps = {
  onBackToChat: () => void;
};

function formatTimestamp(value?: string | null): string | null {
  if (!value) {
    return null;
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(date);
}

function normalizeDraft(values: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    const normalized = value.trim().replace(/\s+/g, " ");
    if (!normalized || seen.has(normalized)) {
      continue;
    }
    seen.add(normalized);
    result.push(normalized);
  }
  return result;
}

export function ProfileContextPanel({ onBackToChat }: ProfileContextPanelProps) {
  const queryClient = useQueryClient();
  const [isEditing, setIsEditing] = useState(false);
  const [draftInputs, setDraftInputs] = useState<string[]>([]);

  const profileQuery = useQuery<ProfileContextResponse>({
    queryKey: ["profile-context"],
    queryFn: getProfileContext,
    retry: false,
  });

  const inputsQuery = useQuery<string[]>({
    queryKey: ["profile-context-inputs"],
    queryFn: getProfileInputs,
    retry: false,
    enabled: profileQuery.isSuccess,
  });

  const saveInputsMutation = useMutation({
    mutationFn: async (inputs: string[]) => putProfileInputs(normalizeDraft(inputs)),
    onSuccess: (accepted) => {
      setDraftInputs(accepted);
      setIsEditing(false);
      void queryClient.setQueryData(["profile-context-inputs"], accepted);
    },
  });

  const profile = profileQuery.data;
  const profileGeneratedAt = formatTimestamp(profile?.generatedAt);
  const canEditInputs = inputsQuery.isSuccess;
  const visibleInputs = inputsQuery.data ?? [];
  const normalizedDraft = useMemo(() => normalizeDraft(draftInputs), [draftInputs]);
  const isDirty = JSON.stringify(normalizedDraft) !== JSON.stringify(visibleInputs);

  if (profileQuery.isLoading) {
    return (
      <main className="flex flex-1 items-center justify-center bg-cream text-sm text-stone">
        <div className="spinner mr-3" />
        Loading profile context
      </main>
    );
  }

  if (profileQuery.isError) {
    return (
      <main className="flex flex-1 flex-col bg-cream">
        <header className="border-b border-stone/10 px-8 py-5">
          <Button type="button" variant="ghost" size="sm" onClick={onBackToChat}>
            Back to chat
          </Button>
        </header>
        <div className="flex flex-1 items-center justify-center px-8 text-sm text-stone">
          {isProfileContextUnsupported(profileQuery.error)
            ? "Profile context is not available from this agent app."
            : "Profile context is unavailable."}
        </div>
      </main>
    );
  }

  const profileContent = profile?.content?.trim() ?? "";

  return (
    <main className="flex min-w-0 flex-1 flex-col bg-cream">
      <header className="border-b border-stone/10 px-8 py-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="font-serif text-3xl tracking-tight text-ink">Manage memory</h2>
            {profileGeneratedAt && <p className="mt-1 text-sm text-stone">Generated {profileGeneratedAt}</p>}
          </div>
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={onBackToChat}>
              Back to chat
            </Button>
          </div>
        </div>
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[minmax(0,1fr)_360px]">
        <section className="min-h-0 overflow-y-auto px-8 py-6">
          <div className="max-w-4xl whitespace-pre-wrap rounded-md border border-stone/15 bg-mist/35 p-5 font-mono text-sm leading-6 text-ink">
            {profileContent || <span className="font-sans text-stone">No generated profile context yet.</span>}
          </div>
        </section>

        {canEditInputs && (
          <aside className="min-h-0 overflow-y-auto border-l border-stone/10 px-5 py-6">
            <div className="mb-4 flex items-center justify-between gap-3">
              <h3 className="text-sm font-semibold uppercase tracking-wide text-stone">Profile Inputs</h3>
              {isEditing ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setDraftInputs(visibleInputs);
                    setIsEditing(false);
                  }}
                  disabled={saveInputsMutation.isPending}
                >
                  <X className="h-4 w-4" />
                  Cancel
                </Button>
              ) : (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setDraftInputs(visibleInputs);
                    setIsEditing(true);
                  }}
                >
                  <Pencil className="h-4 w-4" />
                  Edit
                </Button>
              )}
            </div>

            {isEditing ? (
              <div className="space-y-3">
                {draftInputs.map((value, index) => (
                  <div key={index} className="flex items-start gap-2">
                    <textarea
                      value={value}
                      onChange={(event) => {
                        const next = [...draftInputs];
                        next[index] = event.target.value;
                        setDraftInputs(next);
                      }}
                      rows={3}
                      className="min-h-20 flex-1 resize-y rounded-md border border-stone/20 bg-cream px-3 py-2 text-sm leading-5 text-ink outline-none transition focus:border-sage focus:ring-2 focus:ring-sage/20"
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label="Remove input"
                      onClick={() => setDraftInputs((current) => current.filter((_, itemIndex) => itemIndex !== index))}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                ))}

                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setDraftInputs([...draftInputs, ""])}
                  >
                    <Plus className="h-4 w-4" />
                    Add
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    disabled={!isDirty || saveInputsMutation.isPending}
                    onClick={() => saveInputsMutation.mutate(draftInputs)}
                  >
                    <Check className="h-4 w-4" />
                    Save
                  </Button>
                </div>

                {saveInputsMutation.isError && (
                  <p className="text-sm text-terracotta">Failed to save profile inputs.</p>
                )}
              </div>
            ) : (
              <div className="space-y-2">
                {visibleInputs.length === 0 ? (
                  <p className="rounded-md border border-stone/10 bg-mist/40 px-3 py-3 text-sm text-stone">
                    No inputs.
                  </p>
                ) : (
                  visibleInputs.map((input, index) => (
                    <div
                      key={`${index}-${input}`}
                      className="rounded-md border border-stone/10 bg-mist/40 px-3 py-3 text-sm leading-5 text-ink"
                    >
                      {input}
                    </div>
                  ))
                )}
              </div>
            )}
          </aside>
        )}
      </div>
    </main>
  );
}
