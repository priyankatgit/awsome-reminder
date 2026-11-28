import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { VoiceControl, VoiceHost } from "./voice";

// "Hey Awesome": Go shows the box without focus and emits capture:wake. The
// voice controller and the chime are fakes, so the capture box's side (key
// guard, hint, start, hide rules) is driven directly.
type Fake = { host: VoiceHost; calls: string[] };
const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  (window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    disconnect(): void {}
  };
  return {
    handlers: new Map<string, (data: unknown) => void>(),
    fake: null as unknown as Fake,
    log: [] as string[],
    chime: true,
    playChime: async (): Promise<void> => {},
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
    getSettings: async () => ({ defaultTime: "09:00", workDays: ["monday"], hotkey: "Ctrl+Shift+Space" }),
    hideCapture: async () => {
      h.log.push("hideCapture");
    },
    resizeCapture: async () => {},
    withDue: async (input: string) => input,
    deleteItem: async () => {},
    voice: {
      wakeStatus: async () => ({ state: "listening", enabled: true, chime: h.chime }),
    },
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  events: { captureOpen: "capture:open", replyOpen: "reply:open", captureWake: "capture:wake", wakeStatus: "voice:wake-status" },
  on: (name: string, fn: (data: unknown) => void) => {
    h.handlers.set(name, fn);
    return () => {};
  },
}));
vi.mock("./voice", () => ({
  playChime: () => {
    h.fake.calls.push("chime");
    return h.playChime();
  },
  mountVoice: (_root: HTMLElement, host: VoiceHost): VoiceControl => {
    // One record per mount: listeners of earlier tests' boxes stay on the
    // document and must not write into this test's calls.
    const fake: Fake = { host, calls: [] };
    h.fake = fake;
    return {
      toggle: () => void fake.calls.push("toggle"),
      escape: () => {
        fake.calls.push("escape");
        return false;
      },
      cancel: () => void fake.calls.push("cancel"),
      state: () => "idle",
      startSession: (mode) => void fake.calls.push(`start:${mode}`),
      focusGained: () => void fake.calls.push("focusGained"),
      finish: async () => {},
    };
  },
}));

import { mountCapture } from "./capture";

const settle = () => new Promise((r) => setTimeout(r, 0));
const input = () => document.querySelector<HTMLInputElement>("#cap-input")!;
const box = () => document.querySelector<HTMLElement>("#capture")!;
const status = () => document.querySelector<HTMLElement>("#cap-hint-status")!;
const errorRow = () => document.querySelector<HTMLElement>("#cap-error")!;
const wakeHint = () => document.querySelector<HTMLElement>("#cap-wake-hint")!;
const rows = () => [...document.querySelectorAll(".rv-row .rv-text")].map((n) => n.textContent);
const press = (target: EventTarget, key: string, init: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  target.dispatchEvent(e);
  return e;
};
async function wake(): Promise<void> {
  h.handlers.get("capture:wake")!(undefined);
  await settle();
  await settle();
}

describe("capture box wake mode", () => {
  beforeEach(async () => {
    // Boxes of earlier tests still listen on the document (an Esc hides them
    // too): let their hide timers run before this test's log starts.
    await settle();
    document.body.innerHTML = "";
    const root = document.createElement("div");
    document.body.append(root);
    h.log = [];
    h.chime = true;
    h.playChime = async () => {};
    mountCapture(root);
    await settle(); // the initial wakeStatus
    input().blur();
  });

  afterEach(() => {
    // The hotkey releases this box's guard, so its document listener cannot
    // block keys in the next test.
    h.handlers.get("capture:open")!(undefined);
  });

  it("opens without focusing the input, arms the guard, chimes, then starts a wake session", async () => {
    await wake();
    expect(box().classList.contains("is-hidden")).toBe(false);
    expect(document.activeElement).not.toBe(input());
    expect(h.fake.calls).toEqual(["chime", "start:wake"]);
    expect(wakeHint().hidden).toBe(false);
    expect(wakeHint().textContent).toBe("Click or press Ctrl+Shift+Space to type");
  });

  it("no chime when it is switched off", async () => {
    h.handlers.get("voice:wake-status")!({ state: "listening", enabled: true, chime: false }); // Settings changed it
    await wake();
    expect(h.fake.calls).toEqual(["start:wake"]);
  });

  it("the chime setting comes from the wake status at start", async () => {
    h.handlers.get("capture:open")!(undefined); // release this test's first box
    document.body.innerHTML = "";
    const root = document.createElement("div");
    document.body.append(root);
    h.chime = false;
    mountCapture(root);
    await settle();
    await wake();
    expect(h.fake.calls).toEqual(["start:wake"]);
  });

  it("a chime that cannot play is logged and the session still starts", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    h.playChime = async () => {
      throw new Error("audio is suspended");
    };
    await wake();
    expect(warn).toHaveBeenCalled();
    expect(h.fake.calls).toEqual(["chime", "start:wake"]);
    warn.mockRestore();
  });

  it("while armed, printable keys, Enter and Backspace never reach the input", async () => {
    await wake();
    h.fake.host.insert("buy milk"); // a segment landed
    input().blur();
    for (const k of ["a", "Enter", "Backspace", "Tab"]) {
      const e = press(input(), k);
      expect(e.defaultPrevented, k).toBe(true);
    }
    await settle();
    expect(h.log.filter((l) => l.startsWith("capture:"))).toEqual([]); // Enter saved nothing
    expect(input().value).toBe("buy milk");
    expect(document.querySelector<HTMLElement>("#cap-picker")!.hidden).toBe(true); // Tab opened nothing
  });

  it("Esc while armed cancels the session and hides the box, keeping the draft", async () => {
    await wake();
    h.fake.host.insert("buy milk");
    const e = press(document, "Escape");
    expect(e.defaultPrevented).toBe(true);
    expect(h.fake.calls).toContain("cancel");
    expect(box().classList.contains("is-hidden")).toBe(true);
    await settle();
    expect(h.log).toContain("hideCapture");
    expect(input().value).toBe("buy milk");
  });

  it("a mousedown in the box releases the guard and switches to focused rules", async () => {
    await wake();
    box().dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    expect(h.fake.calls).toContain("focusGained");
    expect(wakeHint().hidden).toBe(true);
    expect(press(input(), "a").defaultPrevented).toBe(false);
  });

  it("the hotkey releases the guard and switches to focused rules", async () => {
    await wake();
    h.handlers.get("capture:open")!(undefined);
    expect(h.fake.calls).toContain("focusGained");
    expect(document.activeElement).toBe(input());
    expect(press(input(), "a").defaultPrevented).toBe(false);
  });

  it("Ctrl+M releases the guard without stopping the session", async () => {
    await wake();
    press(document, "m", { ctrlKey: true });
    expect(h.fake.calls).toContain("focusGained");
    expect(h.fake.calls).not.toContain("toggle");
    expect(document.activeElement).toBe(input());
    expect(press(input(), "a").defaultPrevented).toBe(false);
  });

  it("window focus right after the wake show is not engagement; later it is", async () => {
    await wake();
    window.dispatchEvent(new Event("focus")); // the OS focusing the shown window
    expect(h.fake.calls).not.toContain("focusGained");
    expect(document.activeElement).not.toBe(input());
    const now = performance.now();
    const spy = vi.spyOn(performance, "now").mockReturnValue(now + 5000);
    window.dispatchEvent(new Event("focus")); // the user clicked or alt-tabbed in
    spy.mockRestore();
    expect(h.fake.calls).toContain("focusGained");
    expect(document.activeElement).toBe(input());
  });

  it("a quiet wake hides the box with no error", async () => {
    await wake();
    h.fake.host.sessionEnded("wake", "quiet-wake");
    expect(box().classList.contains("is-hidden")).toBe(true);
    await settle();
    expect(h.log).toContain("hideCapture");
    expect(errorRow().hidden).toBe(true);
    expect(errorRow().textContent).toBe("");
  });

  it("a wake session ending with text hides and keeps it; the next open says so", async () => {
    await wake();
    h.fake.host.insert("buy milk");
    h.fake.host.sessionEnded("wake", "silence");
    expect(box().classList.contains("is-hidden")).toBe(true);
    await settle();
    expect(h.log).toContain("hideCapture");
    h.handlers.get("capture:open")!(undefined);
    expect(input().value).toBe("buy milk");
    expect(status().hidden).toBe(false);
    expect(status().textContent).toBe("1 unsaved task from voice");
  });

  it("rows and text from a wake session count together on the next open", async () => {
    await wake();
    h.fake.host.insert("call Sam");
    h.fake.host.addTask();
    h.fake.host.insert("buy milk");
    h.fake.host.sessionEnded("wake", "silence");
    await settle();
    h.handlers.get("capture:open")!(undefined);
    expect(rows()).toEqual(["call Sam"]);
    expect(status().textContent).toBe("2 unsaved tasks from voice");
  });

  it("a spoken save or cancel with something to act on keeps the box for its note", async () => {
    await wake();
    h.fake.host.insert("buy milk");
    h.fake.host.sessionEnded("wake", "save"); // saveAll follows, then the Undo note hides the box
    await settle();
    expect(box().classList.contains("is-hidden")).toBe(false);
    expect(h.log).not.toContain("hideCapture");
  });

  it("a spoken save or cancel with nothing to act on hides the box", async () => {
    await wake();
    h.fake.host.sessionEnded("wake", "cancel");
    expect(box().classList.contains("is-hidden")).toBe(true);
    await settle();
    expect(h.log).toContain("hideCapture");
  });

  it("the hotkey after an Esc-hidden wake still switches a draining session to focused rules", async () => {
    await wake();
    press(document, "Escape"); // hidden; the session may still be draining
    h.handlers.get("capture:open")!(undefined);
    expect(h.fake.calls.filter((c) => c === "focusGained")).toHaveLength(1);
  });

  it("a session the user engaged with does not hide the box when it ends", async () => {
    await wake();
    h.fake.host.insert("buy milk");
    h.fake.host.sessionEnded("focused", "silence");
    await settle();
    expect(box().classList.contains("is-hidden")).toBe(false);
    expect(h.log).not.toContain("hideCapture");
  });

  it("a hidden box with a reply draft opens as a normal box on a wake", async () => {
    h.handlers.get("reply:open")!({ line: "Reply to Priya: \"hi\"", owedTo: "Priya", source: "Teams", quote: "", keepText: false });
    await settle();
    window.dispatchEvent(new Event("blur")); // Go hid it; the reply draft stays
    await wake();
    expect(box().dataset.mode).not.toBe("reply");
    expect(input().value).toBe("");
    expect(h.fake.host.multi()).toBe(true);
  });
});
