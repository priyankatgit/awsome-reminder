// Continuous dictation helpers (Phase 3): cut the mic stream into segments at
// pauses, decide when a session ends, and transcribe segments one at a time
// in order. Pure logic; no audio APIs. voice.ts imports from here, not back.

export const PRE_ROLL_MS = 300;
export const CUT_SILENCE_MS = 700;
export const MAX_SEGMENT_MS = 20000;
export const FORCE_CUT_WINDOW_MS = 2000;
export const QUIET_WINDOW_MS = 100;
export const MIN_SPEECH_MS = 250;

export type Segment = { pcm: Float32Array; speechMs: number };
export type PcmSegment = { pcm: Int16Array /* 16 kHz mono */; speechMs: number };

interface Blk {
  data: Float32Array;
  speech: boolean;
  energy: number; // sum of squares
}

/** Cuts a stream of blocks into speech segments at pauses. */
export class Segmenter {
  private preRoll: Blk[] = [];
  private preRollN = 0;
  private cur: Blk[] = [];
  private curN = 0;
  private speechN = 0;
  private silenceN = 0; // trailing non-speech samples

  private readonly preRollMax: number;
  private readonly cutSilence: number;
  private readonly maxSeg: number;
  private readonly forceWin: number;
  private readonly quietWin: number;
  private readonly minSpeech: number;

  constructor(private rate: number) {
    const n = (ms: number) => Math.round((rate * ms) / 1000);
    this.preRollMax = n(PRE_ROLL_MS);
    this.cutSilence = n(CUT_SILENCE_MS);
    this.maxSeg = n(MAX_SEGMENT_MS);
    this.forceWin = n(FORCE_CUT_WINDOW_MS);
    this.quietWin = n(QUIET_WINDOW_MS);
    this.minSpeech = n(MIN_SPEECH_MS);
  }

  push(block: Float32Array, speech: boolean): Segment | null {
    let energy = 0;
    for (let i = 0; i < block.length; i++) energy += block[i] * block[i];
    const b: Blk = { data: block, speech, energy };

    if (this.cur.length === 0) {
      if (!speech) {
        this.preRoll.push(b);
        this.preRollN += block.length;
        while (this.preRoll.length > 1 && this.preRollN - this.preRoll[0].data.length >= this.preRollMax) {
          this.preRollN -= this.preRoll.shift()!.data.length;
        }
        return null;
      }
      for (const p of this.preRoll) this.add(p);
      this.preRoll = [];
      this.preRollN = 0;
    }
    this.add(b);

    if (this.silenceN >= this.cutSilence) return this.finish();
    if (this.curN >= this.maxSeg) return this.forceCut();
    return null;
  }

  /** Ends the stream: whatever is buffered, if it holds enough speech. */
  flush(): Segment | null {
    this.preRoll = [];
    this.preRollN = 0;
    return this.finish();
  }

  private add(b: Blk): void {
    this.cur.push(b);
    this.curN += b.data.length;
    if (b.speech) {
      this.speechN += b.data.length;
      this.silenceN = 0;
    } else {
      this.silenceN += b.data.length;
    }
  }

  private finish(): Segment | null {
    return this.emit(this.cur.length);
  }

  /** Cuts the first `count` blocks off as a segment; the rest carries over. */
  private emit(count: number): Segment | null {
    const head = this.cur.slice(0, count);
    const rest = this.cur.slice(count);
    this.cur = [];
    this.curN = 0;
    this.speechN = 0;
    this.silenceN = 0;
    this.preRoll = [];
    this.preRollN = 0;
    let headSpeech = 0;
    let n = 0;
    for (const b of head) {
      n += b.data.length;
      if (b.speech) headSpeech += b.data.length;
    }
    for (const b of rest) this.add(b);
    if (headSpeech < this.minSpeech) return null;
    const pcm = new Float32Array(n);
    let off = 0;
    for (const b of head) {
      pcm.set(b.data, off);
      off += b.data.length;
    }
    return { pcm, speechMs: (headSpeech / this.rate) * 1000 };
  }

  /** At the length cap: cut in the quietest window of the last 2 s. */
  private forceCut(): Segment | null {
    // Candidate windows end at block boundaries within the last forceWin samples.
    const from = this.curN - this.forceWin;
    let pos = 0; // sample offset where block i starts
    const starts: number[] = [];
    for (const b of this.cur) {
      starts.push(pos);
      pos += b.data.length;
    }
    let bestMean = Infinity;
    let bestCut = this.cur.length;
    for (let i = 0; i < this.cur.length; i++) {
      if (starts[i] < from) continue;
      let sum = 0;
      let n = 0;
      let j = i;
      while (j < this.cur.length && n < this.quietWin) {
        sum += this.cur[j].energy;
        n += this.cur[j].data.length;
        j++;
      }
      if (n < this.quietWin) break; // window runs past the end
      const mean = sum / n;
      if (mean < bestMean) {
        bestMean = mean;
        // cut in the middle of the window
        bestCut = i + Math.max(1, Math.floor((j - i) / 2));
      }
    }
    return this.emit(bestCut);
  }
}

export type SessionMode = "focused" | "wake";
export type EndReason = "silence" | "nothing" | "limit" | "quiet-wake";

const LIMIT_MS = 300000;

/** When (and why) a dictation session should end; null to keep going. */
export function endReason(t: {
  mode: SessionMode;
  elapsedMs: number;
  heardSpeech: boolean;
  sinceSpeechMs: number;
  afterBoundary: boolean;
}): EndReason | null {
  if (t.elapsedMs >= LIMIT_MS) return "limit";
  if (t.mode === "focused") {
    if (t.heardSpeech) {
      if (t.sinceSpeechMs >= (t.afterBoundary ? 8000 : 2000)) return "silence";
    } else if (t.elapsedMs >= 8000) {
      return "nothing";
    }
    return null;
  }
  if (t.heardSpeech) {
    if (t.sinceSpeechMs >= 15000) return "silence";
  } else if (t.elapsedMs >= 6000) {
    return "quiet-wake";
  }
  return null;
}

/** Transcribes segments one at a time and applies results in push order. */
export class SegmentQueue {
  private items: PcmSegment[] = [];
  private busy = false;
  private gen = 0;
  private waiters: (() => void)[] = [];

  constructor(
    private transcribe: (seg: PcmSegment, prompt: string) => Promise<string>,
    private apply: (text: string) => void,
    private fail: (err: unknown) => void,
    private prompt: () => string,
  ) {}

  push(seg: PcmSegment): void {
    this.items.push(seg);
    void this.pump();
  }

  /** Resolves when nothing is queued or in flight. */
  idle(): Promise<void> {
    if (!this.busy && this.items.length === 0) return Promise.resolve();
    return new Promise((resolve) => this.waiters.push(resolve));
  }

  /** Drops pending items and ignores the result of the one in flight. */
  cancel(): void {
    this.gen++;
    this.items = [];
    this.busy = false; // the in-flight request no longer blocks new pushes
    this.release();
  }

  private release(): void {
    if (this.busy || this.items.length > 0) return;
    const w = this.waiters;
    this.waiters = [];
    for (const r of w) r();
  }

  private async pump(): Promise<void> {
    if (this.busy) return;
    this.busy = true;
    const my = this.gen;
    try {
      while (my === this.gen && this.items.length > 0) {
        const seg = this.items.shift()!;
        try {
          const text = await this.transcribe(seg, this.prompt());
          if (my !== this.gen) return;
          this.apply(text);
        } catch (err) {
          if (my !== this.gen) return;
          this.fail(err);
        }
      }
    } finally {
      // After a cancel the flag is already reset and may belong to a newer pump.
      if (my === this.gen) {
        this.busy = false;
        this.release();
      }
    }
  }
}
