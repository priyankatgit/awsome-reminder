import { describe, expect, it } from "vitest";
import { placePaste, splitPasted } from "./multi-task";

describe("splitPasted", () => {
  it("one task per non-empty line, list markers dropped", () => {
    expect(splitPasted("- a\r\n* b\n\n• c\n1. d\n2) e\n[ ] f\n- [x] g\n+ h")).toEqual(["a", "b", "c", "d", "e", "f", "g", "h"]);
  });

  it("keeps text that only looks like a marker mid-line, and #tags / !priority", () => {
    expect(splitPasted("call Sam - urgent !high\n#backend fix it")).toEqual(["call Sam - urgent !high", "#backend fix it"]);
  });

  it("a single line stays one entry; blank text gives none", () => {
    expect(splitPasted("just one")).toEqual(["just one"]);
    expect(splitPasted(" \n \n")).toEqual([]);
  });
});

describe("placePaste", () => {
  it("joins the text around the selection to the first and last lines", () => {
    expect(placePaste("pre SEL post", 4, 7, ["a", "b", "c"])).toEqual({ rows: ["pre a", "b"], input: "c post", caret: 1 });
  });

  it("an empty input takes the last line", () => {
    expect(placePaste("", 0, 0, ["a", "b"])).toEqual({ rows: ["a"], input: "b", caret: 1 });
  });
});
