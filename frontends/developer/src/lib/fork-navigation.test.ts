import { describe, expect, it } from "vitest";
import { buildForkNavigationSearch } from "./fork-navigation";

describe("fork navigation search", () => {
  it("targets a journal continuation entry directly", () => {
    expect(buildForkNavigationSearch("journal-continuation", "journal")).toEqual({
      entryId: "journal-continuation",
      channel: "journal",
    });
  });

  it("targets a history continuation directly without an explicit default channel", () => {
    expect(buildForkNavigationSearch("history-continuation", "history")).toEqual({
      entryId: "history-continuation",
    });
  });
});
