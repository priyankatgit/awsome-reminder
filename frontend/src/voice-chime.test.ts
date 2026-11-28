import { afterEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
});
vi.mock("./api", () => ({ api: {}, errorText: String, events: {}, on: () => () => {} }));

import { playChime } from "./voice";

// A fake AudioContext that records what the chime schedules. Oscillators
// "end" on the next tick after stop().
class FakeParam {
  value = 0;
  constructor(private log: string[], private name: string) {}
  setValueAtTime(v: number, t: number): void {
    this.log.push(`${this.name}.set ${v} @${t.toFixed(3)}`);
  }
  linearRampToValueAtTime(v: number, t: number): void {
    this.log.push(`${this.name}.ramp ${v} @${t.toFixed(3)}`);
  }
}

class FakeCtx {
  log: string[] = [];
  state: AudioContextState = "suspended";
  currentTime = 1;
  destination = {};
  closed = false;
  constructor(private resumesTo: AudioContextState | "never" = "running") {}
  resume(): Promise<void> {
    this.log.push("resume");
    if (this.resumesTo === "never") return new Promise(() => {}); // blocked: Chromium leaves it pending
    this.state = this.resumesTo;
    return Promise.resolve();
  }
  close(): Promise<void> {
    this.closed = true;
    this.state = "closed";
    return Promise.resolve();
  }
  createGain() {
    const gain = new FakeParam(this.log, "gain");
    return { gain, connect: (n: unknown) => n };
  }
  createOscillator() {
    const log = this.log;
    const osc = {
      type: "",
      frequency: new FakeParam(log, "freq"),
      onended: null as null | (() => void),
      connect: (n: unknown) => n,
      start: (t: number) => log.push(`start ${osc.type} ${osc.frequency.value} @${t.toFixed(3)}`),
      stop: (t: number) => {
        log.push(`stop @${t.toFixed(3)}`);
        setTimeout(() => osc.onended?.(), 0);
      },
    };
    return osc;
  }
}

const asCtx = (c: FakeCtx) => c as unknown as AudioContext;

describe("playChime", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("resumes first, then plays 660 Hz and 880 Hz for 70 ms each with 10 ms fades at 0.15", async () => {
    const c = new FakeCtx();
    await playChime(asCtx(c));
    expect(c.log[0]).toBe("resume");
    expect(c.log.filter((l) => l.startsWith("start"))).toEqual(["start sine 660 @1.000", "start sine 880 @1.070"]);
    expect(c.log.filter((l) => l.startsWith("stop"))).toEqual(["stop @1.070", "stop @1.140"]);
    // Each note: silent, up to 0.15 in 10 ms, held, down to 0 in its last 10 ms.
    expect(c.log.filter((l) => l.startsWith("gain"))).toEqual([
      "gain.set 0 @1.000",
      "gain.ramp 0.15 @1.010",
      "gain.set 0.15 @1.060",
      "gain.ramp 0 @1.070",
      "gain.set 0 @1.070",
      "gain.ramp 0.15 @1.080",
      "gain.set 0.15 @1.130",
      "gain.ramp 0 @1.140",
    ]);
    expect(c.closed).toBe(false); // a passed context belongs to the caller
  });

  it("rejects when the context is not running after resume", async () => {
    const c = new FakeCtx("suspended");
    await expect(playChime(asCtx(c))).rejects.toThrow(/suspended/);
    expect(c.log.some((l) => l.startsWith("start"))).toBe(false);
  });

  it("rejects when resume never settles (autoplay blocked)", async () => {
    vi.useFakeTimers();
    const c = new FakeCtx("never");
    const p = playChime(asCtx(c));
    const check = expect(p).rejects.toThrow(/suspended/);
    await vi.advanceTimersByTimeAsync(1000);
    await check;
  });

  it("creates and closes its own context when none is passed", async () => {
    const made: FakeCtx[] = [];
    const Orig = (globalThis as { AudioContext?: unknown }).AudioContext;
    (globalThis as { AudioContext?: unknown }).AudioContext = class extends FakeCtx {
      constructor() {
        super();
        made.push(this);
      }
    };
    try {
      await playChime();
    } finally {
      (globalThis as { AudioContext?: unknown }).AudioContext = Orig;
    }
    expect(made).toHaveLength(1);
    expect(made[0].closed).toBe(true);
  });
});
