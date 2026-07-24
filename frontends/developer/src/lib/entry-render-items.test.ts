import { describe, expect, it } from "vitest";
import type { EntryWithForkPoint } from "@/hooks";
import { buildRenderItems, filterRenderItemsByChannel } from "./entry-render-items";

describe("entry render item filtering", () => {
  it("retains journal fork metadata in the journal view", () => {
    const journalEntry = {
      id: "journal-entry",
      channel: "journal",
      isForkPoint: true,
      forksAtPoint: [
        {
          conversationId: "fork",
          entryId: "journal-entry",
          title: "Journal branch",
          createdAt: "2026-07-23T00:00:00Z",
          isActive: true,
        },
      ],
    } as EntryWithForkPoint;

    const filtered = filterRenderItemsByChannel(buildRenderItems([journalEntry]), "journal");

    expect(filtered).toEqual([{ type: "entry", entry: journalEntry }]);
    expect(filtered[0]?.type === "entry" && filtered[0].entry.forksAtPoint).toEqual(journalEntry.forksAtPoint);
  });
});
