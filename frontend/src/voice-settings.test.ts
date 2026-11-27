import { describe, expect, it, vi } from "vitest";
import type { VoiceStatus, WakeStatus } from "./api";

const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  return {
    wake: undefined as unknown as WakeStatus,
    voice: undefined as unknown as VoiceStatus,
    setWake: [] as boolean[],
    notInstalled: true,
  };
});
vi.mock("./api", () => ({
  api: {
    voice: {
      status: async () => h.voice,
      wakeStatus: async () => h.wake,
      setWakeEnabled: async (on: boolean) => {
        h.setWake.push(on);
        if (on && h.notInstalled) throw new Error("voice: wake word files are not installed");
      },
      setWakeChime: async () => {},
      install: async () => {},
      installWake: async () => {},
      cancelInstall: async () => {},
      cancelWakeInstall: async () => {},
    },
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  events: { voiceStatus: "voice:status", wakeStatus: "voice:wake-status" },
  on: () => () => {},
}));

import { consentBytes, consentText, mountVoiceSection, isNotInstalled, megabytes, runInstall, statusLine, watchDictation } from "./voice-settings";

const wake = (o: Partial<WakeStatus> = {}): WakeStatus => ({
  state: "off", enabled: false, chime: true, phase: "", done: 0, total: 0, download: 8_300_000, error: "", hint: "", ...o,
});
const voice = (o: Partial<VoiceStatus> = {}): VoiceStatus => ({
  state: "ready", phase: "", done: 0, total: 0, download: 0, engine: "bundled", error: "", hint: "", ...o,
});

describe("helpers", () => {
  it("formats megabytes like the capture box", () => {
    expect(megabytes(5_250_000)).toBe("5.3 MB");
    expect(megabytes(62_000_000)).toBe("62 MB");
  });
  it("detects the not-installed rejection by its text", () => {
    expect(isNotInstalled(new Error("voice: wake word files are not installed"))).toBe(true);
    expect(isNotInstalled(new Error("boom"))).toBe(false);
  });
  it("adds the dictation download only when dictation is absent", () => {
    expect(consentBytes(wake(), voice())).toBe(8_300_000);
    expect(consentBytes(wake(), voice({ state: "absent", download: 40_000_000 }))).toBe(48_300_000);
    expect(consentText(wake(), voice({ state: "absent", download: 40_000_000 }))).toContain("48 MB");
  });
  it("words the status line", () => {
    expect(statusLine(wake())).toBeNull();
    expect(statusLine(wake({ enabled: true, state: "listening" }))).toEqual({ text: 'Listening for "Hey Awesome"', error: false });
    expect(statusLine(wake({ enabled: true, state: "starting" }))?.text).toBe("Starting…");
    expect(statusLine(wake({ enabled: true, state: "error", error: "no mic", hint: "Check privacy." }))).toEqual({
      text: "no mic Check privacy.",
      error: true,
    });
  });
});

describe("runInstall", () => {
  function deps(absent: boolean, order: string[], over: Partial<Parameters<typeof runInstall>[0]> = {}) {
    return {
      dictationAbsent: absent,
      installDictation: async () => void order.push("dictation"),
      watchDictation: () => ({ done: Promise.resolve(), recheck: async () => {}, stop: () => void order.push("stop") }),
      installWake: async () => void order.push("wake"),
      onPhase: (p: string) => void order.push(`phase:${p}`),
      cancelled: () => false,
      ...over,
    } as Parameters<typeof runInstall>[0];
  }
  it("installs dictation first when absent, then the wake word", async () => {
    const order: string[] = [];
    await runInstall(deps(true, order));
    expect(order).toEqual(["phase:dictation", "dictation", "stop", "phase:wake", "wake"]);
  });
  it("skips dictation when it is installed", async () => {
    const order: string[] = [];
    await runInstall(deps(false, order));
    expect(order).toEqual(["phase:wake", "wake"]);
  });
  it("does not start the wake download when dictation fails or the flow was cancelled", async () => {
    const order: string[] = [];
    const failing = deps(true, order, {
      watchDictation: () => ({ done: Promise.reject(new Error("net")), recheck: async () => {}, stop: () => {} }),
    });
    await expect(runInstall(failing)).rejects.toThrow("net");
    expect(order).not.toContain("wake");
    const o2: string[] = [];
    await runInstall(deps(false, o2, { cancelled: () => true }));
    expect(o2).toEqual([]);
  });
});

describe("watchDictation", () => {
  function setup(current: VoiceStatus) {
    let push: (s: VoiceStatus) => void = () => {};
    const w = watchDictation((cb) => ((push = cb), () => {}), async () => current);
    return { w, push: (s: VoiceStatus) => push(s) };
  }
  it("resolves on ready", async () => {
    const { w, push } = setup(voice({ state: "installing" }));
    push(voice({ state: "ready" }));
    await expect(w.done).resolves.toBeUndefined();
  });
  it("rejects when installing falls back to absent (cancel or failure)", async () => {
    const { w, push } = setup(voice({ state: "installing" }));
    push(voice({ state: "installing" }));
    push(voice({ state: "absent", error: "" }));
    await expect(w.done).rejects.toThrow("cancelled");
  });
  it("ignores an absent status seen before the install began", async () => {
    const { w, push } = setup(voice({ state: "absent" }));
    push(voice({ state: "absent" }));
    push(voice({ state: "ready" }));
    await expect(w.done).resolves.toBeUndefined();
  });
  it("recheck catches a ready that fired before the watch", async () => {
    const { w } = setup(voice({ state: "ready" }));
    await w.recheck();
    await expect(w.done).resolves.toBeUndefined();
  });
});

describe("Voice section", () => {
  const tick = () => new Promise((r) => setTimeout(r, 0));
  it("asks for consent with both sizes when the wake files are absent, and Not now backs out", async () => {
    h.wake = wake({ state: "absent", download: 8_300_000 });
    h.voice = voice({ state: "absent", download: 40_000_000 });
    h.setWake = [];
    document.body.innerHTML = "<section id='s'></section>";
    const host = document.getElementById("s")!;
    mountVoiceSection(host);
    await tick();
    expect(host.textContent).toContain("Voice settings apply right away.");
    expect(host.querySelector<HTMLInputElement>("#wake-chime")!.disabled).toBe(true);
    const sw = host.querySelector<HTMLInputElement>("#wake-on")!;
    sw.checked = true;
    sw.dispatchEvent(new Event("change"));
    await tick();
    await tick();
    const box = host.querySelector<HTMLElement>("#wake-consent")!;
    expect(box.hidden).toBe(false);
    expect(box.textContent).toContain("48 MB");
    expect(sw.checked).toBe(false);
    [...box.querySelectorAll("button")].find((b) => b.textContent === "Not now")!.click();
    expect(box.hidden).toBe(true);
  });
  it("disables the wake switch and shows the hint when dictation is unsupported", async () => {
    h.wake = wake();
    h.voice = voice({ state: "unsupported", hint: "Install the VC++ runtime." });
    document.body.innerHTML = "<section id='s'></section>";
    const host = document.getElementById("s")!;
    mountVoiceSection(host);
    await tick();
    expect(host.querySelector<HTMLInputElement>("#wake-on")!.disabled).toBe(true);
    expect(host.querySelector<HTMLElement>("#wake-hint")!.textContent).toBe("Install the VC++ runtime.");
  });
});
