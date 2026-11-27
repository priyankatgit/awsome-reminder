import { beforeEach, describe, expect, it, vi } from "vitest";

// The capture box imports the Wails API; a fake stands in, with a local event bus.
const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  (window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    disconnect(): void {}
  };
  return {
    handlers: new Map<string, (data: unknown) => void>(),
    captureReply: undefined as unknown as (input: string, meta: unknown) => Promise<unknown>,
    capture: undefined as unknown as (input: string) => Promise<unknown>,
  };
});
vi.mock("./api", () => ({
  api: {
    preview: async (input: string) => ({ kind: "todo", text: input, tags: ["inbox"], dueAt: null, rrule: "", priority: "", human: "", alerts: null, warnings: null }),
    capture: (input: string) => h.capture(input),
    captureReply: (input: string, meta: unknown) => h.captureReply(input, meta),
    getSettings: async () => ({ defaultTime: "09:00", workDays: ["monday", "tuesday", "wednesday", "thursday", "friday"] }),
    hideCapture: async () => {},
    resizeCapture: async () => {},
    withDue: async (input: string) => input,
    voice: { status: async () => ({}), install: async () => {}, cancelInstall: async () => {}, warm: () => Promise.resolve(), transcribe: () => Promise.resolve("") },
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  events: { captureOpen: "capture:open", replyOpen: "reply:open" },
  on: (name: string, fn: (data: unknown) => void) => {
    h.handlers.set(name, fn);
    return () => {};
  },
}));

import { mountCapture } from "./capture";
import { nudgeSummary, personLabel } from "./reply-mode";

const workWeek = { defaultTime: "09:00", workDays: ["monday", "tuesday", "wednesday", "thursday", "friday"] };
const at = (iso: string) => new Date(iso); // local time

describe("nudgeSummary", () => {
  it("without a due time: a nudge in 2 hours, then the next working morning", () => {
    expect(nudgeSummary(at("2026-12-02T10:00"), workWeek, null)).toBe("nudge in 2h, then 9:00"); // Wednesday
  });
  it("names the day when the next working morning is not tomorrow", () => {
    expect(nudgeSummary(at("2026-12-04T16:00"), workWeek, null)).toBe("nudge in 2h, then Mon 9:00"); // Friday
    expect(nudgeSummary(at("2026-12-02T23:00"), workWeek, null)).toBe("nudge in 2h, then Fri 9:00"); // first nudge is Thursday
  });
  it("uses the default time without a leading zero", () => {
    expect(nudgeSummary(at("2026-12-02T10:00"), { ...workWeek, defaultTime: "08:30" }, null)).toBe("nudge in 2h, then 8:30");
  });
  it("with no working days only the first nudge is left", () => {
    expect(nudgeSummary(at("2026-12-02T10:00"), { ...workWeek, workDays: [] }, null)).toBe("nudge in 2h");
  });
  it("with a due time: the alerts, then daily nudges", () => {
    expect(nudgeSummary(at("2026-12-02T10:00"), workWeek, at("2026-12-02T17:00"))).toBe("alerts before 17:00, then daily 9:00");
    expect(nudgeSummary(at("2026-12-02T10:00"), workWeek, at("2026-12-03T17:00"))).toBe("alerts before Thu 17:00, then daily 9:00");
    expect(nudgeSummary(at("2026-12-02T10:00"), workWeek, at("2026-12-20T09:05"))).toBe("alerts before 20 Dec 9:05, then daily 9:00");
    expect(nudgeSummary(at("2026-12-02T10:00"), { ...workWeek, workDays: [] }, at("2026-12-02T17:00"))).toBe("alerts before 17:00");
  });
});

describe("personLabel", () => {
  it("names nobody as Someone", () => {
    expect(personLabel("")).toBe("Someone");
    expect(personLabel("   ")).toBe("Someone");
    expect(personLabel(" Sam ")).toBe("Sam");
  });
});

const tick = () => new Promise((r) => setTimeout(r, 0));
const prefill = (extra: Record<string, unknown> = {}) => ({
  line: "Reply to Priya: can you check the deploy?",
  owedTo: "Priya",
  source: "Teams",
  quote: "can you check the deploy?",
  grabbed: true,
  needsAccess: false,
  keepText: false,
  ...extra,
});
function key(target: EventTarget, k: string, init: KeyboardEventInit = {}): void {
  target.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...init }));
}
const q = <T extends HTMLElement>(sel: string) => document.querySelector<T>(sel)!;

describe("capture box in reply mode", () => {
  let calls: { input: string; meta: unknown }[];
  let plain: string[];
  beforeEach(() => {
    calls = [];
    plain = [];
    h.captureReply = async (input, meta) => void calls.push({ input, meta });
    h.capture = async (input) => void plain.push(input);
    document.body.innerHTML = "<div id='app'></div>";
    mountCapture(document.getElementById("app")!);
  });

  it("Alt+P edits the person, Enter returns to the line, Enter saves the reply", async () => {
    h.handlers.get("reply:open")!(prefill());
    await tick();
    const input = q<HTMLInputElement>("#cap-input");
    expect(input.value).toBe("Reply to Priya: can you check the deploy?");
    expect(q("#cap-lead").dataset.icon).toBe("reply");
    expect(q("#cap-preview").textContent).toContain("Reply owed");
    expect(q("#cap-preview").textContent).toContain("for Priya");
    expect(q("#cap-preview").textContent).toContain("from Teams");

    key(document.activeElement!, "p", { code: "KeyP", altKey: true });
    const person = document.activeElement as HTMLInputElement;
    expect(person).not.toBe(input);
    expect(person.tagName).toBe("INPUT");
    person.value = "Sam";
    person.dispatchEvent(new Event("input", { bubbles: true }));
    key(person, "Enter");
    expect(document.activeElement).toBe(input);
    expect(q("#cap-preview").textContent).toContain("for Sam");

    key(input, "Enter");
    await tick();
    expect(plain).toEqual([]);
    expect(calls).toEqual([
      { input: "Reply to Priya: can you check the deploy?", meta: { owedTo: "Sam", source: "Teams", quote: "can you check the deploy?" } },
    ]);
  });

  it("Esc in the person input goes back to the line without closing", async () => {
    h.handlers.get("reply:open")!(prefill());
    await tick();
    q<HTMLButtonElement>(".chip-person").click();
    const person = document.activeElement as HTMLInputElement;
    person.value = "Nobody";
    key(person, "Escape");
    expect(document.activeElement).toBe(q("#cap-input"));
    expect(q("#capture").classList.contains("is-hidden")).toBe(false);
    expect(q("#cap-preview").textContent).toContain("for Priya");
  });

  it("keepText switches to reply mode keeping the typed text, with nobody named", async () => {
    const input = q<HTMLInputElement>("#cap-input");
    input.value = "send Sam the figures";
    h.handlers.get("reply:open")!(prefill({ line: "", owedTo: "", source: "", quote: "", grabbed: false, keepText: true }));
    await tick();
    expect(input.value).toBe("send Sam the figures");
    expect(q("#cap-preview").textContent).toContain("for Someone");
    expect(q("#cap-preview").textContent).not.toContain("from");
    expect(q(".cap-access").hidden).toBe(true);
    key(input, "Enter");
    await tick();
    expect(calls).toEqual([{ input: "send Sam the figures", meta: { owedTo: "", source: "", quote: "" } }]);
  });

  it("explains the macOS Accessibility setting when the grab needs it", async () => {
    h.handlers.get("reply:open")!(prefill({ needsAccess: true, grabbed: false }));
    await tick();
    const note = q(".cap-access");
    expect(note.hidden).toBe(false);
    expect(note.textContent).toContain("Accessibility");
  });

  it("the next capture hotkey opens a clean normal box", async () => {
    h.handlers.get("reply:open")!(prefill());
    await tick();
    key(q("#cap-input"), "Escape");
    h.handlers.get("capture:open")!(undefined);
    await tick();
    expect(q<HTMLInputElement>("#cap-input").value).toBe("");
    expect(q("#cap-lead").dataset.icon).toBe("search");
    expect(q(".cap-reply").hidden).toBe(true); // the "Reply owed" parts
    expect(q("#capture").dataset.mode).toBeUndefined();
    q<HTMLInputElement>("#cap-input").value = "buy milk";
    key(q("#cap-input"), "Enter");
    await tick();
    expect(plain).toEqual(["buy milk"]);
    expect(calls).toEqual([]);
  });

  it("shows a save error inline", async () => {
    h.captureReply = async () => {
      throw new Error("store is closed");
    };
    h.handlers.get("reply:open")!(prefill());
    await tick();
    key(q("#cap-input"), "Enter");
    await tick();
    expect(q("#cap-error").hidden).toBe(false);
    expect(q("#cap-error").textContent).toBe("store is closed");
  });
});
