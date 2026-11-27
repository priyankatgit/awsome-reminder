import { describe, expect, it } from "vitest";
import { priorityLabel, setPriorityToken } from "./priority";

describe("setPriorityToken", () => {
  it("appends the word form when no token is present", () => {
    expect(setPriorityToken("deploy", "high")).toBe("deploy !high");
    expect(setPriorityToken("deploy  ", "low")).toBe("deploy !low");
    expect(setPriorityToken("", "medium")).toBe("!med");
  });

  it("replaces an existing token in place", () => {
    expect(setPriorityToken("deploy !l now", "medium")).toBe("deploy !med now");
    expect(setPriorityToken("!HIGH deploy", "low")).toBe("!low deploy");
  });

  it("removes the token for none", () => {
    expect(setPriorityToken("deploy !!! now", "")).toBe("deploy now");
    expect(setPriorityToken("!m deploy", "")).toBe("deploy");
    expect(setPriorityToken("deploy", "")).toBe("deploy");
  });

  it("recognises every typed form", () => {
    for (const t of ["!high", "!h", "!!!", "!medium", "!med", "!m", "!low", "!l"]) {
      expect(setPriorityToken(`a ${t} b`, "")).toBe("a b");
    }
  });

  it("leaves quoted text and look-alikes alone", () => {
    expect(setPriorityToken('say "!h" now', "low")).toBe('say "!h" now !low');
    expect(setPriorityToken('say "x !h y" now', "")).toBe('say "x !h y" now');
    expect(setPriorityToken("call Bob ! wow!! !important", "high")).toBe("call Bob ! wow!! !important !high");
    expect(setPriorityToken("constructor toString", "")).toBe("constructor toString");
  });

  it("treats an unclosed quote as text, like the parser", () => {
    expect(setPriorityToken('say "hello !h', "low")).toBe('say "hello !low');
  });

  it("keeps one token when several are typed", () => {
    expect(setPriorityToken("a !h b !l c", "medium")).toBe("a !med b c");
    expect(setPriorityToken("a !h b !l c", "")).toBe("a b c");
  });
});

describe("priorityLabel", () => {
  it("names each level", () => {
    expect(priorityLabel("high")).toBe("High");
    expect(priorityLabel("medium")).toBe("Medium");
    expect(priorityLabel("low")).toBe("Low");
    expect(priorityLabel("")).toBe("None");
  });
});
