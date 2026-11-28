import { beforeEach, describe, expect, it, vi } from "vitest";
import type { VoiceControl, VoiceHost } from "./voice";

// Typing several tasks in the capture box (black glossy theme design, R9):
// Shift+Enter stages the line, a multi-line paste stages all but the last,
// Enter saves them all in order. Same harness as capture-voice.test.ts.
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
    deleteItem: async (id: string) => {
      h.log.push(`delete:${id}`);
    },
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
const rows = () => [...document.querySelectorAll(".rv-row .rv-text")].map((n) => n.textContent);
const input = () => document.querySelector<HTMLInputElement>("#cap-input")!;
const press = (k: string, shiftKey = false) =>
  input().dispatchEvent(new KeyboardEvent("keydown", { key: k, shiftKey, bubbles: true, cancelable: true }));
const type = (text: string) => {
  input().value = text;
  input().dispatchEvent(new Event("input", { bubbles: true }));
};
function paste(text: string): Event {
  const e = new Event("paste", { bubbles: true, cancelable: true }) as Event & { clipboardData: unknown };
  e.clipboardData = { getData: (t: string) => (t === "text/plain" ? text : "") };
  input().dispatchEvent(e);
  return e;
}

describe("capture box: several typed tasks", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    const root = document.createElement("div");
    document.body.append(root);
    h.log = [];
    mountCapture(root);
  });

  it("Shift+Enter stages the line and keeps the box open; Enter saves all in order", async () => {
    type("call Sam tomorrow 5pm");
    press("Enter", true);
    expect(rows()).toEqual(["call Sam tomorrow 5pm"]);
    expect(input().value).toBe("");
    expect(h.log).toEqual([]); // nothing saved yet
    expect(document.querySelector("#cap-hint-save")!.textContent).toBe("save");
    type("buy milk");
    expect(document.querySelector("#cap-hint-save")!.textContent).toBe("save 2");
    press("Enter");
    await settle();
    await settle();
    expect(h.log).toEqual(["capture:call Sam tomorrow 5pm", "capture:buy milk"]);
    expect(rows()).toEqual([]);
  });

  it("Shift+Enter on an empty line does nothing", () => {
    press("Enter", true);
    expect(rows()).toEqual([]);
  });

  it("announces the staged task", () => {
    type("one");
    press("Enter", true);
    type("two");
    press("Enter", true);
    expect(document.querySelector("#cap-voice-live")!.textContent).toBe("Task 2 added");
  });

  it("a multi-line paste stages every line but the last", () => {
    type("note: ");
    input().setSelectionRange(6, 6);
    const e = paste(["- fix login bug !high", "", "- review PR #backend", "- lunch with Priya friday 1pm"].join("\n"));
    expect(e.defaultPrevented).toBe(true);
    expect(rows()).toEqual(["note: fix login bug !high", "review PR #backend"]);
    expect(input().value).toBe("lunch with Priya friday 1pm");
    expect(input().selectionStart).toBe(input().value.length);
  });

  it("a one-line paste is left to the browser", () => {
    const e = paste("just one task");
    expect(e.defaultPrevented).toBe(false);
    expect(rows()).toEqual([]);
  });

  it("reply mode keeps Shift+Enter as save and pastes normally", async () => {
    h.handlers.get("reply:open")!({ line: 'Reply to Priya: "hi"', owedTo: "Priya", source: "Teams", quote: "", keepText: false });
    await settle();
    expect(paste(["a", "b"].join("\n")).defaultPrevented).toBe(false);
    press("Enter", true);
    await settle();
    expect(rows()).toEqual([]);
    expect(h.log.some((l) => l.startsWith("reply:"))).toBe(true);
  });
});

describe("capture box: typed rows left behind", () => {
  it("are not called voice leftovers when the box reopens", async () => {
    document.body.innerHTML = "";
    const root = document.createElement("div");
    document.body.append(root);
    mountCapture(root);
    type("one");
    press("Enter", true);
    window.dispatchEvent(new Event("blur"));
    h.handlers.get("capture:open")!(undefined);
    await settle();
    const status = document.querySelector<HTMLElement>("#cap-hint-status")!;
    expect(status.hidden).toBe(false);
    expect(status.textContent).toBe("1 unsaved task");
  });
});
