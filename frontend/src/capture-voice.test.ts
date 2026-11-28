import { beforeEach, describe, expect, it, vi } from "vitest";
import type { VoiceControl, VoiceHost } from "./voice";

// The capture box's side of dictation: the host it hands to mountVoice. The
// voice controller itself is replaced so the host can be driven directly.
const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  (window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    disconnect(): void {}
  };
  return {
    handlers: new Map<string, (data: unknown) => void>(),
    host: null as unknown as VoiceHost,
    log: [] as string[],
    finish: async () => {},
  };
});
vi.mock("./api", () => ({
  api: {
    preview: async (input: string) => ({ kind: "todo", text: input, tags: ["inbox"], dueAt: null, rrule: "", priority: "", human: "", alerts: null, warnings: null }),
    capture: async (input: string) => {
      h.log.push(`capture:${input}`);
      return { id: `id-${input}`, text: input };
    },
    captureReply: async (input: string) => {
      h.log.push(`reply:${input}`);
      return { id: "r", text: input };
    },
    getSettings: async () => ({ defaultTime: "09:00", workDays: ["monday"] }),
    hideCapture: async () => {},
    resizeCapture: async () => {},
    withDue: async (input: string) => input,
    deleteItem: async () => {},
    voice: { wakeStatus: async () => ({ state: "off", enabled: false, chime: true }) },
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  events: { captureOpen: "capture:open", replyOpen: "reply:open", captureWake: "capture:wake", wakeStatus: "voice:wake-status" },
  on: (name: string, fn: (data: unknown) => void) => {
    h.handlers.set(name, fn);
    return () => {};
  },
}));
vi.mock("./voice", () => ({
  mountVoice: (_root: HTMLElement, host: VoiceHost): VoiceControl => {
    h.host = host;
    return {
      toggle() {},
      escape: () => false,
      cancel() {},
      state: () => "idle",
      startSession() {},
      focusGained() {},
      finish: () => h.finish(),
    };
  },
}));

import { mountCapture } from "./capture";

const settle = () => new Promise((r) => setTimeout(r, 0));
const key = (k: string) => document.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
const rows = () => [...document.querySelectorAll(".rv-row .rv-text")].map((n) => n.textContent);
const input = () => document.querySelector<HTMLInputElement>("#cap-input")!;
const status = () => document.querySelector<HTMLElement>("#cap-hint-status")!;

describe("capture box voice host", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    const root = document.createElement("div");
    document.body.append(root);
    h.log = [];
    h.finish = async () => {};
    mountCapture(root);
  });

  it("a boundary moves the input into a new row and returns the count", () => {
    expect(h.host.addTask()).toBe(0); // empty input: nothing
    h.host.insert("call Sam");
    expect(h.host.addTask()).toBe(1);
    h.host.insert("buy milk");
    expect(h.host.addTask()).toBe(2);
    expect(rows()).toEqual(["call Sam", "buy milk"]);
    expect(input().value).toBe("");
    expect(h.host.sessionText()).toBe("call Sam buy milk");
  });

  it("a row added by voice dismisses a showing discard confirm", () => {
    h.host.insert("call Sam");
    h.host.addTask();
    h.host.insert("buy milk");
    key("Escape"); // first Esc with rows: the confirm
    expect(status().hidden).toBe(false);
    expect(status().textContent).toContain("Discard");
    h.host.addTask(); // "new task" heard while the confirm shows
    expect(status().hidden).toBe(true);
    key("Escape"); // asks again instead of discarding
    expect(rows()).toEqual(["call Sam", "buy milk"]);
  });

  it("scratch clears the input, then drops the last row", () => {
    h.host.insert("call Sam");
    h.host.addTask();
    h.host.insert("buy milk");
    expect(h.host.scratch()).toBe(true);
    expect(input().value).toBe("");
    expect(h.host.scratch()).toBe(true);
    expect(rows()).toEqual([]);
    expect(h.host.scratch()).toBe(false);
  });

  it("reply mode is single-task: no rows, scratch never drops hidden rows", async () => {
    h.host.insert("call Sam");
    h.host.addTask();
    h.handlers.get("reply:open")!({ line: "Reply to Priya: \"hi\"", owedTo: "Priya", source: "Teams", quote: "", keepText: false });
    await settle();
    expect(h.host.multi()).toBe(false);
    expect(h.host.addTask()).toBe(0);
    input().value = "";
    expect(h.host.scratch()).toBe(false);
    expect(h.host.sessionText()).toBe("");
    h.handlers.get("capture:open")!(undefined);
    expect(h.host.multi()).toBe(true);
    expect(rows()).toEqual(["call Sam"]); // still there for the next normal box
  });

  it("Enter while listening waits for the session to finish, then saves", async () => {
    const box = document.querySelector<HTMLElement>("#capture")!;
    box.dataset.voice = "listening";
    h.finish = async () => {
      h.log.push("finish");
      h.host.insert("buy milk"); // the last segment lands
      box.dataset.voice = "idle";
    };
    input().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    await settle();
    expect(h.log).toEqual(["finish", "capture:buy milk"]);
  });

  it("a note without Undo hides the Undo button", () => {
    h.host.showNote("Stopped after 5 minutes");
    expect(document.querySelector("#cap-note-text")!.textContent).toBe("Stopped after 5 minutes");
    expect(document.querySelector<HTMLElement>("#cap-note")!.hidden).toBe(false);
    expect(document.querySelector<HTMLElement>("#cap-note-undo")!.hidden).toBe(true);
  });

  it("the listening hint row shows ↵ save N when Enter would save several", () => {
    const hint = document.querySelector<HTMLElement>("#cap-voice-save")!;
    expect(hint.closest(".cap-hint-voice")).not.toBeNull();
    expect(hint.hidden).toBe(true);
    h.host.insert("call Sam");
    expect(hint.hidden).toBe(true); // one task
    h.host.addTask();
    expect(hint.hidden).toBe(true); // one row, empty input
    h.host.insert("buy milk");
    expect(hint.hidden).toBe(false);
    expect(hint.textContent).toBe("↵ save 2");
    h.host.scratch();
    expect(hint.hidden).toBe(true);
  });
});
