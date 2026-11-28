import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  return {
    saved: [] as unknown[],
    saveErr: "" as string,
    recording: [] as boolean[],
    status: { hotkeyErr: "", replyHotkeyErr: "" },
  };
});
vi.mock("./api", () => ({
  api: {
    getSettings: async () => ({
      hotkey: "Ctrl+Shift+Space",
      leadTimes: ["1h0m0s"],
      defaultTime: "09:00",
      nextWeekDay: "monday",
      nextWeekTime: "09:00",
      exportPath: "",
      autostart: false,
      showPendingCount: true,
      replyHotkey: "Ctrl+Alt+R",
      workDays: ["monday", "tuesday", "wednesday", "thursday", "friday"],
      endOfDay: "17:30",
      endOfDaySweep: true,
    }),
    status: async () => h.status,
    saveSettings: async (s: unknown) => {
      if (h.saveErr) throw new Error(h.saveErr);
      h.saved.push(s);
    },
    setHotkeyRecording: async (on: boolean) => void h.recording.push(on),
    chooseExportPath: async () => "",
    closeWindow: async () => {},
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  isMac: () => false,
  isWindows: () => true,
}));
vi.mock("./sync-settings", () => ({ mountSyncSection: () => {} }));
vi.mock("./voice-settings", () => ({ mountVoiceSection: () => {} }));
import { mountSettings } from "./settings";

const tick = () => new Promise((r) => setTimeout(r, 0));
async function mount(): Promise<HTMLElement> {
  document.body.innerHTML = "<div id='root'></div>";
  const root = document.getElementById("root")!;
  mountSettings(root);
  await tick();
  await tick();
  return root;
}
const q = <T extends HTMLElement>(r: HTMLElement, s: string) => r.querySelector<T>(s)!;
const days = (r: HTMLElement) => [...r.querySelectorAll<HTMLButtonElement>(".day-chip")];

describe("Replies settings", () => {
  beforeEach(() => {
    h.saved = [];
    h.saveErr = "";
    h.recording = [];
    h.status = { hotkeyErr: "", replyHotkeyErr: "" };
  });

  it("shows the four fields from settings, after the hotkey field", async () => {
    const r = await mount();
    expect(q(r, "#reply-hotkey-btn").textContent).toContain("R");
    expect(days(r).map((b) => b.getAttribute("aria-pressed"))).toEqual(["true", "true", "true", "true", "true", "false", "false"]);
    expect(days(r).map((b) => b.textContent)).toEqual(["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]);
    expect(q<HTMLInputElement>(r, "#end-of-day").value).toBe("17:30");
    expect(q<HTMLInputElement>(r, "#eod-sweep").checked).toBe(true);
    const order = [...r.querySelectorAll("#hotkey-btn, #reply-hotkey-btn, #leads-label")];
    expect(order.map((n) => n.id)).toEqual(["hotkey-btn", "reply-hotkey-btn", "leads-label"]);
  });

  it("round-trips edits to the four fields on save", async () => {
    const r = await mount();
    days(r)[5].click(); // Sat on
    days(r)[0].click(); // Mon off
    const eod = q<HTMLInputElement>(r, "#end-of-day");
    eod.value = "18:15";
    eod.dispatchEvent(new Event("input", { bubbles: true }));
    const sweep = q<HTMLInputElement>(r, "#eod-sweep");
    sweep.checked = false;
    sweep.dispatchEvent(new Event("change", { bubbles: true }));
    const btn = q<HTMLButtonElement>(r, "#reply-hotkey-btn");
    btn.click();
    btn.dispatchEvent(new KeyboardEvent("keydown", { key: "j", code: "KeyJ", ctrlKey: true, altKey: true, bubbles: true }));
    expect(h.recording).toEqual([true, false]);
    q<HTMLFormElement>(r, "#settings-form").requestSubmit();
    await tick();
    await tick();
    expect(h.saved[0]).toMatchObject({
      replyHotkey: "Ctrl+Alt+J",
      workDays: ["tuesday", "wednesday", "thursday", "friday", "saturday"],
      endOfDay: "18:15",
      endOfDaySweep: false,
    });
  });

  it("keeps work days in Monday..Sunday order", async () => {
    const r = await mount();
    days(r)[6].click();
    days(r)[5].click();
    q<HTMLFormElement>(r, "#settings-form").requestSubmit();
    await tick();
    await tick();
    expect((h.saved[0] as { workDays: string[] }).workDays.slice(-2)).toEqual(["saturday", "sunday"]);
  });

  it("clearing every work day shows the error and disables Save", async () => {
    const r = await mount();
    for (const b of days(r)) if (b.getAttribute("aria-pressed") === "true") b.click();
    const err = q(r, "#workdays-error");
    expect(err.hidden).toBe(false);
    expect(err.textContent).toBe("Pick at least one working day");
    expect(q<HTMLButtonElement>(r, "#settings-save").disabled).toBe(true);
    days(r)[2].click();
    expect(err.hidden).toBe(true);
    expect(q<HTMLButtonElement>(r, "#settings-save").disabled).toBe(false);
  });

  it("an empty end-of-day time shows an inline error and does not save", async () => {
    const r = await mount();
    const eod = q<HTMLInputElement>(r, "#end-of-day");
    eod.value = "";
    eod.dispatchEvent(new Event("input", { bubbles: true }));
    q<HTMLFormElement>(r, "#settings-form").requestSubmit();
    await tick();
    expect(q(r, "#end-of-day-error").textContent).toBe("Enter a time, for example 17:30");
    expect(h.saved).toEqual([]);
  });

  it("routes the clash error to the reply hotkey field", async () => {
    h.saveErr = "Pick a different key from the capture hotkey";
    const r = await mount();
    days(r)[6].click();
    q<HTMLFormElement>(r, "#settings-form").requestSubmit();
    await tick();
    await tick();
    expect(q(r, "#reply-hotkey-error").textContent).toBe("Pick a different key from the capture hotkey");
    expect(q(r, "#reply-hotkey-btn").getAttribute("aria-invalid")).toBe("true");
  });

  it("routes endOfDay and workDays Go errors to their fields", async () => {
    h.saveErr = "endOfDay: invalid time \"x\", want HH:MM\nworkDays: pick at least one working day";
    const r = await mount();
    days(r)[6].click();
    q<HTMLFormElement>(r, "#settings-form").requestSubmit();
    await tick();
    await tick();
    expect(q(r, "#end-of-day-error").hidden).toBe(false);
    expect(q(r, "#workdays-error").textContent).toBe("Pick at least one working day");
  });

  it("shows a reply hotkey registration failure from Status inline", async () => {
    h.status = { hotkeyErr: "", replyHotkeyErr: "Ctrl+Alt+R is already used by another app" };
    const r = await mount();
    expect(q(r, "#reply-hotkey-error").textContent).toBe("Ctrl+Alt+R is already used by another app");
  });

  it("a Validate failure on the reply hotkey saves nothing: draft stays dirty, Save enabled", async () => {
    h.saveErr = 'replyHotkey: invalid combo "Ctrl+"';
    const r = await mount();
    days(r)[6].click();
    q<HTMLFormElement>(r, "#settings-form").requestSubmit();
    await tick();
    await tick();
    expect(q(r, "#reply-hotkey-error").textContent).toBe('Invalid combo "Ctrl+"');
    expect(q(r, "#settings-status").textContent).toBe("");
    expect(q<HTMLButtonElement>(r, "#settings-save").disabled).toBe(false);
  });

  it("a reply hotkey registration failure after a save is 'Saved, with a problem'", async () => {
    h.saveErr = "reply hotkey: already used";
    h.status = { hotkeyErr: "", replyHotkeyErr: "Already used" }; // Go reports it in Status too
    const r = await mount();
    days(r)[6].click();
    q<HTMLFormElement>(r, "#settings-form").requestSubmit();
    await tick();
    await tick();
    expect(q(r, "#reply-hotkey-error").textContent).toBe("Already used");
    expect(q(r, "#settings-status").textContent).toBe("Saved, with a problem");
    expect(q<HTMLButtonElement>(r, "#settings-save").disabled).toBe(true);
  });
});
