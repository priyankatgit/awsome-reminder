// Settings › "Voice": the speech model, "Words I use" and the "Hey Awesome"
// wake word. Like the sync section it sits outside the form's Save/dirty
// flow: its controls act at once. The pure helpers and the install flow are
// tested; mountVoiceSection draws them.
import { api, errorText, events, on, type VoiceModel, type VoiceStatus, type WakeStatus } from "./api";
import { el } from "./dom";
import { icons } from "./icons";

/** Working set of the real Windows helper started by the app: 69.3 MB (spec "Measurements"). */
const WAKE_MEMORY = "about 70 MB";
const NOT_INSTALLED = "wake word files are not installed";

export const WAKE_HELP =
  'Opens the capture box when you say "Hey Awesome". While this is on, the microphone stays on, so your system shows ' +
  "that Awesome Reminder is using it. Audio is processed on this computer and never stored or sent anywhere. " +
  `Uses ${WAKE_MEMORY} of memory and a little CPU.`;
const CHIME_HELP = "Plays a short sound when the capture box opens because you said it.";

/** "62 MB", "5.3 MB": decimal megabytes, as the capture box's download panel writes them. */
export function megabytes(bytes: number): string {
  const mb = bytes / 1e6;
  return mb >= 10 ? `${Math.round(mb)} MB` : `${mb.toFixed(1)} MB`;
}

/** Whether a SetWakeEnabled(true) rejection means "the files are absent" (Go voice.ErrWakeNotInstalled). */
export function isNotInstalled(err: unknown): boolean {
  return errorText(err).toLowerCase().includes(NOT_INSTALLED);
}

/** Total bytes the consent box announces: wake files, plus the dictation files when those are absent too. */
export function consentBytes(wake: WakeStatus | null, voice: VoiceStatus | null): number {
  return (wake?.download ?? 0) + (voice?.state === "absent" ? voice.download : 0);
}

export function consentText(wake: WakeStatus | null, voice: VoiceStatus | null): string {
  const total = megabytes(consentBytes(wake, voice));
  const both = voice?.state === "absent";
  return (
    `"Hey Awesome" needs a one-time download of ${total}` +
    (both ? " (the wake word files and the speech files for dictation)." : " (the wake word files).") +
    " Nothing you say leaves this computer."
  );
}

export interface StatusLine {
  text: string;
  /** Show the error styling and the "Try again" button. */
  error: boolean;
}

/** The line under the switches for a wake status; null when there is nothing to say. */
export function statusLine(w: WakeStatus | null): StatusLine | null {
  if (w && w.state === "absent" && w.error) return { text: w.error, error: true };
  if (!w || !w.enabled) return null;
  if (w.state === "listening") return { text: 'Listening for "Hey Awesome"', error: false };
  if (w.state === "starting") return { text: "Starting…", error: false };
  if (w.state === "error") {
    return { text: [w.error || "The wake word stopped", w.hint].filter(Boolean).join(" "), error: true };
  }
  return null;
}

// ---------- Install flow ----------

export interface Watch {
  /** Resolves when dictation is ready; rejects when the install failed or was cancelled. */
  done: Promise<void>;
  /** Looks at the current status once (an event may have fired before the watch began). */
  recheck(): Promise<void>;
  stop(): void;
}

/** Watches voice:status for an install that this flow starts. */
export function watchDictation(
  subscribe: (cb: (s: VoiceStatus) => void) => () => void,
  current: () => Promise<VoiceStatus>,
): Watch {
  let res!: () => void;
  let rej!: (e: Error) => void;
  const done = new Promise<void>((a, b) => {
    res = a;
    rej = b;
  });
  done.catch(() => undefined); // surfaced through the awaiting caller
  let sawInstall = false;
  const see = (s: VoiceStatus) => {
    if (s.state === "ready") res();
    else if (s.state === "installing") sawInstall = true;
    else if (s.state === "unsupported") rej(new Error(s.hint || "Dictation is not available on this computer"));
    else if (s.state === "absent" && (sawInstall || s.error)) rej(new Error(s.error || "The download was cancelled"));
  };
  const off = subscribe(see);
  return {
    done,
    recheck: async () => see(await current()),
    stop: off,
  };
}

/**
 * Watches voice:wake-status for the wake download that Install starts (Go's
 * Install returns as soon as its goroutine starts). Resolves when it left
 * "installing" with the files in place; rejects on "absent" after "installing"
 * (cancelled or failed; the status error is the message).
 */
export function watchWake(
  subscribe: (cb: (s: WakeStatus) => void) => () => void,
  current: () => Promise<WakeStatus>,
): Watch {
  let res!: () => void;
  let rej!: (e: Error) => void;
  const done = new Promise<void>((a, b) => {
    res = a;
    rej = b;
  });
  done.catch(() => undefined);
  let sawInstall = false;
  const see = (s: WakeStatus) => {
    if (s.state === "installing") sawInstall = true;
    else if (s.state === "unsupported") rej(new Error(s.hint || "The wake word is not available on this computer"));
    else if (s.state === "absent") {
      if (sawInstall) rej(new Error(s.error || "The download was cancelled"));
    } else if (sawInstall || s.enabled) res(); // off, starting, listening, error: installed
  };
  const off = subscribe(see);
  return {
    done,
    recheck: async () => see(await current()),
    stop: off,
  };
}

export interface InstallDeps {
  /** Dictation files are absent too: download them first. */
  dictationAbsent: boolean;
  installDictation(): Promise<void>;
  watchDictation(): Watch;
  installWake(): Promise<void>;
  watchWake(): Watch;
  onPhase(phase: "dictation" | "wake"): void;
  cancelled(): boolean;
}

/** Dictation first when absent (waiting for it to be ready), then the wake word. Rejects on a failure. */
export async function runInstall(d: InstallDeps): Promise<void> {
  if (d.dictationAbsent) {
    d.onPhase("dictation");
    const w = d.watchDictation();
    try {
      await d.installDictation();
      await w.recheck();
      await w.done;
    } finally {
      w.stop();
    }
  }
  if (d.cancelled()) return;
  d.onPhase("wake");
  const w = d.watchWake();
  try {
    await d.installWake();
    await w.recheck();
    await w.done;
  } finally {
    w.stop();
  }
}

// ---------- Speech model and "Words I use" ----------

/** The select option for a model: "Large v3 Turbo (574 MB) · Recommended". */
export function modelOptionLabel(m: VoiceModel): string {
  return `${m.label} (${megabytes(m.size)})${m.recommended ? " · Recommended" : ""}`;
}

/** The help line under the select for the chosen model. */
export function modelHelp(models: VoiceModel[], id: string, voice: VoiceStatus | null): string {
  const m = models.find((x) => x.id === id);
  if (!m) return "";
  if (m.installed || voice?.state !== "ready" || voice.target === id) return m.hint;
  return `${m.hint} Choosing it downloads ${megabytes(m.size)}; dictation keeps working meanwhile.`;
}

/** Nudges toward the recommended model while another one is active; "" otherwise. */
export function recommendTip(models: VoiceModel[], voice: VoiceStatus | null): string {
  const rec = models.find((m) => m.recommended);
  const active = voice?.model;
  if (!rec || !active || active === rec.id || voice?.target === rec.id || voice?.state === "unsupported") return "";
  return `Words often missed, especially with an Indian or other accent? ${rec.label} is much more accurate (a one-time ${megabytes(rec.size)} download).`;
}

/** Progress of a background model switch; null when none is running. */
export function switchLine(voice: VoiceStatus | null, models: VoiceModel[]): string | null {
  if (!voice?.target) return null;
  const to = models.find((m) => m.id === voice.target)?.label ?? voice.target;
  const from = models.find((m) => m.id === voice.model)?.label ?? voice.model;
  return `Downloading ${to}… ${megabytes(voice.done)} of ${megabytes(voice.total)}. Dictation uses ${from} until it finishes.`;
}

export const WORDS_HELP =
  "Names, places and terms you say often, separated by commas: people, your company, product names. " +
  "They help the speech engine spell them right.";

const SPEECH_TEMPLATE = `
<div class="field">
  <label class="field-label" for="speech-model">Speech model</label>
  <div class="field-row">
    <select class="input input-select" id="speech-model" aria-describedby="speech-model-help speech-model-tip speech-error"></select>
  </div>
  <p class="field-help" id="speech-model-help"></p>
  <p class="field-help" id="speech-model-tip" hidden></p>
  <div class="cap-progress" id="speech-progress" role="progressbar" aria-label="Speech model download"
       aria-valuemin="0" aria-valuemax="100" aria-valuenow="0" hidden><span class="cap-progress-bar" id="speech-progress-bar"></span></div>
  <div class="voice-status" id="speech-status" role="status" aria-live="polite" hidden>
    <p class="voice-status-text" id="speech-status-text"></p>
    <button class="btn btn-ghost" id="speech-cancel" type="button">Cancel</button>
  </div>
  <p class="field-error" id="speech-error" role="alert" hidden></p>
</div>

<div class="field">
  <label class="field-label" for="speech-words">Words I use</label>
  <div class="field-row">
    <input class="input input-path" id="speech-words" type="text" spellcheck="false" autocomplete="off" maxlength="300"
           placeholder="For example: Venkatesh, Pune, Jira" aria-describedby="speech-words-help speech-words-error" />
  </div>
  <p class="field-help" id="speech-words-help"></p>
  <p class="field-error" id="speech-words-error" role="alert" hidden></p>
</div>`;

/** Wires the speech model select and the "Words I use" field inside host. */
function mountSpeech(host: HTMLElement): void {
  const $ = <T extends HTMLElement>(id: string) => host.querySelector<T>(`#${id}`)!;
  const select = $<HTMLSelectElement>("speech-model");
  const help = $("speech-model-help");
  const tip = $("speech-model-tip");
  const progress = $("speech-progress");
  const bar = $("speech-progress-bar");
  const statusBox = $("speech-status");
  const statusText = $("speech-status-text");
  const cancel = $<HTMLButtonElement>("speech-cancel");
  const err = $("speech-error");
  const words = $<HTMLInputElement>("speech-words");
  const wordsErr = $("speech-words-error");
  $("speech-words-help").textContent = WORDS_HELP;

  let models: VoiceModel[] = [];
  let voice: VoiceStatus | null = null;
  let busy = false;

  const show = (box: HTMLElement, text: string) => {
    box.textContent = text;
    box.hidden = text === "";
  };

  function render(): void {
    const chosen = voice?.target || voice?.model || "";
    if (select.options.length !== models.length) {
      select.replaceChildren(
        ...models.map((m) => {
          const o = el("option");
          o.value = m.id;
          return o;
        }),
      );
    }
    models.forEach((m, i) => (select.options[i].textContent = modelOptionLabel(m)));
    if (chosen) select.value = chosen;
    select.disabled = busy || !voice || voice.state === "unsupported" || voice.state === "installing" || !!voice.target;
    help.textContent = modelHelp(models, chosen, voice);
    show(tip, recommendTip(models, voice));
    const line = switchLine(voice, models);
    statusBox.hidden = !line;
    statusText.textContent = line ?? "";
    progress.hidden = !line;
    if (line && voice) {
      const pct = voice.total > 0 ? Math.min(100, Math.round((voice.done / voice.total) * 100)) : 0;
      progress.setAttribute("aria-valuenow", String(pct));
      progress.setAttribute("aria-valuetext", `${pct}%`);
      bar.style.transform = `scaleX(${pct / 100})`;
    }
    if (!line && voice?.switchError && err.hidden) show(err, voice.switchError);
  }

  async function refresh(): Promise<void> {
    try {
      [models, voice] = await Promise.all([api.voice.models(), api.voice.status()]);
    } catch (e) {
      show(err, errorText(e));
    }
    render();
  }

  select.addEventListener("change", () => {
    const id = select.value;
    void (async () => {
      busy = true;
      show(err, "");
      render();
      try {
        await api.voice.setModel(id);
      } catch (e) {
        show(err, errorText(e));
      } finally {
        busy = false;
      }
      await refresh();
    })();
  });
  cancel.addEventListener("click", () => {
    void api.voice.cancelModelSwitch().catch((e: unknown) => show(err, errorText(e)));
  });
  words.addEventListener("change", () => {
    const text = words.value;
    void (async () => {
      try {
        await api.voice.setWords(text);
        show(wordsErr, "");
        words.value = await api.voice.words();
      } catch (e) {
        show(wordsErr, errorText(e));
      }
    })();
  });

  on<VoiceStatus>(events.voiceStatus, (s) => {
    const switched = !!voice?.target && !s.target;
    voice = s;
    if (switched) void refresh(); // the installed/active flags changed
    else render();
  });
  void (async () => {
    try {
      words.value = await api.voice.words();
    } catch (e) {
      show(wordsErr, errorText(e));
    }
  })();
  void refresh();
}

// ---------- Section ----------

const TEMPLATE = `
<h2 class="sync-title" id="voice-title">Voice</h2>
<p class="field-help">Voice settings apply right away.</p>
${SPEECH_TEMPLATE}

<div class="field">
  <div class="field-row field-switch">
    <label class="field-label" for="wake-on">Listen for "Hey Awesome"</label>
    <input class="switch" id="wake-on" type="checkbox" role="switch" aria-describedby="wake-help wake-hint" />
  </div>
  <p class="field-help" id="wake-help"></p>
  <p class="field-help" id="wake-hint" hidden></p>
</div>

<div class="voice-consent" id="wake-consent" role="group" aria-labelledby="wake-consent-title" hidden>
  <h3 class="cap-voice-title" id="wake-consent-title">Download the wake word</h3>
  <p class="cap-voice-body" id="wake-consent-body"></p>
  <div class="cap-progress" id="wake-progress" role="progressbar" aria-label="Wake word download"
       aria-valuemin="0" aria-valuemax="100" aria-valuenow="0" hidden><span class="cap-progress-bar" id="wake-progress-bar"></span></div>
  <p class="cap-voice-meta" id="wake-meta"></p>
  <div class="cap-voice-actions" id="wake-actions"></div>
</div>

<div class="field">
  <div class="field-row field-switch">
    <label class="field-label" for="wake-chime">Play a sound when it hears you</label>
    <input class="switch" id="wake-chime" type="checkbox" role="switch" aria-describedby="wake-chime-help" />
  </div>
  <p class="field-help" id="wake-chime-help"></p>
</div>

<div class="voice-status" id="wake-status" role="status" aria-live="polite" hidden>
  <p class="voice-status-text" id="wake-status-text"></p>
  <button class="btn btn-ghost" id="wake-retry" type="button" hidden>Try again</button>
</div>
<p class="field-error" id="wake-error" role="alert" hidden></p>`;

/** Mounts the section into host (an empty element after the settings form and the sync section). */
export function mountVoiceSection(host: HTMLElement): void {
  host.setAttribute("aria-labelledby", "voice-title");
  host.innerHTML = TEMPLATE;
  const $ = <T extends HTMLElement>(id: string) => host.querySelector<T>(`#${id}`)!;
  const wakeOn = $<HTMLInputElement>("wake-on");
  const hint = $("wake-hint");
  const consent = $("wake-consent");
  const consentBody = $("wake-consent-body");
  const progress = $("wake-progress");
  const bar = $("wake-progress-bar");
  const meta = $("wake-meta");
  const actions = $("wake-actions");
  const chime = $<HTMLInputElement>("wake-chime");
  const statusBox = $("wake-status");
  const statusText = $("wake-status-text");
  const retry = $<HTMLButtonElement>("wake-retry");
  const errBox = $("wake-error");
  $("wake-help").textContent = WAKE_HELP;
  $("wake-chime-help").textContent = CHIME_HELP;
  mountSpeech(host);

  let wake: WakeStatus | null = null;
  let voice: VoiceStatus | null = null;
  let asking = false; // the consent box is open
  let phase: "dictation" | "wake" | null = null; // a download this section started is running
  let cancelled = false;
  let busy = false; // a switch call is in flight
  let error = "";

  function setError(text: string): void {
    error = text;
    errBox.textContent = text;
    errBox.hidden = text === "";
  }

  function button(label: string, cls: string, onClick: () => void, icon?: string): HTMLButtonElement {
    const b = el("button", `btn ${cls}`);
    b.type = "button";
    if (icon) b.innerHTML = icon;
    b.append(label);
    b.addEventListener("click", onClick);
    return b;
  }

  function renderConsent(): void {
    const showing = asking || phase !== null;
    consent.hidden = !showing;
    if (!showing) return;
    const had = actions.contains(document.activeElement);
    actions.replaceChildren();
    meta.classList.remove("is-error");
    if (phase === null) {
      progress.hidden = true;
      meta.textContent = "";
      consentBody.textContent = consentText(wake, voice);
      const dl = button(`Download ${megabytes(consentBytes(wake, voice))}`, "btn-primary", () => void download(), icons.download);
      actions.append(button("Not now", "btn-ghost", dismiss), dl);
      if (!had) dl.focus();
      return;
    }
    const src = phase === "dictation" ? voice : wake;
    const pct = src && src.total > 0 ? Math.min(100, Math.round((src.done / src.total) * 100)) : 0;
    const what =
      phase === "dictation"
        ? src?.phase === "engine"
          ? "Downloading speech engine"
          : "Downloading English model"
        : src?.phase === "engine"
          ? "Downloading wake word engine"
          : "Downloading wake word model";
    consentBody.textContent =
      phase === "dictation" ? "Step 1 of 2: the speech files for dictation." : "The wake word files.";
    progress.hidden = false;
    progress.setAttribute("aria-valuenow", String(pct));
    progress.setAttribute("aria-valuetext", `${pct}%`);
    bar.style.transform = `scaleX(${pct / 100})`;
    meta.textContent = `${what}… ${megabytes(src?.done ?? 0)} of ${megabytes(src?.total ?? 0)}`;
    const cancel = button("Cancel", "btn-ghost", () => void cancelDownload());
    actions.append(cancel);
    if (!had) cancel.focus();
  }

  function render(): void {
    const unsupported = voice?.state === "unsupported" || wake?.state === "unsupported";
    const hintText = unsupported ? ((voice?.state === "unsupported" ? voice.hint : "") || wake?.hint || "") : "";
    hint.textContent = hintText;
    hint.hidden = hintText === "";
    wakeOn.disabled = unsupported || busy || phase !== null;
    wakeOn.checked = !unsupported && !!wake?.enabled && phase === null;
    chime.checked = wake?.chime ?? true;
    chime.disabled = !wakeOn.checked;
    const line = phase === null ? statusLine(wake) : null;
    statusBox.hidden = !line;
    statusText.textContent = line?.text ?? "";
    statusText.classList.toggle("is-error", !!line?.error);
    retry.hidden = !line?.error;
    retry.disabled = busy || phase !== null;
    renderConsent();
  }

  async function refresh(): Promise<void> {
    try {
      [voice, wake] = await Promise.all([api.voice.status(), api.voice.wakeStatus()]);
    } catch (err) {
      setError(errorText(err));
    }
    render();
  }

  async function turnOn(): Promise<void> {
    busy = true;
    setError("");
    render();
    try {
      await api.voice.setWakeEnabled(true);
    } catch (err) {
      if (isNotInstalled(err)) {
        try {
          voice = await api.voice.status();
          wake = await api.voice.wakeStatus();
        } catch {
          // The consent text falls back to what was loaded before.
        }
        asking = true;
      } else {
        setError(errorText(err));
      }
    } finally {
      busy = false;
    }
    await refresh();
  }

  async function turnOff(): Promise<void> {
    busy = true;
    setError("");
    render();
    try {
      await api.voice.setWakeEnabled(false);
    } catch (err) {
      setError(errorText(err));
    } finally {
      busy = false;
    }
    await refresh();
  }

  function dismiss(): void {
    asking = false;
    render();
    wakeOn.focus();
  }

  async function download(): Promise<void> {
    asking = false;
    cancelled = false;
    setError("");
    try {
      await runInstall({
        dictationAbsent: voice?.state === "absent",
        installDictation: () => api.voice.install(),
        watchDictation: () =>
          watchDictation(
            (cb) => on<VoiceStatus>(events.voiceStatus, cb),
            () => api.voice.status(),
          ),
        installWake: () => api.voice.installWake(),
        watchWake: () =>
          watchWake(
            (cb) => on<WakeStatus>(events.wakeStatus, cb),
            () => api.voice.wakeStatus(),
          ),
        onPhase: (p) => {
          phase = p;
          render();
        },
        cancelled: () => cancelled,
      });
    } catch (err) {
      if (!cancelled) setError(errorText(err));
    }
    phase = null;
    await refresh();
    // Go switches it on when the download finishes; if the files are in but it stayed off, switch it on.
    if (!cancelled && !error && wake?.state === "off" && !wake.enabled) {
      await turnOn();
    }
    if (statusLine(wake)?.error) setError(""); // the status line already shows it
    wakeOn.focus();
  }

  async function cancelDownload(): Promise<void> {
    cancelled = true;
    try {
      await (phase === "dictation" ? api.voice.cancelInstall() : api.voice.cancelWakeInstall());
    } catch (err) {
      setError(errorText(err));
    }
  }

  wakeOn.addEventListener("change", () => {
    if (wakeOn.checked) void turnOn();
    else void turnOff();
  });
  chime.addEventListener("change", () => {
    const on = chime.checked;
    void (async () => {
      try {
        await api.voice.setWakeChime(on);
        setError("");
      } catch (err) {
        setError(errorText(err));
      }
      await refresh();
    })();
  });
  retry.addEventListener("click", () => {
    if (busy) return;
    void (async () => {
      if (wake?.state !== "absent") await turnOff(); // a failed download goes straight to consent
      await turnOn();
    })();
  });

  on<WakeStatus>(events.wakeStatus, (s) => {
    wake = s;
    render();
  });
  on<VoiceStatus>(events.voiceStatus, (s) => {
    voice = s;
    render();
  });
  render();
  void refresh();
}
