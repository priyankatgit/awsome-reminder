// One dictation session (Phase 3): segments from the recorder are transcribed
// in order, each result becomes spoken-command operations, and the session
// ends by the rules of its mode. No audio or Wails APIs here, so it is tested
// with fakes; voice.ts wires it to the Recorder and the capture box.
import { parseSegment } from "./voice-commands";
import { endReason, SegmentQueue, type EndReason, type PcmSegment, type SessionMode } from "./voice-segment";

export const NOTHING_HEARD = "Didn't catch that. Try again closer to the mic.";
export const LIMIT_NOTE = "Stopped after 5 minutes";
const PROMPT_TAIL = " new task";

/** The parts of the Recorder (segment mode) a session uses. */
export interface SessionRecorder {
  readonly heardSpeech: boolean;
  elapsedMs(): number;
  /** In the same clock as SessionDeps.now; 0 before any speech. */
  lastSpeechAt(): number;
  /** Flushes the open segment and releases the mic. */
  stopSegments(): PcmSegment | null;
  /** Drops the open segment and releases the mic. */
  cancel(): void;
}

/** What a session does to the capture box. */
export interface SessionHost {
  insert(text: string): void;
  /** Boundary: the input becomes a new row. Returns the row count, 0 when nothing was added. */
  addTask(): number;
  /** Returns whether anything was removed. */
  scratch(): boolean;
  saveAll(spoken: boolean): Promise<void>;
  cancelAll(spoken: boolean): void;
  multi(): boolean;
  sessionText(): string;
  showError(msg: string): void;
  showNote(msg: string): void;
  /** The role="status" line. */
  announce(msg: string): void;
  /** The shimmer at the end of the input. */
  setBusy(on: boolean): void;
}

export type SessionPhase = "listening" | "draining" | "done";
export type SessionEnd = EndReason | "user" | "escape" | "blur" | "save" | "cancel" | "abort";

export interface SessionDeps {
  transcribe(seg: PcmSegment, prompt: string): Promise<string>;
  now(): number;
  /** Called once when the session is over (api.voice.sessionEnded). */
  sessionEnded(): void;
  errorText(err: unknown): string;
  onPhase(phase: SessionPhase): void;
  /** Stops an in-flight request whose result is no longer wanted. */
  cancelTranscribe?(): void;
}

export class VoiceSession {
  private phase: SessionPhase = "listening";
  private readonly queue: SegmentQueue;
  private pending = 0; // segments pushed and not yet applied or failed
  private afterBoundary = false;
  private heard = false; // some segment produced an operation
  private failed = false;
  private end: SessionEnd | null = null;
  private saveAfter = false; // "save all" heard: save once drained
  private blurred = false;
  private drainer: Promise<void> | null = null;

  constructor(
    private mode: SessionMode,
    private readonly rec: SessionRecorder,
    private readonly host: SessionHost,
    private readonly deps: SessionDeps,
  ) {
    this.queue = new SegmentQueue(
      (s, prompt) => deps.transcribe(s, prompt),
      (text) => this.settled(() => this.apply(text)),
      (err) => this.settled(() => this.fail(err)),
      () => (host.sessionText() + PROMPT_TAIL).trim(),
    );
  }

  state(): SessionPhase {
    return this.phase;
  }

  /** Why the session stopped listening; null while it listens. */
  endedBy(): SessionEnd | null {
    return this.end;
  }

  sessionMode(): SessionMode {
    return this.mode;
  }

  /** A segment from the recorder. */
  segment(s: PcmSegment): void {
    if (this.phase === "done") return;
    this.pending++;
    this.host.setBusy(true);
    this.queue.push(s);
  }

  /** Checks the end rules (called a few times a second while listening). */
  tick(): EndReason | null {
    if (this.phase !== "listening") return null;
    const heardSpeech = this.rec.heardSpeech;
    const why = endReason({
      mode: this.mode,
      elapsedMs: this.rec.elapsedMs(),
      heardSpeech,
      sinceSpeechMs: heardSpeech ? this.deps.now() - this.rec.lastSpeechAt() : 0,
      afterBoundary: this.afterBoundary,
    });
    if (!why) return null;
    // A segment still in flight may hold a "new task" that extends the wait.
    if (why === "silence" && this.pending > 0) return null;
    if (why === "limit") this.host.showNote(LIMIT_NOTE);
    // With no speech heard there is no open segment worth sending.
    this.stop(why, why === "silence" || why === "limit");
    void this.drain();
    return why;
  }

  /** The box got focus: a wake session follows the focused rules from now on. */
  focusGained(): void {
    this.mode = "focused";
  }

  /** Stop listening (Ctrl+M, the stop button, Enter) and wait for the queue. */
  finish(): Promise<void> {
    if (this.phase === "listening") this.stop("user", true);
    if (this.phase === "done") return this.drainer ?? Promise.resolve();
    return this.drain();
  }

  /** Esc: drop the audio not yet in a segment; the queue still finishes. */
  escape(): void {
    if (this.phase !== "listening") return;
    this.stop("escape", false);
    this.host.announce("Stopped listening");
    void this.drain();
  }

  /** Window blur or hide: release the mic; queued segments land in the draft. */
  blur(): void {
    this.blurred = true;
    this.saveAfter = false; // nothing is saved behind a hidden box
    if (this.phase !== "listening") return;
    this.stop("blur", false);
    void this.drain();
  }

  /** The mic could not start: end without messages. */
  abort(): void {
    if (this.phase === "done") return;
    if (this.phase === "listening") this.rec.cancel();
    this.end ??= "abort";
    this.dropQueue();
    this.done();
  }

  private stop(end: SessionEnd, keepTail: boolean): void {
    if (this.phase !== "listening") return;
    this.end = end;
    let tail: PcmSegment | null = null;
    if (keepTail) tail = this.rec.stopSegments();
    else this.rec.cancel();
    this.phase = "draining";
    this.deps.onPhase("draining");
    if (tail) this.segment(tail);
  }

  private settled(fn: () => void): void {
    if (this.pending > 0) this.pending--;
    try {
      fn();
    } finally {
      if (this.pending === 0) this.host.setBusy(false);
    }
  }

  private apply(text: string): void {
    for (const op of parseSegment(text, { multi: this.host.multi() })) {
      if (this.phase === "done") return; // a "cancel" ended the session
      this.heard = true;
      switch (op.kind) {
        case "text":
          this.host.insert(op.text);
          this.afterBoundary = false;
          break;
        case "boundary": {
          const n = this.host.addTask();
          if (n > 0) this.host.announce(`Task ${n} added`);
          this.afterBoundary = true;
          break;
        }
        case "scratch":
          if (this.host.scratch()) this.host.announce("Removed");
          break;
        case "save":
          if (this.blurred) break;
          this.saveAfter = true;
          this.stop("save", true);
          void this.drain();
          break;
        case "cancel":
          if (this.blurred) break;
          this.cancel();
          break;
      }
    }
  }

  private fail(err: unknown): void {
    this.failed = true;
    this.host.showError(`Couldn't transcribe the last bit: ${this.deps.errorText(err)}`);
  }

  /** Spoken "cancel": drop the rest and discard (with Undo). */
  private cancel(): void {
    if (this.phase === "listening") {
      this.end = "cancel";
      this.rec.cancel();
    }
    this.dropQueue();
    this.done();
    this.host.cancelAll(true);
  }

  private dropQueue(): void {
    this.queue.cancel();
    this.deps.cancelTranscribe?.();
    this.pending = 0;
    this.host.setBusy(false);
  }

  private drain(): Promise<void> {
    this.drainer ??= this.runDrain();
    return this.drainer;
  }

  private async runDrain(): Promise<void> {
    await this.queue.idle();
    if (this.phase === "done") return; // cancelled meanwhile
    this.done();
    if (this.saveAfter) {
      await this.host.saveAll(true);
      return;
    }
    const end = this.end;
    if (end === "blur" || end === "quiet-wake") return; // quietly into the draft
    if (!this.heard && !this.failed) {
      if (end === "escape") return;
      this.host.showError(NOTHING_HEARD);
      this.host.announce("Nothing heard");
      return;
    }
    if (this.host.sessionText() !== "") this.host.announce("Ready to save");
  }

  private done(): void {
    this.phase = "done";
    this.deps.onPhase("done");
    this.deps.sessionEnded();
  }
}
