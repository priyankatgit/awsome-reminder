import { describe, expect, it } from "vitest";
import { endReason, Segmenter, SegmentQueue, type PcmSegment, type Segment } from "./voice-segment";

const RATE = 16000;
const BLOCK = 128;
const blockMs = (BLOCK / RATE) * 1000;

/** Feeds `ms` of constant-amplitude audio in 128-sample blocks. */
function feed(seg: Segmenter, out: Segment[], ms: number, speech: boolean, amp = 0.2): void {
  const n = Math.round(ms / blockMs);
  for (let i = 0; i < n; i++) {
    const s = seg.push(new Float32Array(BLOCK).fill(amp), speech);
    if (s) out.push(s);
  }
}

describe("Segmenter", () => {
  it("cuts after 700 ms of silence and keeps 300 ms pre-roll", () => {
    const seg = new Segmenter(RATE);
    const out: Segment[] = [];
    feed(seg, out, 1000, false, 0.001);
    feed(seg, out, 1000, true);
    feed(seg, out, 1000, false, 0.001);
    expect(out).toHaveLength(1);
    const ms = (out[0].pcm.length / RATE) * 1000;
    expect(ms).toBeGreaterThanOrEqual(300 + 1000 - blockMs);
    expect(ms).toBeLessThanOrEqual(300 + 1000 + 700 + blockMs);
    expect(Math.abs(out[0].speechMs - 1000)).toBeLessThanOrEqual(blockMs);
    expect(seg.flush()).toBeNull();
  });

  it("drops segments under 250 ms of speech", () => {
    const seg = new Segmenter(RATE);
    const out: Segment[] = [];
    feed(seg, out, 500, false, 0.001);
    feed(seg, out, 200, true);
    feed(seg, out, 1500, false, 0.001);
    expect(out).toHaveLength(0);
    expect(seg.flush()).toBeNull();
  });

  it("force-cuts at 20 s at the quietest window", () => {
    const seg = new Segmenter(RATE);
    const out: Segment[] = [];
    feed(seg, out, 19000, true, 0.3);
    feed(seg, out, 100, true, 0.01); // the quiet window, 19.0 to 19.1 s
    feed(seg, out, 2900, true, 0.3);
    expect(out).toHaveLength(1);
    const end = (out[0].pcm.length / RATE) * 1000;
    expect(end).toBeGreaterThanOrEqual(19000 - blockMs);
    expect(end).toBeLessThanOrEqual(19100 + blockMs);
    const rest = seg.flush();
    expect(rest).not.toBeNull();
    const restMs = (rest!.pcm.length / RATE) * 1000;
    expect(restMs).toBeGreaterThan(2800);
    expect(Math.abs(restMs + end - 22000)).toBeLessThanOrEqual(2 * blockMs);
  });
});

describe("endReason", () => {
  const base = { elapsedMs: 0, heardSpeech: false, sinceSpeechMs: 0, afterBoundary: false };
  const rows: [string, Parameters<typeof endReason>[0], ReturnType<typeof endReason>][] = [
    ["focused silence", { ...base, mode: "focused", heardSpeech: true, elapsedMs: 5000, sinceSpeechMs: 2000 }, "silence"],
    ["focused just under silence", { ...base, mode: "focused", heardSpeech: true, elapsedMs: 5000, sinceSpeechMs: 1999 }, null],
    ["focused after boundary waits", { ...base, mode: "focused", heardSpeech: true, afterBoundary: true, elapsedMs: 9000, sinceSpeechMs: 7999 }, null],
    ["focused after boundary silence", { ...base, mode: "focused", heardSpeech: true, afterBoundary: true, elapsedMs: 9000, sinceSpeechMs: 8000 }, "silence"],
    ["focused nothing", { ...base, mode: "focused", elapsedMs: 8000 }, "nothing"],
    ["focused just under nothing", { ...base, mode: "focused", elapsedMs: 7999 }, null],
    ["wake silence", { ...base, mode: "wake", heardSpeech: true, elapsedMs: 20000, sinceSpeechMs: 15000 }, "silence"],
    ["wake just under silence", { ...base, mode: "wake", heardSpeech: true, elapsedMs: 20000, sinceSpeechMs: 14999 }, null],
    ["wake quiet", { ...base, mode: "wake", elapsedMs: 6000 }, "quiet-wake"],
    ["wake just under quiet", { ...base, mode: "wake", elapsedMs: 5999 }, null],
    ["focused limit", { ...base, mode: "focused", heardSpeech: true, elapsedMs: 300000, sinceSpeechMs: 100 }, "limit"],
    ["focused just under limit", { ...base, mode: "focused", heardSpeech: true, elapsedMs: 299999, sinceSpeechMs: 100 }, null],
    ["wake limit", { ...base, mode: "wake", heardSpeech: true, elapsedMs: 300000, sinceSpeechMs: 100 }, "limit"],
    ["wake unengaged cap", { ...base, mode: "wake", heardSpeech: true, elapsedMs: 60000, sinceSpeechMs: 100 }, "wake-limit"],
    ["wake just under unengaged cap", { ...base, mode: "wake", heardSpeech: true, elapsedMs: 59999, sinceSpeechMs: 100 }, null],
  ];
  it.each(rows)("%s", (_name, input, want) => {
    expect(endReason(input)).toBe(want);
  });
});

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const seg = (n: number): PcmSegment => ({ pcm: new Int16Array([n]), speechMs: 500 });
const tick = () => new Promise((r) => setTimeout(r, 0));

describe("SegmentQueue", () => {
  it("keeps order and survives a failed segment", async () => {
    const d = [deferred<string>(), deferred<string>(), deferred<string>()];
    const started: number[] = [];
    const applied: string[] = [];
    const failed: unknown[] = [];
    const q = new SegmentQueue(
      (s) => {
        started.push(s.pcm[0]);
        return d[s.pcm[0] - 1].promise;
      },
      (t) => applied.push(t),
      (e) => failed.push(e),
      () => "",
    );
    q.push(seg(1));
    q.push(seg(2));
    q.push(seg(3));
    await tick();
    expect(started).toEqual([1]); // one request at a time
    d[2].resolve("three"); // the third is ready first, but waits its turn
    await tick();
    expect(applied).toEqual([]);
    d[0].resolve("one");
    await tick();
    expect(started).toEqual([1, 2]);
    d[1].reject(new Error("boom"));
    await q.idle();
    expect(applied).toEqual(["one", "three"]);
    expect(failed).toHaveLength(1);
  });

  it("cancel ignores late results", async () => {
    const d = deferred<string>();
    const applied: string[] = [];
    const started: number[] = [];
    const q = new SegmentQueue(
      (s) => {
        started.push(s.pcm[0]);
        return d.promise;
      },
      (t) => applied.push(t),
      () => applied.push("failed"),
      () => "",
    );
    q.push(seg(1));
    q.push(seg(2));
    await tick();
    q.cancel();
    d.resolve("late");
    await q.idle();
    await tick();
    expect(applied).toEqual([]);
    expect(started).toEqual([1]);
  });

  it("asks for the prompt when a request starts", async () => {
    let ctx = "";
    const prompts: string[] = [];
    const q = new SegmentQueue(
      async (_s, p) => {
        prompts.push(p);
        return "x";
      },
      (t) => (ctx += t),
      () => undefined,
      () => ctx,
    );
    q.push(seg(1));
    q.push(seg(2));
    await q.idle();
    expect(prompts).toEqual(["", "x"]);
  });
});
