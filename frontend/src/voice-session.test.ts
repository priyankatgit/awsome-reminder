import { afterEach, describe, expect, it, vi } from "vitest";
import { mountVoice, type VoiceHost } from "./voice";
import type { PcmSegment } from "./voice-segment";
import { LIMIT_NOTE, NOTHING_HEARD, SEGMENT_TIMEOUT_MS, VoiceSession, type SessionHost, type SessionRecorder } from "./voice-session";

// mountVoice's wiring (below) runs against a fake Wails API and a fake
// mic / AudioWorklet stack; the VoiceSession tests do not touch either.
const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  return {
  status: (): Promise<unknown> => Promise.resolve({ state: "ready" }),
  transcribe: (_pcm: Int16Array, _prompt: string, _speechMs: number): Promise<string> & { cancel(): void } =>
    Object.assign(Promise.resolve(""), { cancel() {} }),
  ended: 0,
  getUserMedia: 0,
  taps: [] as { port: { onmessage: ((e: { data: Float32Array }) => void) | null } }[],
  };
});
vi.mock("./api", () => ({
  api: {
    voice: {
      status: () => m.status(),
      install: async () => {},
      cancelInstall: async () => {},
      warm: () => Promise.resolve(),
      transcribeSegment: (pcm: Int16Array, prompt: string, speechMs: number) => m.transcribe(pcm, prompt, speechMs),
      sessionEnded: async () => {
        m.ended++;
      },
    },
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  events: { voiceStatus: "voice:status" },
  on: () => () => {},
}));

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
  let cancels = 0;
  const s = new VoiceSession(opts.mode ?? "focused", rec, host, {
    transcribe: tx.fn,
    now: () => now,
    sessionEnded: () => ended++,
    errorText: (e) => (e instanceof Error ? e.message : String(e)),
    onPhase: (p) => host.phases.push(p),
    cancelTranscribe: () => cancels++,
  });
  return {
    s,
    cancels: () => cancels,
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

  describe("stuck transcriptions", () => {
    afterEach(() => {
      vi.useRealTimers();
    });

    it("Esc while draining cancels the pending request and ends, keeping what landed", async () => {
      const t = setup();
      t.s.segment(seg(1));
      t.s.segment(seg(2));
      t.tx.say(1, "buy milk");
      await settle();
      let finished = false;
      const p = t.s.finish().then(() => (finished = true)); // Enter: segment 2 never returns
      await settle();
      expect(t.host.phases).toEqual(["draining"]);
      t.s.escape();
      expect(t.cancels()).toBeGreaterThan(0);
      expect(t.host.errors).toEqual(["Couldn't transcribe the last bit: cancelled"]);
      expect(t.host.phases).toEqual(["draining", "done"]);
      expect(t.ended()).toBe(1);
      await p;
      expect(finished).toBe(true);
      expect(t.host.input).toBe("buy milk");
      t.tx.say(2, "late"); // ignored
      await settle();
      expect(t.host.input).toBe("buy milk");
    });

    it("Esc while draining a spoken save does not save", async () => {
      const t = setup();
      t.rec.tail = seg(2);
      t.s.segment(seg(1));
      t.tx.say(1, "buy milk. save all");
      await settle();
      expect(t.host.phases).toEqual(["draining"]); // tail segment 2 pending
      t.s.escape();
      await settle();
      expect(t.host.calls).toEqual(["insert:buy milk"]);
    });

    it("a segment with no answer in 30 s fails; the session goes on and finish completes", async () => {
      vi.useFakeTimers();
      const t = setup();
      t.s.segment(seg(1));
      t.s.segment(seg(2));
      t.tx.say(2, "call Sam");
      let finished = false;
      void t.s.finish().then(() => (finished = true));
      await vi.advanceTimersByTimeAsync(SEGMENT_TIMEOUT_MS - 1);
      expect(t.host.errors).toEqual([]);
      expect(finished).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      await settle();
      expect(t.cancels()).toBe(1); // the stuck request is cancelled
      expect(t.host.errors).toEqual(["Couldn't transcribe the last bit: timed out"]);
      expect(t.host.calls).toEqual(["insert:call Sam"]); // later segment still applied
      expect(finished).toBe(true);
      expect(t.host.said).toContain("Ready to save");
      expect(t.ended()).toBe(1);
    });

    it("a stuck segment holds the silence end only until its timeout", async () => {
      vi.useFakeTimers();
      const t = setup();
      t.s.segment(seg(1));
      t.at(5000, 3000);
      expect(t.s.tick()).toBeNull(); // held: segment 1 in flight
      await vi.advanceTimersByTimeAsync(SEGMENT_TIMEOUT_MS);
      await settle();
      expect(t.host.errors).toEqual(["Couldn't transcribe the last bit: timed out"]);
      expect(t.s.tick()).toBe("silence");
    });
  });
});

describe("mountVoice wiring", () => {
  const node = () => ({ connect: (n: unknown) => n, disconnect() {} });
  class FakeContext {
    static resumesTo = "running";
    sampleRate = 16000;
    state = "suspended";
    resume = async () => {
      this.state = FakeContext.resumesTo;
    };
    destination = {};
    audioWorklet = { addModule: async () => {} };
    createMediaStreamSource = node;
    createGain = () => ({ ...node(), gain: { value: 1 } });
    close = async () => {};
  }
  class FakeTap {
    port = { onmessage: null as ((e: { data: Float32Array }) => void) | null };
    connect = (n: unknown) => n;
    disconnect() {}
    constructor() {
      m.taps.push(this);
    }
  }

  let root: HTMLElement;
  let host: VoiceHost & { errors: string[]; inserted: string[]; ends: string[]; fails: string[] };

  function mount() {
    document.body.innerHTML = `
      <main id="capture">
        <input id="cap-input" />
        <span id="cap-busy" hidden></span>
        <button id="cap-mic"><span id="cap-mic-icon"></span></button>
        <span id="cap-listen" hidden><span id="cap-listen-label"></span><canvas id="cap-wave"></canvas><span id="cap-timer"></span></span>
        <p id="cap-voice-live"></p>
        <section id="cap-voice" hidden><h2 id="cap-voice-title"></h2><p id="cap-voice-body"></p>
          <div id="cap-progress"><span id="cap-progress-bar"></span></div><p id="cap-voice-meta"></p><div id="cap-voice-actions"></div></section>
      </main>`;
    root = document.body;
    const box = root.querySelector<HTMLElement>("#capture")!;
    const input = root.querySelector<HTMLInputElement>("#cap-input")!;
    host = {
      box,
      input,
      errors: [],
      inserted: [],
      ends: [],
      fails: [],
      insert(t) {
        host.inserted.push(t);
        input.value = `${input.value} ${t}`.trim();
      },
      showError(msg) {
        if (msg) host.errors.push(msg);
      },
      setListeningLead() {},
      addTask: () => 0,
      scratch: () => false,
      saveAll: async () => {},
      cancelAll() {},
      multi: () => true,
      sessionText: () => input.value,
      showNote() {},
      sessionEnded(mode, end) {
        host.ends.push(`${mode}:${end}`);
      },
      wakeFailed(msg) {
        host.fails.push(msg ?? "(error kept)");
      },
    };
    return mountVoice(root, host);
  }

  /** Speech then silence through the fake worklet: one segment. */
  function speakOneSegment() {
    const tap = m.taps.at(-1)!;
    for (let i = 0; i < 64; i++) tap.port.onmessage!({ data: new Float32Array(128).fill(0.3) });
    for (let i = 0; i < 120; i++) tap.port.onmessage!({ data: new Float32Array(128) });
  }

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function stubAudio() {
    FakeContext.resumesTo = "running";
    m.getUserMedia = 0;
    m.taps = [];
    vi.stubGlobal("AudioContext", FakeContext);
    vi.stubGlobal("AudioWorkletNode", FakeTap);
    vi.stubGlobal("requestAnimationFrame", () => 0);
    vi.stubGlobal("cancelAnimationFrame", () => {});
    vi.stubGlobal("navigator", {
      mediaDevices: {
        getUserMedia: async () => {
          m.getUserMedia++;
          return { getTracks: () => [] };
        },
      },
    });
    URL.createObjectURL = () => "blob:tap";
    URL.revokeObjectURL = () => {};
    HTMLCanvasElement.prototype.getContext = (() => null) as never;
  }

  it("a blur during the status check stops the start: no mic, no session", async () => {
    stubAudio();
    const st = deferred<unknown>();
    m.status = () => st.promise;
    const ctl = mount();
    ctl.startSession("focused");
    ctl.cancel(); // the window lost focus while the status was loading
    st.resolve({ state: "ready" });
    await settle();
    expect(m.getUserMedia).toBe(0);
    expect(ctl.state()).toBe("idle");
  });

  it("Esc while transcribing cancels the in-flight request and ends the session", async () => {
    stubAudio();
    m.status = () => Promise.resolve({ state: "ready" });
    const cancel = vi.fn();
    const prompts: string[] = [];
    m.transcribe = (_pcm, prompt) => {
      prompts.push(prompt);
      return Object.assign(new Promise<string>(() => {}), { cancel }); // never answers
    };
    m.ended = 0;
    const ctl = mount();
    ctl.startSession("focused");
    await settle();
    expect(m.getUserMedia).toBe(1);
    expect(ctl.state()).toBe("listening");
    speakOneSegment();
    await settle();
    expect(prompts).toEqual(["new task"]);
    ctl.toggle(); // Ctrl+M: stop, wait for the segment
    expect(ctl.state()).toBe("transcribing");
    expect(ctl.escape()).toBe(true);
    expect(cancel).toHaveBeenCalled();
    expect(ctl.state()).toBe("idle");
    expect(host.errors).toContain("Couldn't transcribe the last bit: cancelled");
    expect(m.ended).toBe(1);
    await ctl.finish(); // nothing left to wait for
  });

  it("a wake session shows its own listening label and reports how it ended", async () => {
    stubAudio();
    m.status = () => Promise.resolve({ state: "ready" });
    const ctl = mount();
    ctl.startSession("wake");
    await settle();
    expect(ctl.state()).toBe("listening");
    expect(root.querySelector("#cap-listen-label")!.textContent).toBe("Listening — say your task");
    ctl.escape();
    await settle();
    expect(host.ends).toEqual(["wake:escape"]);
  });

  it("a wake session the user engaged with ends as focused", async () => {
    stubAudio();
    m.status = () => Promise.resolve({ state: "ready" });
    const ctl = mount();
    ctl.startSession("wake");
    await settle();
    ctl.focusGained();
    ctl.escape();
    await settle();
    expect(host.ends).toEqual(["focused:escape"]);
  });

  it("a wake whose mic cannot start reports the mic error as a failed wake", async () => {
    stubAudio();
    FakeContext.resumesTo = "suspended"; // autoplay blocked: no user gesture
    m.status = () => Promise.resolve({ state: "ready" });
    const ctl = mount();
    ctl.startSession("wake");
    await settle();
    expect(ctl.state()).toBe("idle");
    expect(host.fails).toEqual(["Could not start the microphone: audio is suspended (no user gesture yet?)"]);
    expect(host.ends).toEqual(["wake:abort"]);
  });

  it("a focused session's mic error is shown as before, not as a failed wake", async () => {
    stubAudio();
    FakeContext.resumesTo = "suspended";
    m.status = () => Promise.resolve({ state: "ready" });
    const ctl = mount();
    ctl.startSession("focused");
    await settle();
    expect(host.errors).toEqual(["Could not start the microphone: audio is suspended (no user gesture yet?)"]);
    expect(host.fails).toEqual([]);
  });

  it("a wake without dictation installed fails with the setup copy, no panel, no mic", async () => {
    stubAudio();
    m.status = () => Promise.resolve({ state: "absent" });
    const ctl = mount();
    ctl.startSession("wake");
    await settle();
    expect(ctl.state()).toBe("idle");
    expect(root.querySelector<HTMLElement>("#cap-voice")!.hidden).toBe(true);
    expect(host.fails).toEqual(["Voice isn't set up yet. Open Settings › Voice to set it up."]);
    expect(m.getUserMedia).toBe(0);
  });

  it("a wake while voice is installing fails with the downloading copy", async () => {
    stubAudio();
    m.status = () => Promise.resolve({ state: "installing" });
    const ctl = mount();
    ctl.startSession("wake");
    await settle();
    expect(host.fails).toEqual(["Voice is still downloading. Try again when it's ready."]);
    expect(root.querySelector<HTMLElement>("#cap-voice")!.hidden).toBe(true);
  });

  it("a wake where voice is unsupported fails with the status hint", async () => {
    stubAudio();
    m.status = () => Promise.resolve({ state: "unsupported", hint: "Install whisper-cpp with Homebrew." });
    const ctl = mount();
    ctl.startSession("wake");
    await settle();
    expect(host.fails).toEqual(["Install whisper-cpp with Homebrew."]);
  });

  it("a wake whose status check fails keeps that error and reports a failed wake", async () => {
    stubAudio();
    m.status = () => Promise.reject(new Error("status broke"));
    const ctl = mount();
    ctl.startSession("wake");
    await settle();
    expect(host.errors).toEqual(["status broke"]);
    expect(host.fails).toEqual(["(error kept)"]);
    expect(m.getUserMedia).toBe(0);
  });

  it("a wake while a session is still running reports a failed wake", async () => {
    stubAudio();
    m.status = () => Promise.resolve({ state: "ready" });
    const ctl = mount();
    ctl.startSession("focused");
    await settle();
    expect(ctl.state()).toBe("listening");
    ctl.startSession("wake");
    await settle();
    expect(host.fails).toEqual(["(error kept)"]);
    expect(m.getUserMedia).toBe(1);
  });
});
