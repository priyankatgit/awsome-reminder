import { describe, expect, it } from "vitest";
import type { PcmSegment } from "./voice-segment";
import { LIMIT_NOTE, NOTHING_HEARD, VoiceSession, type SessionHost, type SessionRecorder } from "./voice-session";

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Lets queued promise callbacks run. */
async function settle(): Promise<void> {
  for (let i = 0; i < 20; i++) await Promise.resolve();
}

const seg = (id: number): PcmSegment => ({ pcm: new Int16Array([id]), speechMs: 600 });

class FakeRecorder implements SessionRecorder {
  heardSpeech = false;
  elapsed = 0;
  last = 0;
  tail: PcmSegment | null = null;
  stopped = 0;
  cancelled = 0;
  elapsedMs(): number {
    return this.elapsed;
  }
  lastSpeechAt(): number {
    return this.last;
  }
  stopSegments(): PcmSegment | null {
    this.stopped++;
    const t = this.tail;
    this.tail = null;
    return t;
  }
  cancel(): void {
    this.cancelled++;
  }
}

/** A capture box stand-in: rows, the input, and a log of what was called. */
function fakeHost(multi = true) {
  const h = {
    rows: [] as string[],
    input: "",
    calls: [] as string[],
    errors: [] as string[],
    notes: [] as string[],
    said: [] as string[],
    busy: [] as boolean[],
    phases: [] as string[],
    insert(t: string) {
      h.input = h.input.trim() === "" ? t : `${h.input.trimEnd()} ${t}`;
      h.calls.push(`insert:${t}`);
    },
    addTask() {
      if (h.input.trim() === "") return 0;
      h.rows.push(h.input);
      h.input = "";
      h.calls.push("addTask");
      return h.rows.length;
    },
    scratch() {
      h.calls.push("scratch");
      if (h.input.trim() !== "") {
        h.input = "";
        return true;
      }
      return h.rows.pop() !== undefined;
    },
    async saveAll(spoken: boolean) {
      h.calls.push(`saveAll:${spoken}`);
    },
    cancelAll(spoken: boolean) {
      h.calls.push(`cancelAll:${spoken}`);
    },
    multi: () => multi,
    sessionText: () => [...h.rows, h.input].filter((s) => s.trim() !== "").join(" ").slice(-200),
    showError(m: string) {
      h.errors.push(m);
    },
    showNote(m: string) {
      h.notes.push(m);
    },
    announce(m: string) {
      h.said.push(m);
    },
    setBusy(on: boolean) {
      h.busy.push(on);
    },
  } satisfies SessionHost & Record<string, unknown>;
  return h;
}

/**
 * A transcriber whose results the test hands out per segment id, possibly
 * before the queue has started that segment's request.
 */
function fakeTranscriber() {
  const results = new Map<number, ReturnType<typeof deferred<string>>>();
  const result = (id: number) => {
    if (!results.has(id)) results.set(id, deferred<string>());
    return results.get(id)!;
  };
  const calls: { id: number; prompt: string }[] = [];
  return {
    calls,
    fn: (s: PcmSegment, prompt: string) => {
      calls.push({ id: s.pcm[0], prompt });
      return result(s.pcm[0]).promise;
    },
    say(id: number, text: string) {
      result(id).resolve(text);
    },
    fail(id: number, err: unknown) {
      result(id).reject(err);
    },
  };
}

function setup(opts: { multi?: boolean; mode?: "focused" | "wake" } = {}) {
  const host = fakeHost(opts.multi ?? true);
  const rec = new FakeRecorder();
  const tx = fakeTranscriber();
  let now = 0;
  let ended = 0;
  const s = new VoiceSession(opts.mode ?? "focused", rec, host, {
    transcribe: tx.fn,
    now: () => now,
    sessionEnded: () => ended++,
    errorText: (e) => (e instanceof Error ? e.message : String(e)),
    onPhase: (p) => host.phases.push(p),
  });
  return {
    s,
    host,
    rec,
    tx,
    ended: () => ended,
    /** Moves the clock: elapsed time and time since the last speech. */
    at(elapsed: number, sinceSpeech?: number) {
      now = elapsed;
      rec.elapsed = elapsed;
      if (sinceSpeech !== undefined) {
        rec.heardSpeech = true;
        rec.last = elapsed - sinceSpeech;
      }
    },
  };
}

describe("VoiceSession", () => {
  it("one task and a pause: the text lands in the input, then Ready to save", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.tx.say(1, "buy milk");
    await settle();
    expect(t.host.input).toBe("buy milk");
    expect(t.host.calls).toEqual(["insert:buy milk"]);
    t.at(1500, 1000);
    expect(t.s.tick()).toBeNull();
    t.at(3500, 2000);
    expect(t.s.tick()).toBe("silence");
    expect(t.rec.stopped).toBe(1); // the open segment is flushed
    await settle();
    expect(t.host.said).toContain("Ready to save");
    expect(t.host.errors).toEqual([]);
    expect(t.host.calls).not.toContain("saveAll:true");
    expect(t.ended()).toBe(1);
    expect(t.host.phases).toEqual(["draining", "done"]);
  });

  it("a, new task, b, next task, c: two rows and c in the input", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.tx.say(1, "a, new task, b, next task, c");
    await settle();
    expect(t.host.rows).toEqual(["a", "b"]);
    expect(t.host.input).toBe("c");
    expect(t.host.calls.filter((c) => c === "addTask")).toHaveLength(2);
    expect(t.host.said).toEqual(["Task 1 added", "Task 2 added"]);
  });

  it("a boundary with an empty input adds nothing", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.tx.say(1, "new task. next task");
    await settle();
    expect(t.host.rows).toEqual([]);
    expect(t.host.said).toEqual([]);
  });

  it("scratch that clears the input, or drops the last row when it is empty", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.s.segment(seg(2));
    t.s.segment(seg(3));
    t.tx.say(1, "call Sam new task buy milk");
    t.tx.say(2, "Scratch that.");
    await settle();
    expect(t.host.rows).toEqual(["call Sam"]);
    expect(t.host.input).toBe("");
    expect(t.host.said).toEqual(["Task 1 added", "Removed"]);
    t.tx.say(3, "scratch that");
    await settle();
    expect(t.host.rows).toEqual([]);
    expect(t.host.said.at(-1)).toBe("Removed");
  });

  it("save all: stops, waits for every pending segment, then saveAll(true)", async () => {
    const t = setup();
    t.rec.tail = seg(3); // audio still open when the save is heard
    t.s.segment(seg(1));
    t.s.segment(seg(2));
    t.tx.say(2, "save all"); // ready first, but applied after 1
    await settle();
    expect(t.host.calls).toEqual([]);
    t.tx.say(1, "buy milk");
    await settle();
    expect(t.rec.stopped).toBe(1);
    expect(t.host.calls).toEqual(["insert:buy milk"]); // segment 3 still pending
    t.tx.say(3, "and call Sam");
    await settle();
    expect(t.host.calls).toEqual(["insert:buy milk", "insert:and call Sam", "saveAll:true"]);
    expect(t.host.said).not.toContain("Ready to save");
    expect(t.ended()).toBe(1);
  });

  it("cancel: cancelAll(true) at once, later segments are dropped", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.s.segment(seg(2));
    t.tx.say(1, "buy milk. Never mind");
    await settle();
    expect(t.host.calls).toEqual(["insert:buy milk", "cancelAll:true"]);
    expect(t.rec.cancelled).toBe(1);
    expect(t.ended()).toBe(1);
    expect(t.host.phases.at(-1)).toBe("done");
    t.tx.say(2, "call Sam");
    await settle();
    expect(t.host.calls).toEqual(["insert:buy milk", "cancelAll:true"]);
  });

  it("a failed segment shows the error and later segments still apply in order", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.s.segment(seg(2));
    t.s.segment(seg(3));
    t.tx.say(3, "three");
    t.tx.fail(1, new Error("engine stopped"));
    await settle();
    expect(t.host.errors).toEqual(["Couldn't transcribe the last bit: engine stopped"]);
    t.tx.say(2, "two");
    await settle();
    expect(t.host.calls).toEqual(["insert:two", "insert:three"]);
    t.at(5000, 3000);
    expect(t.s.tick()).toBe("silence");
    await settle();
    expect(t.host.said).toContain("Ready to save");
  });

  it("single-task mode (reply): boundary and save phrases stay text", async () => {
    const t = setup({ multi: false });
    t.s.segment(seg(1));
    t.tx.say(1, "thanks new task save all");
    await settle();
    expect(t.host.calls).toEqual(["insert:thanks new task save all"]);
    expect(t.host.rows).toEqual([]);
  });

  it("after a boundary the end waits 8 s for the next task", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.tx.say(1, "call Sam, new task");
    await settle();
    t.at(5000, 3000);
    expect(t.s.tick()).toBeNull();
    t.at(9900, 7900);
    expect(t.s.tick()).toBeNull();
    t.at(10000, 8000);
    expect(t.s.tick()).toBe("silence");
  });

  it("text after a boundary goes back to the 2 s end", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.s.segment(seg(2));
    t.tx.say(1, "call Sam, new task");
    t.tx.say(2, "buy milk");
    await settle();
    t.at(5000, 2000);
    expect(t.s.tick()).toBe("silence");
  });

  it("does not end on silence while a segment is still being transcribed", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.at(5000, 2500);
    expect(t.s.tick()).toBeNull(); // it may hold a "new task"
    t.tx.say(1, "call Sam new task");
    await settle();
    expect(t.s.tick()).toBeNull(); // now the 8 s wait applies
  });

  it("shows the shimmer while segments are pending", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.s.segment(seg(2));
    expect(t.host.busy.at(-1)).toBe(true);
    t.tx.say(1, "a");
    await settle();
    expect(t.host.busy.at(-1)).toBe(true);
    t.tx.say(2, "b");
    await settle();
    expect(t.host.busy.at(-1)).toBe(false);
  });

  it("the prompt is the session text plus \"new task\"", async () => {
    const t = setup();
    t.host.rows.push("call Sam");
    t.s.segment(seg(1));
    t.s.segment(seg(2));
    await settle();
    expect(t.tx.calls[0].prompt).toBe("call Sam new task");
    t.tx.say(1, "buy milk");
    await settle();
    expect(t.tx.calls[1].prompt).toBe("call Sam buy milk new task");
  });

  it("Esc while listening drops only the open audio and lets the queue finish", async () => {
    const t = setup();
    t.rec.tail = seg(9);
    t.s.segment(seg(1));
    t.s.escape();
    expect(t.rec.cancelled).toBe(1);
    expect(t.rec.stopped).toBe(0); // the open segment is not sent
    expect(t.host.phases).toEqual(["draining"]);
    t.tx.say(1, "buy milk");
    await settle();
    expect(t.host.input).toBe("buy milk");
    expect(t.host.errors).toEqual([]);
    expect(t.host.phases).toEqual(["draining", "done"]);
    expect(t.ended()).toBe(1);
    expect(t.tx.calls.map((c) => c.id)).toEqual([1]);
  });

  it("blur releases the mic; queued segments still land, spoken save is ignored", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.s.segment(seg(2));
    t.s.blur();
    expect(t.rec.cancelled).toBe(1);
    t.tx.say(1, "buy milk");
    t.tx.say(2, "save all");
    await settle();
    expect(t.host.calls).toEqual(["insert:buy milk"]);
    expect(t.host.said).not.toContain("Ready to save");
    expect(t.host.errors).toEqual([]);
    expect(t.ended()).toBe(1);
  });

  it("stops at 5 minutes with a note and keeps the tasks", async () => {
    const t = setup();
    t.s.segment(seg(1));
    t.tx.say(1, "buy milk");
    await settle();
    t.at(300000, 100);
    expect(t.s.tick()).toBe("limit");
    expect(t.host.notes).toEqual([LIMIT_NOTE]);
    expect(LIMIT_NOTE).toBe("Stopped after 5 minutes");
    expect(t.rec.stopped).toBe(1);
    await settle();
    expect(t.host.input).toBe("buy milk");
    expect(t.ended()).toBe(1);
  });

  it("no speech for 8 s: Didn't catch that", async () => {
    const t = setup();
    t.at(7900);
    expect(t.s.tick()).toBeNull();
    t.at(8000);
    expect(t.s.tick()).toBe("nothing");
    await settle();
    expect(t.host.errors).toEqual([NOTHING_HEARD]);
    expect(NOTHING_HEARD).toBe("Didn't catch that. Try again closer to the mic.");
    expect(t.ended()).toBe(1);
  });

  it("finish (Enter or stop) resolves once the queue is empty", async () => {
    const t = setup();
    t.s.segment(seg(1));
    let done = false;
    const p = t.s.finish().then(() => (done = true));
    await settle();
    expect(done).toBe(false);
    t.tx.say(1, "buy milk");
    await p;
    expect(t.host.input).toBe("buy milk");
    expect(t.host.said).toContain("Ready to save");
    await t.s.finish(); // already done: resolves at once
  });

  it("wake mode: hides quietly after 6 s without speech", async () => {
    const t = setup({ mode: "wake" });
    t.at(5900);
    expect(t.s.tick()).toBeNull();
    t.at(6000);
    expect(t.s.tick()).toBe("quiet-wake");
    await settle();
    expect(t.host.errors).toEqual([]);
    expect(t.host.said).toEqual([]);
    expect(t.s.endedBy()).toBe("quiet-wake");
  });

  it("wake mode waits 15 s of silence; focusing the box switches to focused rules", async () => {
    const t = setup({ mode: "wake" });
    t.at(6000, 3000);
    expect(t.s.tick()).toBeNull();
    t.s.focusGained();
    expect(t.s.tick()).toBe("silence");
  });
});
