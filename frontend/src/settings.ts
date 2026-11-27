// Settings window (#settings): one scrolling form, labels above fields,
// helper text below, errors inline. Follows the "Settings" section of the UI
// guide. Go validates on save (internal/settings Validate); the checks here
// mirror it so most mistakes show before saving.
import { api, errorText, isMac, isWindows, type Settings } from "./api";
import { el, keycaps } from "./dom";
import { icons } from "./icons";
import { mountSyncSection } from "./sync-settings";
import { mountVoiceSection } from "./voice-settings";

const SEC = 1;
const MIN = 60 * SEC;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
/** Mirrors internal/settings: at most 5 lead times, each > 0 and <= 7 days. */
const MAX_LEADS = 5;
const MAX_LEAD = 7 * DAY;

const WEEKDAYS = ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"];

// ---------- Durations ----------

/** Seconds in a Go duration string such as "1h0m0s" or "10m0s"; NaN if invalid. */
export function parseGoDuration(s: string): number {
  const units: Record<string, number> = { h: HOUR, m: MIN, s: SEC, ms: 1e-3, us: 1e-6, "µs": 1e-6, ns: 1e-9 };
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|h|m|s)/gy;
  let total = 0;
  let matched = 0;
  for (let m = re.exec(s); m; m = re.exec(s)) {
    total += parseFloat(m[1]) * units[m[2]];
    matched = re.lastIndex;
  }
  return matched === s.length && s !== "" ? Math.round(total) : NaN;
}

/** The Go duration string for whole seconds, as time.Duration.String writes it. */
export function goDuration(sec: number): string {
  const h = Math.floor(sec / HOUR);
  const m = Math.floor((sec % HOUR) / MIN);
  const s = sec % MIN;
  if (h > 0) return `${h}h${m}m${s}s`;
  if (m > 0) return `${m}m${s}s`;
  return `${s}s`;
}

/** Parses user input such as "30m", "2h", "1d" or "1h30m" to seconds; NaN if invalid. */
export function parseLead(input: string): number {
  const s = input.toLowerCase().replace(/\s+/g, "");
  const m = /^(?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?$/.exec(s);
  if (!s || !m) return NaN;
  return (Number(m[1] ?? 0) * DAY) + (Number(m[2] ?? 0) * HOUR) + (Number(m[3] ?? 0) * MIN);
}

/** "1d", "1h 30m", "10m": a lead time for its chip. */
export function leadLabel(sec: number): string {
  const parts: string[] = [];
  let rest = sec;
  for (const [unit, size] of [["d", DAY], ["h", HOUR], ["m", MIN], ["s", SEC]] as const) {
    const n = Math.floor(rest / size);
    if (n > 0) parts.push(`${n}${unit}`);
    rest -= n * size;
  }
  return parts.join(" ") || "0m";
}

/** Long form for screen readers: "1 hour 30 minutes". */
function leadSpoken(sec: number): string {
  const parts: string[] = [];
  let rest = sec;
  for (const [unit, size] of [["day", DAY], ["hour", HOUR], ["minute", MIN], ["second", SEC]] as const) {
    const n = Math.floor(rest / size);
    if (n > 0) parts.push(`${n} ${unit}${n === 1 ? "" : "s"}`);
    rest -= n * size;
  }
  return parts.join(" ");
}

// ---------- Hotkey recorder ----------

const KEY_HINT = "Use a letter, a digit, F1–F12, Space, Enter or Tab";

/** The combo for a keydown in the hotkey format Go parses ("Ctrl+Shift+Space"), or a hint. */
export function comboFor(e: KeyboardEvent, mac: boolean): { combo?: string; hint?: string } {
  const mods: string[] = [];
  if (e.ctrlKey) mods.push("Ctrl");
  if (e.altKey) mods.push("Alt");
  if (e.shiftKey) mods.push("Shift");
  if (e.metaKey) {
    if (!mac) return { hint: "The Windows key can't be part of the hotkey" };
    mods.push("Cmd");
  }
  if (["Control", "Alt", "Shift", "Meta", "AltGraph", "OS"].includes(e.key)) {
    return { hint: mods.length ? `${mods.join(" + ")} + …` : undefined };
  }
  let key = "";
  const c = e.code;
  if (/^Key[A-Z]$/.test(c)) key = c.slice(3);
  else if (/^Digit[0-9]$/.test(c)) key = c.slice(5);
  else if (/^F([1-9]|1[0-2])$/.test(c)) key = c;
  else if (c === "Space" || c === "Enter" || c === "Tab") key = c;
  else if (c === "NumpadEnter") key = "Enter";
  else if (c === "" || c === "Unidentified") {
    // No physical key code (some remote or injected input): use the key.
    const k = e.key;
    if (/^[a-z0-9]$/i.test(k)) key = k.toUpperCase();
    else if (k === " " || k === "Spacebar") key = "Space";
    else if (k === "Enter" || k === "Tab" || /^F([1-9]|1[0-2])$/.test(k)) key = k;
  }
  if (!key) return { hint: KEY_HINT };
  // Mirrors hotkey.ParseCombo: Shift alone would take over ordinary typing.
  if (!e.ctrlKey && !e.altKey && !(mac && e.metaKey)) {
    return { hint: mac ? "Add Ctrl, Alt or Cmd (Shift alone isn't enough)" : "Add Ctrl or Alt (Shift alone isn't enough)" };
  }
  return { combo: [...mods, key].join("+") };
}

// ---------- Form model ----------

interface Draft {
  hotkey: string;
  leads: number[]; // seconds, longest first
  defaultTime: string;
  nextWeekDay: string;
  nextWeekTime: string;
  exportPath: string;
  autostart: boolean;
  showPendingCount: boolean;
  replyHotkey: string;
  workDays: string[];
  endOfDay: string;
  endOfDaySweep: boolean;
  // Go owns the shared-settings stamp; it is carried through untouched.
  sharedUpdatedAt?: number;
  sharedUpdatedBy?: string;
}

function toDraft(s: Settings): Draft {
  return {
    hotkey: s.hotkey,
    leads: (s.leadTimes ?? []).map(parseGoDuration).filter((n) => !Number.isNaN(n)).sort((a, b) => b - a),
    defaultTime: s.defaultTime,
    nextWeekDay: s.nextWeekDay,
    nextWeekTime: s.nextWeekTime,
    exportPath: s.exportPath,
    autostart: s.autostart,
    showPendingCount: s.showPendingCount,
    replyHotkey: s.replyHotkey,
    workDays: [...(s.workDays ?? [])],
    endOfDay: s.endOfDay,
    endOfDaySweep: s.endOfDaySweep,
    sharedUpdatedAt: s.sharedUpdatedAt,
    sharedUpdatedBy: s.sharedUpdatedBy,
  };
}

function toSettings(d: Draft): Settings {
  return {
    hotkey: d.hotkey,
    leadTimes: d.leads.map(goDuration),
    defaultTime: d.defaultTime,
    nextWeekDay: d.nextWeekDay,
    nextWeekTime: d.nextWeekTime,
    exportPath: d.exportPath.trim(),
    autostart: d.autostart,
    showPendingCount: d.showPendingCount,
    replyHotkey: d.replyHotkey,
    workDays: [...d.workDays],
    endOfDay: d.endOfDay,
    endOfDaySweep: d.endOfDaySweep,
    sharedUpdatedAt: d.sharedUpdatedAt,
    sharedUpdatedBy: d.sharedUpdatedBy,
  };
}

function same(a: Draft, b: Draft): boolean {
  return JSON.stringify({ ...a, exportPath: a.exportPath.trim() }) === JSON.stringify({ ...b, exportPath: b.exportPath.trim() });
}

type FieldKey =
  | "hotkey"
  | "replyHotkey"
  | "workDays"
  | "endOfDay"
  | "leads"
  | "defaultTime"
  | "nextWeek"
  | "exportPath"
  | "autostart"
  | "form";

/** Short labels for the work-day chips, Monday first (WEEKDAYS order). */
const DAY_SHORT = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const CLASH = "Pick a different key from the capture hotkey";

/** Which field a Go error is about, from its "field: message" prefix. */
export function fieldOf(msg: string): { field: FieldKey; text: string } {
  // Go's own wording for a clash with the capture hotkey has no prefix.
  if (msg.trim() === CLASH) return { field: "replyHotkey", text: CLASH };
  const m = /^([\w ]+):\s*(.*)$/s.exec(msg.trim());
  const map: Record<string, FieldKey> = {
    hotkey: "hotkey",
    replyHotkey: "replyHotkey",
    "reply hotkey": "replyHotkey",
    workDays: "workDays",
    endOfDay: "endOfDay",
    leadTimes: "leads",
    defaultTime: "defaultTime",
    nextWeekTime: "nextWeek",
    nextWeekDay: "nextWeek",
    exportPath: "exportPath",
    autostart: "autostart",
  };
  if (m && map[m[1]]) {
    const text = m[2].charAt(0).toUpperCase() + m[2].slice(1);
    return { field: map[m[1]], text };
  }
  return { field: "form", text: msg.trim() };
}

const CLOCK = /^([01]\d|2[0-3]):[0-5]\d$/;

/**
 * Whether p looks like a full path (mirrors filepath.IsAbs in
 * Settings.Validate): "C:\..." or "\\server\share\..." on Windows, "/..."
 * elsewhere.
 */
export function isFullPath(p: string, windows: boolean): boolean {
  return windows ? /^(?:[A-Za-z]:[\\/]|[\\/]{2}[^\\/])/.test(p) : p.startsWith("/");
}

/** The sample path in the "full path" error (same as Go's). */
function examplePath(windows: boolean): string {
  return windows ? "C:\\Users\\you\\todo.md" : "/home/you/todo.md";
}

const TEMPLATE = `
<div class="settings-app">
  <main class="settings-scroll" id="settings-scroll">
    <form class="settings-form" id="settings-form" novalidate>
      <h1 class="sr-only">Settings</h1>

      <div class="field">
        <span class="field-label" id="hotkey-label">Capture hotkey</span>
        <div class="field-row">
          <button class="hotkey-pill" id="hotkey-btn" type="button"
                  aria-labelledby="hotkey-label hotkey-btn" aria-describedby="hotkey-help hotkey-error"></button>
        </div>
        <p class="field-help" id="hotkey-help" aria-live="polite"></p>
        <p class="field-error" id="hotkey-error" role="alert" hidden></p>
      </div>

      <fieldset class="field-group" aria-labelledby="replies-title">
        <legend class="group-title" id="replies-title">Replies</legend>

        <div class="field">
          <span class="field-label" id="reply-hotkey-label">Reply hotkey</span>
          <div class="field-row">
            <button class="hotkey-pill" id="reply-hotkey-btn" type="button"
                    aria-labelledby="reply-hotkey-label reply-hotkey-btn" aria-describedby="reply-hotkey-help reply-hotkey-error"></button>
          </div>
          <p class="field-help" id="reply-hotkey-help" aria-live="polite"></p>
          <p class="field-error" id="reply-hotkey-error" role="alert" hidden></p>
        </div>

        <fieldset class="field">
          <legend class="field-label">Working days</legend>
          <div class="day-chips" id="day-chips"></div>
          <p class="field-help" id="workdays-help">Replies you snooze to tomorrow land on the next of these days.</p>
          <p class="field-error" id="workdays-error" role="alert" hidden></p>
        </fieldset>

        <div class="field">
          <label class="field-label" for="end-of-day">End of day</label>
          <div class="field-row">
            <input class="input input-time" id="end-of-day" type="time" step="60" required
                   aria-describedby="end-of-day-help end-of-day-error" />
          </div>
          <p class="field-help" id="end-of-day-help">When the day's open replies are swept, on a working day.</p>
          <p class="field-error" id="end-of-day-error" role="alert" hidden></p>
        </div>

        <div class="field">
          <div class="field-row field-switch">
            <label class="field-label" for="eod-sweep">End-of-day sweep</label>
            <input class="switch" id="eod-sweep" type="checkbox" role="switch" aria-describedby="eod-sweep-help" />
          </div>
          <p class="field-help" id="eod-sweep-help">At the end of day, asks what to do with the replies still open.</p>
        </div>
      </fieldset>

      <div class="field">
        <span class="field-label" id="leads-label">Alerts before the due time</span>
        <div class="lead-box" id="lead-box">
          <ul class="lead-chips" id="lead-chips" aria-labelledby="leads-label"></ul>
          <input class="lead-add" id="lead-add" type="text" autocomplete="off" spellcheck="false"
                 placeholder="+ add" aria-label="Add an alert time, for example 30m, 2h or 1d"
                 aria-describedby="leads-help leads-error" />
        </div>
        <p class="field-help" id="leads-help">Every reminder also alerts at its due time. Up to 5, for example 30m, 2h, 1d or 1h30m.</p>
        <p class="field-error" id="leads-error" role="alert" hidden></p>
      </div>

      <div class="field">
        <label class="field-label" for="default-time">Default time</label>
        <div class="field-row">
          <input class="input input-time" id="default-time" type="time" step="60" required
                 aria-describedby="default-time-help default-time-error" />
        </div>
        <p class="field-help" id="default-time-help">Used when you name a day without a time, such as "tomorrow".</p>
        <p class="field-error" id="default-time-error" role="alert" hidden></p>
      </div>

      <fieldset class="field">
        <legend class="field-label">"Next week" means</legend>
        <div class="field-row">
          <select class="input input-select" id="nw-day" aria-label="Day" aria-describedby="nw-help nw-error"></select>
          <span class="field-joiner" aria-hidden="true">at</span>
          <input class="input input-time" id="nw-time" type="time" step="60" required aria-label="Time"
                 aria-describedby="nw-help nw-error" />
        </div>
        <p class="field-help" id="nw-help">The day and time "next week" stands for.</p>
        <p class="field-error" id="nw-error" role="alert" hidden></p>
      </fieldset>

      <div class="field">
        <label class="field-label" for="export-path">Markdown export</label>
        <div class="field-row">
          <input class="input input-path" id="export-path" type="text" spellcheck="false" autocomplete="off"
                 placeholder="Off" aria-describedby="export-help export-error" />
          <button class="btn btn-ghost" id="export-choose" type="button">Choose…</button>
        </div>
        <p class="field-help" id="export-help">Keeps a Markdown copy of your list up to date. Leave empty to turn it off.</p>
        <p class="field-error" id="export-error" role="alert" hidden></p>
      </div>

      <div class="field">
        <div class="field-row field-switch">
          <label class="field-label" for="autostart" id="autostart-label">Start at login</label>
          <input class="switch" id="autostart" type="checkbox" role="switch" aria-describedby="autostart-help autostart-error" />
        </div>
        <p class="field-help" id="autostart-help">Opens Awesome Reminder in the tray when you sign in.</p>
        <p class="field-error" id="autostart-error" role="alert" hidden></p>
      </div>

      <div class="field">
        <div class="field-row field-switch">
          <label class="field-label" for="pending-count">Show pending count</label>
          <input class="switch" id="pending-count" type="checkbox" role="switch" aria-describedby="pending-count-help" />
        </div>
        <p class="field-help" id="pending-count-help">Shows how many items are open on the tray icon and the taskbar.</p>
      </div>
    </form>
    <section class="settings-sync" id="sync-section"></section>
    <section class="settings-sync settings-voice" id="voice-section"></section>
  </main>
  <footer class="settings-foot">
    <p class="field-error settings-form-error" id="form-error" role="alert" hidden></p>
    <span class="settings-status" id="settings-status" role="status" aria-live="polite"></span>
    <button class="btn btn-primary" id="settings-save" type="submit" form="settings-form" disabled>Save</button>
  </footer>
</div>`;

export function mountSettings(root: HTMLElement): void {
  document.documentElement.dataset.view = "settings";
  root.innerHTML = TEMPLATE;
  const $ = <T extends HTMLElement>(id: string) => root.querySelector<T>(`#${id}`)!;
  const form = $<HTMLFormElement>("settings-form");
  const hotkeyBtn = $<HTMLButtonElement>("hotkey-btn");
  const hotkeyHelp = $("hotkey-help");
  const replyBtn = $<HTMLButtonElement>("reply-hotkey-btn");
  const replyHelp = $("reply-hotkey-help");
  const dayChips = $("day-chips");
  const endOfDay = $<HTMLInputElement>("end-of-day");
  const eodSweep = $<HTMLInputElement>("eod-sweep");
  const chips = $("lead-chips");
  const leadAdd = $<HTMLInputElement>("lead-add");
  const defaultTime = $<HTMLInputElement>("default-time");
  const nwDay = $<HTMLSelectElement>("nw-day");
  const nwTime = $<HTMLInputElement>("nw-time");
  const exportPath = $<HTMLInputElement>("export-path");
  const exportChoose = $<HTMLButtonElement>("export-choose");
  const autostart = $<HTMLInputElement>("autostart");
  const pendingCount = $<HTMLInputElement>("pending-count");
  const save = $<HTMLButtonElement>("settings-save");
  const status = $("settings-status");
  const errors: Record<FieldKey, HTMLElement | null> = {
    hotkey: $("hotkey-error"),
    replyHotkey: $("reply-hotkey-error"),
    workDays: $("workdays-error"),
    endOfDay: $("end-of-day-error"),
    leads: $("leads-error"),
    defaultTime: $("default-time-error"),
    nextWeek: $("nw-error"),
    exportPath: $("export-error"),
    autostart: $("autostart-error"),
    form: $("form-error"),
  };
  const inputs: Partial<Record<FieldKey, HTMLElement>> = {
    hotkey: hotkeyBtn,
    replyHotkey: replyBtn,
    workDays: dayChips.querySelector<HTMLElement>("button") ?? undefined,
    endOfDay,
    leads: leadAdd,
    defaultTime,
    nextWeek: nwTime,
    exportPath,
    autostart,
  };

  const mac = isMac();
  const windows = isWindows();
  $("pending-count-help").textContent = `Shows how many items are open on the tray icon${
    mac ? " and the Dock" : windows ? " and the taskbar" : ""
  }.`;
  for (const d of WEEKDAYS) {
    const o = el("option", undefined, d.charAt(0).toUpperCase() + d.slice(1));
    o.value = d;
    nwDay.append(o);
  }

  let saved: Draft | null = null; // what Go has; null until loaded
  let draft: Draft | null = null;
  let hotkeyErr = ""; // Status().hotkeyErr for the saved combo
  let replyHotkeyErr = ""; // Status().replyHotkeyErr for the saved reply combo
  let saving = false;

  const helpIdle = "Click, then press the new key combination.";
  const replyHelpIdle = "Marks the selected message as a reply you owe. " + helpIdle;

  function setError(field: FieldKey, text: string): void {
    const box = errors[field];
    if (!box) return;
    box.textContent = text;
    box.hidden = text === "";
    const input = inputs[field];
    if (input) {
      if (text) input.setAttribute("aria-invalid", "true");
      else input.removeAttribute("aria-invalid");
    }
  }

  function clearErrors(): void {
    (Object.keys(errors) as FieldKey[]).forEach((k) => setError(k, ""));
  }

  /** Redraws both hotkey pills (and their registration errors). */
  function renderHotkey(): void {
    hotkeys.forEach((r) => r.render());
  }

  function renderDays(): void {
    if (!draft) return;
    dayChips.replaceChildren(
      ...WEEKDAYS.map((day, i) => {
        const on = draft!.workDays.includes(day);
        const b = el("button", "day-chip", DAY_SHORT[i]);
        b.type = "button";
        b.setAttribute("aria-pressed", String(on));
        b.setAttribute("aria-label", day.charAt(0).toUpperCase() + day.slice(1));
        b.addEventListener("click", () => {
          if (!draft) return;
          const set = new Set(draft.workDays);
          if (set.has(day)) set.delete(day);
          else set.add(day);
          draft.workDays = WEEKDAYS.filter((d) => set.has(d));
          setError("workDays", "");
          renderDays();
          changed();
          (dayChips.children[i] as HTMLElement).focus();
        });
        return b;
      }),
    );
    inputs.workDays = dayChips.querySelector("button") as HTMLElement;
    // Mirrors Validate: at least one day stays selected.
    if (draft.workDays.length === 0) setError("workDays", "Pick at least one working day");
  }

  function renderLeads(): void {
    if (!draft) return;
    chips.replaceChildren(
      ...draft.leads.map((sec) => {
        const li = el("li", "lead-chip");
        li.append(el("span", undefined, leadLabel(sec)));
        const x = el("button", "lead-remove");
        x.type = "button";
        x.setAttribute("aria-label", `Remove ${leadSpoken(sec)}`);
        x.innerHTML = icons.close;
        x.addEventListener("click", () => {
          draft!.leads = draft!.leads.filter((n) => n !== sec);
          setError("leads", "");
          renderLeads();
          changed();
          leadAdd.focus();
        });
        li.append(x);
        return li;
      }),
    );
    const full = draft.leads.length >= MAX_LEADS;
    leadAdd.disabled = full;
    leadAdd.placeholder = full ? "5 of 5" : "+ add";
  }

  function fill(): void {
    if (!draft) return;
    renderHotkey();
    renderDays();
    endOfDay.value = draft.endOfDay;
    eodSweep.checked = draft.endOfDaySweep;
    renderLeads();
    defaultTime.value = draft.defaultTime;
    nwDay.value = draft.nextWeekDay;
    nwTime.value = draft.nextWeekTime;
    exportPath.value = draft.exportPath;
    autostart.checked = draft.autostart;
    pendingCount.checked = draft.showPendingCount;
  }

  function dirty(): boolean {
    return !!draft && !!saved && !same(draft, saved);
  }

  function changed(): void {
    save.disabled = saving || !dirty() || (!!draft && draft.workDays.length === 0);
    if (dirty()) status.textContent = "";
  }

  /** Adds the lead time typed in the "+ add" box; returns false on an error. */
  function addLead(): boolean {
    if (!draft) return false;
    const raw = leadAdd.value.trim();
    if (!raw) return true;
    const sec = parseLead(raw);
    let err = "";
    if (Number.isNaN(sec)) err = "Use a number with m, h or d, for example 30m, 2h or 1d";
    else if (sec <= 0) err = "An alert time must be more than 0";
    else if (sec > MAX_LEAD) err = "An alert time can be at most 7 days";
    else if (draft.leads.includes(sec)) err = `${leadLabel(sec)} is already in the list`;
    else if (draft.leads.length >= MAX_LEADS) err = `At most ${MAX_LEADS} alert times`;
    setError("leads", err);
    if (err) return false;
    draft.leads = [...draft.leads, sec].sort((a, b) => b - a);
    leadAdd.value = "";
    renderLeads();
    changed();
    leadAdd.focus();
    return true;
  }

  /** Client-side checks mirroring Settings.Validate; returns the first bad field. */
  function validate(): FieldKey | null {
    if (!draft) return null;
    let first: FieldKey | null = null;
    const fail = (f: FieldKey, msg: string) => {
      setError(f, msg);
      first ??= f;
    };
    if (draft.workDays.length === 0) fail("workDays", "Pick at least one working day");
    if (!CLOCK.test(draft.endOfDay)) fail("endOfDay", "Enter a time, for example 17:30");
    if (!CLOCK.test(draft.defaultTime)) fail("defaultTime", "Enter a time, for example 09:00");
    if (!CLOCK.test(draft.nextWeekTime)) fail("nextWeek", "Enter a time, for example 09:00");
    const path = draft.exportPath.trim();
    if (path && !isFullPath(path, windows)) {
      fail("exportPath", `Export path must be a full path, e.g. ${examplePath(windows)}`);
    }
    if (leadAdd.value.trim() && !addLead()) first ??= "leads";
    return first;
  }

  // ----- Hotkey recorder -----

  interface Recorder {
    render(): void;
    isRecording(): boolean;
    stop(): void;
  }

  /**
   * One hotkey field: a pill that records a combo. While it records, Go
   * releases both global hotkeys, so pressing a current combo reaches us.
   */
  function recorder(o: {
    btn: HTMLButtonElement;
    help: HTMLElement;
    idle: string;
    field: "hotkey" | "replyHotkey";
    get: (d: Draft) => string;
    set: (d: Draft, v: string) => void;
    registrationErr: () => string;
  }): Recorder {
    let recording = false;
    const render = () => {
      if (!draft) return;
      o.btn.replaceChildren();
      o.btn.classList.toggle("is-recording", recording);
      o.btn.setAttribute("aria-pressed", String(recording));
      o.btn.append(recording ? el("span", "hotkey-prompt", "Press keys…") : keycaps(o.get(draft)));
      // The registration error is about the saved combo; hide it while a new
      // one is drafted.
      setError(o.field, !recording && saved && o.get(draft) === o.get(saved) ? o.registrationErr() : "");
    };
    const start = () => {
      if (recording || !draft) return;
      recording = true;
      o.help.textContent = "Press the new combination. Esc cancels.";
      render();
      void api.setHotkeyRecording(true).catch(() => undefined);
    };
    const stop = (combo?: string) => {
      if (!recording || !draft) return;
      recording = false;
      void api.setHotkeyRecording(false).catch(() => undefined);
      if (combo) o.set(draft, combo);
      o.help.textContent = o.idle;
      render();
      changed();
    };
    o.btn.addEventListener("click", () => (recording ? stop() : start()));
    o.btn.addEventListener("keydown", (e) => {
      if (!recording) return;
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        stop();
        return;
      }
      // Tab and Shift+Tab end recording and move focus as usual, so keyboard
      // users can leave the field in either direction.
      if (e.key === "Tab" && !e.ctrlKey && !e.altKey && !e.metaKey) {
        stop(); // let focus move on
        return;
      }
      e.preventDefault();
      e.stopPropagation();
      if (e.repeat) return;
      const r = comboFor(e, mac);
      if (r.combo) stop(r.combo);
      else o.help.textContent = r.hint ?? "Press the new combination. Esc cancels.";
    });
    // Keep Alt/F10 from activating the window menu while recording.
    o.btn.addEventListener("keyup", (e) => {
      if (recording) e.preventDefault();
    });
    o.btn.addEventListener("blur", () => stop());
    window.addEventListener("blur", () => stop());
    return { render, isRecording: () => recording, stop: () => stop() };
  }

  const hotkeys: Recorder[] = [
    recorder({
      btn: hotkeyBtn,
      help: hotkeyHelp,
      idle: helpIdle,
      field: "hotkey",
      get: (d) => d.hotkey,
      set: (d, v) => (d.hotkey = v),
      registrationErr: () => hotkeyErr,
    }),
    recorder({
      btn: replyBtn,
      help: replyHelp,
      idle: replyHelpIdle,
      field: "replyHotkey",
      get: (d) => d.replyHotkey,
      set: (d, v) => (d.replyHotkey = v),
      registrationErr: () => replyHotkeyErr,
    }),
  ];

  // ----- Fields -----

  leadAdd.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      addLead();
    } else if (e.key === "Backspace" && leadAdd.value === "" && draft && draft.leads.length) {
      draft.leads = draft.leads.slice(0, -1);
      renderLeads();
      changed();
    }
  });
  leadAdd.addEventListener("input", () => setError("leads", ""));
  leadAdd.addEventListener("blur", () => {
    if (leadAdd.value.trim()) addLead();
  });
  $("lead-box").addEventListener("click", (e) => {
    if (e.target === e.currentTarget) leadAdd.focus();
  });

  const bind = (input: HTMLInputElement | HTMLSelectElement, field: FieldKey, apply: (d: Draft) => void) => {
    input.addEventListener("input", () => {
      if (!draft) return;
      apply(draft);
      setError(field, "");
      changed();
    });
    input.addEventListener("change", () => {
      if (!draft) return;
      apply(draft);
      changed();
    });
  };
  bind(defaultTime, "defaultTime", (d) => (d.defaultTime = defaultTime.value));
  bind(nwDay, "nextWeek", (d) => (d.nextWeekDay = nwDay.value));
  bind(nwTime, "nextWeek", (d) => (d.nextWeekTime = nwTime.value));
  bind(exportPath, "exportPath", (d) => (d.exportPath = exportPath.value));
  bind(endOfDay, "endOfDay", (d) => (d.endOfDay = endOfDay.value));
  bind(eodSweep, "form", (d) => (d.endOfDaySweep = eodSweep.checked));
  bind(autostart, "autostart", (d) => (d.autostart = autostart.checked));
  bind(pendingCount, "form", (d) => (d.showPendingCount = pendingCount.checked));

  exportChoose.addEventListener("click", async () => {
    if (!draft) return;
    try {
      let p = await api.chooseExportPath(draft.exportPath);
      if (!p) return; // cancelled
      if (!/\.md$/i.test(p)) p += ".md";
      draft.exportPath = p;
      exportPath.value = p;
      setError("exportPath", "");
      changed();
    } catch (err) {
      setError("exportPath", errorText(err));
    } finally {
      exportChoose.focus();
    }
  });

  // ----- Save -----

  async function refreshStatus(): Promise<void> {
    try {
      const st = await api.status();
      hotkeyErr = st.hotkeyErr;
      replyHotkeyErr = st.replyHotkeyErr ?? "";
    } catch {
      hotkeyErr = "";
      replyHotkeyErr = "";
    }
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!draft || !dirty() || saving) return;
    clearErrors();
    const bad = validate();
    if (bad) {
      inputs[bad]?.focus();
      changed();
      return;
    }
    if (!dirty()) return;
    saving = true;
    save.disabled = true;
    status.textContent = "Saving…";
    let failed: { field: FieldKey; text: string }[] = [];
    let applied = true;
    try {
      await api.saveSettings(toSettings(draft));
    } catch (err) {
      const lines = errorText(err).split("\n").filter((l) => l.trim());
      failed = lines.map(fieldOf);
      // Registration errors come after the save (Service.SaveSettings:
      // "hotkey:", "reply hotkey:", autostart); Validate's own errors
      // ("replyHotkey:", the clash, ...) mean nothing was saved.
      applied = lines.every((l, i) => /^(hotkey|reply hotkey):/.test(l.trim()) || failed[i].field === "autostart");
    }
    saving = false;
    if (applied) {
      saved = { ...draft, leads: [...draft.leads] };
      await refreshStatus();
    }
    // A hotkey that couldn't be registered shows under the field from
    // Status (renderHotkey), the same text as the save error.
    renderHotkey();
    for (const f of failed) if ((f.field !== "hotkey" && f.field !== "replyHotkey") || !applied) setError(f.field, f.text);
    if (failed.length) {
      status.textContent = applied ? "Saved, with a problem" : "";
      const first = failed[0].field;
      (inputs[first] ?? save).focus();
    } else {
      status.textContent = "Saved";
    }
    changed();
  });

  document.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      form.requestSubmit();
      return;
    }
    if (e.key === "Escape" && !hotkeys.some((r) => r.isRecording()) && !e.defaultPrevented) {
      if (dirty()) {
        status.textContent = "Unsaved changes: Save, or close the window to discard them";
        return;
      }
      void api.closeWindow();
    }
  });

  mountSyncSection($("sync-section"));
  mountVoiceSection($("voice-section"));

  hotkeyHelp.textContent = helpIdle;
  replyHelp.textContent = replyHelpIdle;
  void (async () => {
    try {
      const s = await api.getSettings();
      saved = toDraft(s);
      draft = toDraft(s);
    } catch (err) {
      setError("form", `Could not load settings: ${errorText(err)}`);
      return;
    }
    await refreshStatus();
    fill();
    changed();
  })();
}
