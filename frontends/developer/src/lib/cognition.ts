// Shared helpers for `cognition.v1/*` memory formats used by both the memory
// list and the memory detail views. All of these kinds share the same value
// shape (`content` / `confidence` / `citations` / `provenance`).

export const COGNITION_KINDS = ["preference", "fact", "procedure", "problem_solution", "decision"] as const;
export type CognitionKind = (typeof COGNITION_KINDS)[number];

export const COGNITION_KIND_LABELS: Record<CognitionKind, string> = {
  preference: "Preference",
  fact: "Fact",
  procedure: "Procedure",
  problem_solution: "Problem solution",
  decision: "Decision",
};

export interface CognitionMemoryValue {
  citations?: unknown[];
  confidence?: unknown;
  content?: unknown;
  provenance?: Record<string, unknown>;
}

/** Returns the cognition kind when `namespace` contains `cognition.v1/<kind>`. */
export function getCognitionKind(namespace: string[] | undefined): CognitionKind | undefined {
  if (!namespace) return undefined;
  const cognitionIndex = namespace.findIndex((segment) => segment === "cognition.v1");
  if (cognitionIndex < 0) return undefined;
  const kind = namespace[cognitionIndex + 1];
  return (COGNITION_KINDS as readonly string[]).includes(kind) ? (kind as CognitionKind) : undefined;
}

export function normalizeCognitionMemoryValue(value: unknown): CognitionMemoryValue {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return value as CognitionMemoryValue;
  }
  return {};
}

/** Coerces `confidence` to a fraction in [0, 1], or null when not numeric. */
export function cognitionConfidence(value: CognitionMemoryValue): number | null {
  const numeric = typeof value.confidence === "number" ? value.confidence : Number(value.confidence);
  if (!Number.isFinite(numeric)) return null;
  return Math.max(0, Math.min(1, numeric));
}

export function describeConfidence(value: number): string {
  if (value >= 0.85) return "High confidence";
  if (value >= 0.6) return "Moderate confidence";
  if (value >= 0.3) return "Low confidence";
  return "Very low confidence";
}
