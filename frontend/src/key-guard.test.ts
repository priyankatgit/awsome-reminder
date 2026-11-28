import { describe, expect, it } from "vitest";
import { KeyGuard } from "./key-guard";

const kd = (key: string, init: KeyboardEventInit = {}) => new KeyboardEvent("keydown", { key, ...init });

describe("KeyGuard", () => {
  it("starts released and allows everything", () => {
    const g = new KeyGuard();
    expect(g.armed()).toBe(false);
    for (const k of ["a", "Enter", "Backspace", "Delete", "Tab", "Escape"]) expect(g.allows(kd(k))).toBe(true);
  });

  it("armed: blocks printable keys, Enter, Backspace, Delete and Tab", () => {
    const g = new KeyGuard();
    g.arm();
    expect(g.armed()).toBe(true);
    for (const k of ["a", "A", " ", "1", "Enter", "Backspace", "Delete", "Tab"]) expect(g.allows(kd(k)), k).toBe(false);
    expect(g.allows(kd("Tab", { shiftKey: true }))).toBe(false);
    expect(g.allows(kd("v", { ctrlKey: true }))).toBe(false); // a paste would change the draft
  });

  it("armed: allows Escape and modifier-only keys", () => {
    const g = new KeyGuard();
    g.arm();
    for (const k of ["Escape", "Shift", "Control", "Alt", "Meta"]) expect(g.allows(kd(k)), k).toBe(true);
  });

  it("after release allows everything again", () => {
    const g = new KeyGuard();
    g.arm();
    g.release();
    expect(g.armed()).toBe(false);
    for (const k of ["a", "Enter", "Backspace", "Delete", "Tab", "Escape", "Shift"]) expect(g.allows(kd(k))).toBe(true);
  });
});
