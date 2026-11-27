// Voice dictation for the capture box (Phase 1.5). The page records the mic,
// turns it into 16 kHz mono PCM and hands it to Go, which runs whisper.cpp
// locally. Layout follows "Listening state" in the UI design guide.
import { api, errorText, events, on, type Cancellable, type VoiceStatus } from "./api";
import { el } from "./dom";
import { icons } from "./icons";

const TARGET_RATE = 16000;
const LEVEL_WINDOW_MS = 33; // one RMS level (and one VAD step) per ~frame
const SILENCE_STOP_MS = 1500; // auto-stop this long after the last speech
const NO_SPEECH_STOP_MS = 8000; // give up when nothing was said
const MAX_RECORD_MS = 30000; // whisper's window; stop there
const SPEECH_MIN_RMS = 0.015;
const SPEECH_OVER_FLOOR = 3;
const INITIAL_FLOOR_RMS = 0.003;
const MIN_SAMPLES = TARGET_RATE / 4; // matches voice.MinSamples in Go
const READY_NOTE_MS = 2500;

export type StopReason = "user" | "silence" | "limit" | "nothing";

// The worklet copies each 128-frame input block to the page.
const TAP_WORKLET = `
class ArTap extends AudioWorkletProcessor {
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (ch) this.port.postMessage(ch.slice(0));
    return true;
  }
}
registerProcessor("ar-tap", ArTap);
`;

/** Box-filtered linear resampling of mono audio to 16 kHz. */
export function resampleTo16k(input: Float32Array, rate: number): Float32Array {
  if (rate === TARGET_RATE) return input;
  const ratio = rate / TARGET_RATE;
  const out = new Float32Array(Math.floor(input.length / ratio));
  for (let i = 0; i < out.length; i++) {
    const from = Math.floor(i * ratio);
    const to = Math.min(input.length, Math.max(from + 1, Math.floor((i + 1) * ratio)));
    let sum = 0;
    for (let j = from; j < to; j++) sum += input[j];
    out[i] = sum / (to - from);
  }
  return out;
}

function toInt16(f: Float32Array): Int16Array {
  const out = new Int16Array(f.length);
  for (let i = 0; i < f.length; i++) {
    const s = Math.max(-1, Math.min(1, f[i]));
    out[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
  }
  return out;
}

/** What to tell the user when the mic cannot be opened. */
export function micErrorText(err: unknown): string {
  const name = err instanceof DOMException ? err.name : "";
  switch (name) {
    case "NotAllowedError":
    case "SecurityError":
      return "Microphone access is blocked. On Windows: Settings › Privacy & security › Microphone › let desktop apps use it.";
    case "NotFoundError":
    case "OverconstrainedError":
      return "No microphone found.";
    case "NotReadableError":
    case "AbortError":
      return "The microphone is busy in another app.";
  }
  return `Could not start the microphone: ${errorText(err)}`;
}

/**
 * Records one dictation. A simple RMS detector ends it: 1.5 s after the
 * last speech, at 30 s, or after 8 s without any speech.
 */
export class Recorder {
  private stream?: MediaStream;
  private ctx?: AudioContext;
  private nodes: AudioNode[] = [];
  private chunks: Float32Array[] = [];
  private rate = TARGET_RATE;
  private startedAt = 0;
  private acc = 0;
  private accN = 0;
  // Seeded at a quiet room level, so speech that starts at once still counts.
  private floor = INITIAL_FLOOR_RMS;
  private lastSpeech = 0;
  private stopped = false;
  heardSpeech = false;

  async start(onLevel: (rms: number) => void, onAutoStop: (why: StopReason) => void): Promise<void> {
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    });
    if (this.stopped) {
      this.release(); // cancelled while the mic was opening: close it again
      return;
    }
    try {
      try {
        // Chromium and WebKit resample the mic to the context rate for us.
        this.ctx = new AudioContext({ sampleRate: TARGET_RATE });
      } catch {
        this.ctx = new AudioContext();
      }
      this.rate = this.ctx.sampleRate;
      const url = URL.createObjectURL(new Blob([TAP_WORKLET], { type: "application/javascript" }));
      try {
        await this.ctx.audioWorklet.addModule(url);
      } finally {
        URL.revokeObjectURL(url);
      }
      if (this.stopped) {
        this.release(); // cancelled while starting
        return;
      }
      const source = this.ctx.createMediaStreamSource(this.stream);
      const tap = new AudioWorkletNode(this.ctx, "ar-tap");
      const mute = this.ctx.createGain();
      mute.gain.value = 0; // pulled by the destination, but silent
      source.connect(tap).connect(mute).connect(this.ctx.destination);
      this.nodes = [source, tap, mute];

      const windowSize = Math.round((this.rate * LEVEL_WINDOW_MS) / 1000);
      this.startedAt = performance.now();
      tap.port.onmessage = (e: MessageEvent<Float32Array>) => {
        if (this.stopped) return;
        const block = e.data;
        this.chunks.push(block);
        for (let i = 0; i < block.length; i++) this.acc += block[i] * block[i];
        this.accN += block.length;
        if (this.accN < windowSize) return;
        const rms = Math.sqrt(this.acc / this.accN);
        this.acc = 0;
        this.accN = 0;
        onLevel(rms);
        const why = this.detect(rms);
        if (why) onAutoStop(why);
      };
    } catch (err) {
      this.release();
      throw err;
    }
  }

  /** One VAD step; returns why recording should end, if it should. */
  private detect(rms: number): StopReason | null {
    const now = performance.now();
    // Noise floor: follows quiet levels at once, louder ones only slowly.
    this.floor = rms < this.floor ? rms : this.floor * 0.995 + rms * 0.005;
    if (rms > Math.max(SPEECH_MIN_RMS, this.floor * SPEECH_OVER_FLOOR)) {
      this.heardSpeech = true;
      this.lastSpeech = now;
    }
    const elapsed = now - this.startedAt;
    if (this.heardSpeech && now - this.lastSpeech > SILENCE_STOP_MS) return "silence";
    if (elapsed > MAX_RECORD_MS) return "limit";
    if (!this.heardSpeech && elapsed > NO_SPEECH_STOP_MS) return "nothing";
    return null;
  }

  elapsedMs(): number {
    return this.startedAt ? performance.now() - this.startedAt : 0;
  }

  /** Ends the recording and returns it as 16 kHz mono Int16 PCM. */
  stop(): Int16Array {
    const len = this.chunks.reduce((n, c) => n + c.length, 0);
    const all = new Float32Array(len);
    let off = 0;
    for (const c of this.chunks) {
      all.set(c, off);
      off += c.length;
    }
    this.release();
    return toInt16(resampleTo16k(all, this.rate));
  }

  /** Drops the recording and releases the mic. */
  cancel(): void {
    this.release();
  }

  private release(): void {
    this.stopped = true;
    this.chunks = [];
    for (const n of this.nodes) n.disconnect();
    this.nodes = [];
    this.stream?.getTracks().forEach((t) => t.stop());
    this.stream = undefined;
    void this.ctx?.close().catch(() => undefined);
    this.ctx = undefined;
  }
}

/** The live level meter: 24 bars, newest on the right, at most 30 fps. */
export class Waveform {
  private levels: number[];
  private frame = 0;
  private lastDraw = 0;
  private readonly bars: number;
  private readonly bar: number;
  private readonly gap: number;
  private readonly frameMs: number;

  constructor(private canvas: HTMLCanvasElement) {
    const css = getComputedStyle(document.documentElement);
    const px = (name: string) => parseFloat(css.getPropertyValue(name)) || 0;
    this.bars = px("--wave-bars") || 24;
    this.bar = px("--wave-bar");
    this.gap = px("--wave-gap");
    this.frameMs = 1000 / (px("--wave-fps") || 30);
    this.levels = new Array<number>(this.bars).fill(0);
  }

  push(rms: number): void {
    this.levels.push(Math.min(1, Math.sqrt(rms) * 3.2));
    this.levels.shift();
    if (!this.frame) this.frame = requestAnimationFrame((t) => this.draw(t));
  }

  clear(): void {
    this.levels.fill(0);
    if (this.frame) cancelAnimationFrame(this.frame);
    this.frame = 0;
    this.draw(performance.now() + this.frameMs);
  }

  private draw(t: number): void {
    this.frame = 0;
    if (t - this.lastDraw < this.frameMs) {
      this.frame = requestAnimationFrame((n) => this.draw(n));
      return;
    }
    this.lastDraw = t;
    const c = this.canvas;
    const dpr = window.devicePixelRatio || 1;
    const w = c.clientWidth;
    const h = c.clientHeight;
    if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) {
      c.width = Math.round(w * dpr);
      c.height = Math.round(h * dpr);
    }
    const g = c.getContext("2d");
    if (!g) return;
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, w, h);
    const style = getComputedStyle(c);
    g.fillStyle = style.color;
    g.globalAlpha = parseFloat(style.getPropertyValue("--wave-opacity")) || 0.8;
    this.levels.forEach((lv, i) => {
      const bh = Math.max(this.bar, lv * h);
      const x = i * (this.bar + this.gap);
      g.beginPath();
      g.roundRect(x, (h - bh) / 2, this.bar, bh, this.bar / 2);
      g.fill();
    });
  }
}

function megabytes(bytes: number): string {
  const mb = bytes / 1e6;
  return mb >= 10 ? `${Math.round(mb)} MB` : `${mb.toFixed(1)} MB`;
}

function clock(ms: number): string {
  const s = Math.floor(ms / 1000);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export type VoiceState = "idle" | "consent" | "installing" | "ready-note" | "listening" | "transcribing";

/** What the voice controller needs from the capture box. */
export interface VoiceHost {
  box: HTMLElement;
  input: HTMLInputElement;
  /** Puts dictated text into the input and refreshes the preview. */
  insert(text: string): void;
  showError(msg: string): void;
  /** Swaps the leading icon while listening ("mic") and back (null). */
  setListeningLead(on: boolean): void;
}

export interface VoiceControl {
  /** Ctrl+M and the mic button. */
  toggle(): void;
  /** Esc: returns true when voice consumed it. */
  escape(): boolean;
  /** The window lost focus or is hiding: drop any recording. */
  cancel(): void;
  state(): VoiceState;
}

/** Wires the mic button, listening row and setup panel into the capture box. */
export function mountVoice(root: HTMLElement, host: VoiceHost): VoiceControl {
  const mic = root.querySelector<HTMLButtonElement>("#cap-mic")!;
  const micIcon = root.querySelector<HTMLElement>("#cap-mic-icon")!;
  const listenRow = root.querySelector<HTMLElement>("#cap-listen")!;
  const listenLabel = root.querySelector<HTMLElement>("#cap-listen-label")!;
  const timerEl = root.querySelector<HTMLElement>("#cap-timer")!;
  const live = root.querySelector<HTMLElement>("#cap-voice-live")!;
  const panel = root.querySelector<HTMLElement>("#cap-voice")!;
  const title = root.querySelector<HTMLElement>("#cap-voice-title")!;
  const body = root.querySelector<HTMLElement>("#cap-voice-body")!;
  const progress = root.querySelector<HTMLElement>("#cap-progress")!;
  const bar = root.querySelector<HTMLElement>("#cap-progress-bar")!;
  const meta = root.querySelector<HTMLElement>("#cap-voice-meta")!;
  const actions = root.querySelector<HTMLElement>("#cap-voice-actions")!;
  const wave = new Waveform(root.querySelector<HTMLCanvasElement>("#cap-wave")!);

  let state: VoiceState = "idle";
  let status: VoiceStatus | null = null;
  let recorder: Recorder | null = null;
  let pending: Cancellable<string> | null = null;
  let ticker: number | undefined;
  let noteTimer: number | undefined;
  let run = 0; // invalidates async work of an earlier dictation

  function announce(msg: string): void {
    live.textContent = msg;
  }

  function setMic(listening: boolean): void {
    mic.setAttribute("aria-pressed", String(listening));
    mic.setAttribute("aria-label", listening ? "Stop dictation" : "Start dictation");
    mic.title = listening ? "Stop (Ctrl+M)" : "Dictate (Ctrl+M)";
    micIcon.innerHTML = listening ? icons.stop : icons.mic;
  }

  function setState(next: VoiceState): void {
    state = next;
    host.box.dataset.voice = next;
    const recording = next === "listening" || next === "transcribing";
    listenRow.hidden = !recording;
    host.setListeningLead(recording);
    setMic(next === "listening");
    mic.disabled = next === "transcribing";
    panel.hidden = !(next === "consent" || next === "installing" || next === "ready-note");
    if (next !== "listening") window.clearInterval(ticker);
    if (next === "listening") {
      listenLabel.replaceChildren("Listening…");
      timerEl.textContent = "0:00";
      wave.clear();
    } else if (next === "transcribing") {
      const dots = el("span", "cap-dots");
      dots.setAttribute("aria-hidden", "true");
      dots.append(el("i"), el("i"), el("i"));
      listenLabel.replaceChildren("Transcribing", dots);
    }
  }

  function button(label: string, cls: string, onClick: () => void, icon?: string): HTMLButtonElement {
    const b = el("button", `btn ${cls}`);
    b.type = "button";
    if (icon) b.innerHTML = icon;
    b.append(label);
    b.addEventListener("click", onClick);
    return b;
  }

  /** Renders the setup panel for the current status. */
  function renderPanel(): void {
    const st = status;
    window.clearTimeout(noteTimer);
    progress.hidden = true;
    meta.textContent = "";
    meta.classList.remove("is-error");
    actions.replaceChildren();
    if (state === "ready-note") {
      title.textContent = "Voice is ready";
      body.textContent = "Press Ctrl+M or click the mic to start.";
      noteTimer = window.setTimeout(() => {
        if (state === "ready-note") {
          setState("idle");
          host.input.focus();
        }
      }, READY_NOTE_MS);
      return;
    }
    if (!st) return;
    if (st.state === "unsupported") {
      title.textContent = "Voice needs a speech engine";
      body.textContent = st.hint;
      actions.append(button("Close", "btn-ghost", closePanel));
      (actions.firstElementChild as HTMLElement).focus();
      return;
    }
    if (st.state === "installing") {
      title.textContent = "Setting up voice";
      body.textContent = "You can close this box; the download continues in the background.";
      const pct = st.total > 0 ? Math.min(100, Math.round((st.done / st.total) * 100)) : 0;
      progress.hidden = false;
      progress.setAttribute("aria-valuenow", String(pct));
      progress.setAttribute("aria-valuetext", `${pct}%`);
      bar.style.transform = `scaleX(${pct / 100})`;
      const what = st.phase === "engine" ? "Downloading speech engine" : "Downloading English model";
      meta.textContent = `${what}… ${megabytes(st.done)} of ${megabytes(st.total)}`;
      const cancel = button("Cancel", "btn-ghost", () => void api.voice.cancelInstall());
      actions.append(cancel);
      if (!panel.contains(document.activeElement)) cancel.focus();
      return;
    }
    // absent: ask for consent
    title.textContent = "Dictate with your voice";
    const what = st.engine === "bundled" ? "speech engine and English model" : "English model";
    body.textContent =
      "Speech is turned into text on this computer by whisper.cpp. Nothing you say leaves it. " +
      `Voice needs a one-time download of ${megabytes(st.download)} (${what}).`;
    if (st.error) {
      meta.textContent = st.error;
      meta.classList.add("is-error");
    }
    const dl = button(`Download ${megabytes(st.download)}`, "btn-primary", () => void install(), icons.download);
    actions.append(button("Not now", "btn-ghost", closePanel), dl);
    dl.focus();
  }

  function closePanel(): void {
    setState("idle");
    host.input.focus();
  }

  async function install(): Promise<void> {
    try {
      await api.voice.install();
    } catch (err) {
      host.showError(errorText(err));
      return;
    }
    status = await api.voice.status();
    setState("installing");
    renderPanel();
  }

  async function refreshStatus(): Promise<VoiceStatus | null> {
    try {
      status = await api.voice.status();
    } catch (err) {
      host.showError(errorText(err));
      status = null;
    }
    return status;
  }

  async function start(): Promise<void> {
    host.showError("");
    const st = await refreshStatus();
    if (!st) return;
    if (st.state !== "ready") {
      setState(st.state === "installing" ? "installing" : "consent");
      renderPanel();
      return;
    }
    const my = ++run;
    const rec = new Recorder();
    recorder = rec;
    setState("listening");
    announce("Listening");
    try {
      await rec.start(
        (rms) => wave.push(rms),
        (why) => {
          if (my === run && state === "listening") void finish(why);
        },
      );
    } catch (err) {
      if (my !== run) return;
      recorder = null;
      setState("idle");
      host.showError(micErrorText(err));
      host.input.focus();
      return;
    }
    if (my !== run) return;
    ticker = window.setInterval(() => {
      timerEl.textContent = clock(rec.elapsedMs());
    }, 250);
    // Load the model while the user speaks; errors surface on Transcribe.
    void api.voice.warm().catch(() => undefined);
  }

  async function finish(why: StopReason): Promise<void> {
    const rec = recorder;
    if (!rec || state !== "listening") return;
    recorder = null;
    const heard = rec.heardSpeech;
    const pcm = rec.stop();
    const my = ++run;
    if (why === "nothing" || (!heard && why !== "user") || pcm.length < MIN_SAMPLES) {
      setState("idle");
      host.showError("Didn't catch that. Try again closer to the mic.");
      host.input.focus();
      announce("Nothing heard");
      return;
    }
    setState("transcribing");
    announce("Transcribing");
    const p = api.voice.transcribe(pcm);
    pending = p;
    let text: string;
    try {
      text = await p;
    } catch (err) {
      if (my !== run) return; // cancelled
      pending = null;
      setState("idle");
      host.showError(errorText(err));
      host.input.focus();
      return;
    }
    if (my !== run) return;
    pending = null;
    setState("idle");
    if (text.trim() === "") {
      host.showError("Didn't catch that. Try again closer to the mic.");
      host.input.focus();
      announce("Nothing heard");
      return;
    }
    host.insert(text);
    announce("Ready to save");
  }

  function cancel(): void {
    run++;
    recorder?.cancel();
    recorder = null;
    pending?.cancel();
    pending = null;
    if (state === "listening" || state === "transcribing") setState("idle");
  }

  function toggle(): void {
    switch (state) {
      case "idle":
      case "ready-note":
        void start();
        break;
      case "listening":
        void finish("user");
        break;
      case "consent":
      case "installing":
        closePanel();
        break;
      case "transcribing":
        break;
    }
  }

  function escape(): boolean {
    switch (state) {
      case "listening":
      case "transcribing":
        cancel();
        announce("Cancelled");
        host.input.focus();
        return true;
      case "consent":
      case "installing":
      case "ready-note":
        closePanel();
        return true;
    }
    return false;
  }

  mic.addEventListener("click", () => toggle());
  // Keep focus where it is (the input) when the mic is clicked.
  mic.addEventListener("mousedown", (e) => e.preventDefault());

  on<VoiceStatus>(events.voiceStatus, (st) => {
    const wasInstalling = status?.state === "installing" || state === "installing";
    status = st;
    if (state === "installing" || state === "consent") {
      if (st.state === "ready" && wasInstalling) {
        setState("ready-note");
        announce("Voice is ready");
      } else if (st.state === "installing") {
        setState("installing");
      } else if (st.state === "absent" || st.state === "unsupported") {
        setState("consent");
      }
      renderPanel();
    }
  });

  setState("idle");
  return { toggle, escape, cancel, state: () => state };
}
