import { describe, expect, it, vi } from "vitest";
import type { VoiceModel, VoiceStatus, WakeStatus } from "./api";

const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  return {
    wake: undefined as unknown as WakeStatus,
    voice: undefined as unknown as VoiceStatus,
    setWake: [] as boolean[],
    notInstalled: true,
    listeners: new Map<string, Set<(d: unknown) => void>>(),
    installWake: (async () => {}) as () => Promise<void>,
    cancelWake: (async () => {}) as () => Promise<void>,
    models: [] as VoiceModel[],
    setModel: [] as string[],
    setModelErr: "",
    cancelSwitch: 0,
    words: "",
    setWordsErr: "",
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
      installWake: () => h.installWake(),
      cancelInstall: async () => {},
      cancelWakeInstall: () => h.cancelWake(),
      models: async () => h.models,
      setModel: async (id: string) => {
        h.setModel.push(id);
        if (h.setModelErr) throw new Error(h.setModelErr);
      },
      cancelModelSwitch: async () => {
        h.cancelSwitch++;
      },
      words: async () => h.words,
      setWords: async (w: string) => {
        if (h.setWordsErr) throw new Error(h.setWordsErr);
        h.words = w.split(",").map((x) => x.trim()).filter(Boolean).join(", ");
      },
    },
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  events: { voiceStatus: "voice:status", wakeStatus: "voice:wake-status" },
  on: (name: string, fn: (d: unknown) => void) => {
    if (!h.listeners.has(name)) h.listeners.set(name, new Set());
    h.listeners.get(name)!.add(fn);
    return () => h.listeners.get(name)!.delete(fn);
  },
}));

import {
  consentBytes,
  consentText,
  isNotInstalled,
  megabytes,
  modelHelp,
  modelOptionLabel,
  mountVoiceSection,
  recommendTip,
  runInstall,
  statusLine,
  switchLine,
  watchDictation,
  watchWake,
} from "./voice-settings";

const wake = (o: Partial<WakeStatus> = {}): WakeStatus => ({
  state: "off", enabled: false, chime: true, phase: "", done: 0, total: 0, download: 8_300_000, error: "", hint: "", ...o,
});
const voice = (o: Partial<VoiceStatus> = {}): VoiceStatus => ({
  state: "ready", phase: "", done: 0, total: 0, download: 0, engine: "bundled", error: "", hint: "",
  model: "base", target: "", switchError: "", ...o,
});
const MODELS: VoiceModel[] = [
  { id: "base", label: "Base", hint: "Fastest.", size: 147_964_211, recommended: false, installed: true, active: true },
  { id: "small", label: "Small", hint: "Balanced.", size: 190_098_681, recommended: false, installed: false, active: false },
  { id: "turbo", label: "Large v3 Turbo", hint: "Most accurate.", size: 574_041_195, recommended: true, installed: false, active: false },
];

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
      watchWake: () => ({ done: Promise.resolve(), recheck: async () => {}, stop: () => {} }),
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

describe("Wake download (installWake returns at once, progress arrives as events)", () => {
  const tick = () => new Promise((r) => setTimeout(r, 0));
  const emit = async (w: WakeStatus) => {
    h.wake = w;
    h.listeners.get("voice:wake-status")?.forEach((f) => f(w));
    await tick();
  };
  async function startDownload() {
    h.listeners.clear();
    h.wake = wake({ state: "absent", download: 8_300_000 });
    h.voice = voice();
    h.notInstalled = true;
    h.setWake = [];
    h.installWake = async () => {}; // returns immediately, like Go's Install
    document.body.innerHTML = "<section id='s'></section>";
    const host = document.getElementById("s")!;
    mountVoiceSection(host);
    await tick();
    const sw = host.querySelector<HTMLInputElement>("#wake-on")!;
    sw.checked = true;
    sw.dispatchEvent(new Event("change"));
    await tick();
    await tick();
    const btn = (t: string) => [...host.querySelectorAll("#wake-consent button")].find((b) => b.textContent?.includes(t)) as HTMLButtonElement;
    btn("Download").click();
    await tick();
    await tick();
    return { host, sw, btn, box: host.querySelector<HTMLElement>("#wake-consent")! };
  }

  it("keeps the progress and Cancel up until the wake word is listening, without asking for consent again", async () => {
    const { host, sw, box } = await startDownload();
    await emit(wake({ state: "installing", phase: "model", done: 2_000_000, total: 8_000_000, download: 8_000_000 }));
    expect(box.hidden).toBe(false);
    expect(host.querySelector("#wake-progress")!.getAttribute("aria-valuenow")).toBe("25");
    expect(box.textContent).toContain("Cancel");
    expect(box.textContent).not.toContain("one-time download");
    expect(h.setWake).toEqual([true]); // only the first, rejected, attempt
    h.notInstalled = false;
    await emit(wake({ state: "listening", enabled: true }));
    expect(box.hidden).toBe(true);
    expect(sw.checked).toBe(true);
    expect(host.querySelector("#wake-status-text")!.textContent).toBe('Listening for "Hey Awesome"');
    expect(h.setWake).toEqual([true]);
  });

  it("shows the failure with Try again when the download ends absent with an error", async () => {
    const { host, sw, box } = await startDownload();
    await emit(wake({ state: "installing", done: 1, total: 8_000_000 }));
    await emit(wake({ state: "absent", error: "download failed: timeout", download: 8_300_000 }));
    expect(box.hidden).toBe(true);
    expect(sw.checked).toBe(false);
    expect(host.querySelector("#wake-status-text")!.textContent).toBe("download failed: timeout");
    const retry = host.querySelector<HTMLButtonElement>("#wake-retry")!;
    expect(retry.hidden).toBe(false);
    retry.click();
    await tick();
    await tick();
    expect(box.hidden).toBe(false); // consent again
  });

  it("Cancel calls cancelWakeInstall and ends quietly", async () => {
    const { host, btn, box } = await startDownload();
    await emit(wake({ state: "installing", done: 1, total: 8_000_000 }));
    let cancelled = 0;
    h.cancelWake = async () => void cancelled++;
    btn("Cancel").click();
    await tick();
    await emit(wake({ state: "absent", error: "", download: 8_300_000 }));
    expect(cancelled).toBe(1);
    expect(box.hidden).toBe(true);
    expect(host.querySelector<HTMLElement>("#wake-error")!.hidden).toBe(true);
    expect(host.querySelector<HTMLElement>("#wake-status")!.hidden).toBe(true);
  });
});

describe("watchWake", () => {
  function setup(current: WakeStatus) {
    let push: (s: WakeStatus) => void = () => {};
    const w = watchWake((cb) => ((push = cb), () => {}), async () => current);
    return { w, push: (s: WakeStatus) => push(s) };
  }
  it("ignores an absent seen before installing, resolves after installing then listening", async () => {
    const { w, push } = setup(wake({ state: "absent" }));
    push(wake({ state: "absent" }));
    push(wake({ state: "installing" }));
    push(wake({ state: "listening", enabled: true }));
    await expect(w.done).resolves.toBeUndefined();
  });
  it("rejects with the error on absent after installing", async () => {
    const { w, push } = setup(wake({ state: "absent" }));
    push(wake({ state: "installing" }));
    push(wake({ state: "absent", error: "boom" }));
    await expect(w.done).rejects.toThrow("boom");
  });
});

describe("speech model helpers", () => {
  it("labels options with size and the recommendation", () => {
    expect(modelOptionLabel(MODELS[0])).toBe("Base (148 MB)");
    expect(modelOptionLabel(MODELS[2])).toBe("Large v3 Turbo (574 MB) · Recommended");
  });
  it("mentions the download only for a model that is not on disk while dictation is ready", () => {
    expect(modelHelp(MODELS, "base", voice())).toBe("Fastest.");
    expect(modelHelp(MODELS, "turbo", voice())).toBe("Most accurate. Choosing it downloads 574 MB; dictation keeps working meanwhile.");
    expect(modelHelp(MODELS, "turbo", voice({ state: "absent" }))).toBe("Most accurate.");
    expect(modelHelp(MODELS, "turbo", voice({ target: "turbo" }))).toBe("Most accurate.");
    expect(modelHelp(MODELS, "nope", voice())).toBe("");
  });
  it("recommends the better model only while another is active", () => {
    expect(recommendTip(MODELS, voice())).toContain("Large v3 Turbo is much more accurate");
    expect(recommendTip(MODELS, voice())).toContain("574 MB");
    expect(recommendTip(MODELS, voice({ model: "turbo" }))).toBe("");
    expect(recommendTip(MODELS, voice({ target: "turbo" }))).toBe("");
    expect(recommendTip(MODELS, voice({ state: "unsupported" }))).toBe("");
  });
  it("describes a running switch", () => {
    expect(switchLine(voice(), MODELS)).toBeNull();
    expect(switchLine(voice({ target: "turbo", done: 100_000_000, total: 574_041_195 }), MODELS)).toBe(
      "Downloading Large v3 Turbo… 100 MB of 574 MB. Dictation uses Base until it finishes.",
    );
  });
});

describe("Speech model and words in the Voice section", () => {
  function mount(v: VoiceStatus) {
    h.voice = v;
    h.wake = wake();
    h.models = MODELS;
    h.setModel = [];
    h.setModelErr = "";
    h.cancelSwitch = 0;
    h.words = "Venkatesh, Pune";
    h.setWordsErr = "";
    const host = document.createElement("section");
    document.body.replaceChildren(host);
    mountVoiceSection(host);
    const $ = <T extends HTMLElement>(id: string) => host.querySelector<T>(`#${id}`)!;
    return { host, $ };
  }
  const flush = () => new Promise((r) => setTimeout(r, 0));

  it("shows the models with the active one chosen, the tip and the saved words", async () => {
    const { $ } = mount(voice());
    await flush();
    const sel = $<HTMLSelectElement>("speech-model");
    expect([...sel.options].map((o) => o.textContent)).toEqual([
      "Base (148 MB)",
      "Small (190 MB)",
      "Large v3 Turbo (574 MB) · Recommended",
    ]);
    expect(sel.value).toBe("base");
    expect(sel.disabled).toBe(false);
    expect($("speech-model-tip").hidden).toBe(false);
    expect($<HTMLInputElement>("speech-words").value).toBe("Venkatesh, Pune");
    expect($("speech-status").hidden).toBe(true);
  });

  it("choosing a model calls setModel and shows a refusal", async () => {
    const { $ } = mount(voice());
    await flush();
    const sel = $<HTMLSelectElement>("speech-model");
    h.setModelErr = "voice: wait for the download to finish";
    sel.value = "turbo";
    sel.dispatchEvent(new Event("change"));
    await flush();
    await flush();
    expect(h.setModel).toEqual(["turbo"]);
    expect($("speech-error").textContent).toContain("wait for the download");
  });

  it("shows switch progress with Cancel and locks the select", async () => {
    const { $ } = mount(voice({ target: "turbo", phase: "model", done: 287_000_000, total: 574_041_195 }));
    await flush();
    const sel = $<HTMLSelectElement>("speech-model");
    expect(sel.value).toBe("turbo");
    expect(sel.disabled).toBe(true);
    expect($("speech-status").hidden).toBe(false);
    expect($("speech-status-text").textContent).toContain("287 MB of 574 MB");
    expect($("speech-progress").getAttribute("aria-valuenow")).toBe("50");
    $<HTMLButtonElement>("speech-cancel").click();
    await flush();
    expect(h.cancelSwitch).toBe(1);
  });

  it("reloads the models when the first install finishes", async () => {
    const { $ } = mount(voice({ state: "absent", model: "small" }));
    await flush();
    expect($("speech-model-help").textContent).toBe("Balanced.");
    h.models = MODELS.map((m) => ({ ...m, installed: m.id === "small", active: m.id === "small" }));
    h.voice = voice({ model: "small" });
    for (const fn of h.listeners.get("voice:status") ?? []) fn(h.voice);
    await flush();
    await flush();
    expect($("speech-model-help").textContent).toBe("Balanced.");
    expect($<HTMLSelectElement>("speech-model").value).toBe("small");
  });

  it("saves words on change and shows the normalised list, or the error", async () => {
    const { $ } = mount(voice());
    await flush();
    const input = $<HTMLInputElement>("speech-words");
    input.value = " Shubham ,  Aadhaar,, Jira ";
    input.dispatchEvent(new Event("change"));
    await flush();
    await flush();
    expect(input.value).toBe("Shubham, Aadhaar, Jira");
    expect($("speech-words-error").hidden).toBe(true);
    h.setWordsErr = 'voice: keep "Words I use" under 300 characters';
    input.value = "x".repeat(10);
    input.dispatchEvent(new Event("change"));
    await flush();
    await flush();
    expect($("speech-words-error").hidden).toBe(false);
    expect($("speech-words-error").textContent).toContain("300 characters");
  });
});
