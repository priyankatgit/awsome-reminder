import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createAutosaver, type SaveStatus } from "./autosave";

/** A save function whose calls resolve or reject when the test says so. */
function controlledSave() {
  const calls: { itemId: string; value: string; resolve: () => void; reject: (e: unknown) => void }[] = [];
  const save = vi.fn(
    (itemId: string, value: string) =>
      new Promise<void>((resolve, reject) => {
        calls.push({ itemId, value, resolve, reject });
      }),
  );
  return { save, calls };
}

describe("createAutosaver", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("debounces to one save after 600ms", async () => {
    const save = vi.fn(async (_id: string, _v: string) => {});
    const s = createAutosaver(save);
    s.change("A", "a");
    await vi.advanceTimersByTimeAsync(200);
    s.change("A", "ab");
    await vi.advanceTimersByTimeAsync(200);
    s.change("A", "abc");
    await vi.advanceTimersByTimeAsync(599);
    expect(save).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith("A", "abc");
    expect(s.status()).toBe("saved");
  });

  it("flushes pending edit to the previous item on switch", async () => {
    const save = vi.fn(async (_id: string, _v: string) => {});
    const s = createAutosaver(save);
    s.change("A", "x");
    await vi.advanceTimersByTimeAsync(300);
    s.change("B", "y");
    await vi.advanceTimersByTimeAsync(0);
    // A's edit goes out at once, to A, without waiting for the timer.
    expect(save.mock.calls).toEqual([["A", "x"]]);
    await vi.advanceTimersByTimeAsync(600);
    expect(save.mock.calls).toEqual([
      ["A", "x"],
      ["B", "y"],
    ]);
  });

  it("error status on reject, retry on next change", async () => {
    const { save, calls } = controlledSave();
    const s = createAutosaver(save);
    const seen: SaveStatus[] = [];
    s.onStatus((st) => seen.push(st));

    s.change("A", "x");
    await vi.advanceTimersByTimeAsync(600);
    expect(s.status()).toBe("saving");
    calls[0].reject(new Error("disk full"));
    await vi.advanceTimersByTimeAsync(0);
    expect(s.status()).toBe("error");

    s.change("A", "xy");
    await vi.advanceTimersByTimeAsync(600);
    expect(save).toHaveBeenCalledTimes(2);
    expect(save).toHaveBeenLastCalledWith("A", "xy");
    calls[1].resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(s.status()).toBe("saved");
    expect(seen).toEqual(["saving", "error", "saving", "saved"]);
  });

  it("flush resolves after in-flight save", async () => {
    const { save, calls } = controlledSave();
    const s = createAutosaver(save);
    s.change("A", "x");
    await vi.advanceTimersByTimeAsync(600);
    expect(calls).toHaveLength(1);

    let flushed = false;
    void s.flush().then(() => {
      flushed = true;
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(flushed).toBe(false);
    calls[0].resolve();
    await vi.advanceTimersByTimeAsync(0);
    expect(flushed).toBe(true);
  });

  it("flush saves a pending edit at once and waits for it", async () => {
    const { save, calls } = controlledSave();
    const s = createAutosaver(save);
    s.change("A", "x");
    let flushed = false;
    void s.flush().then(() => {
      flushed = true;
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(save).toHaveBeenCalledWith("A", "x");
    expect(flushed).toBe(false);
    calls[0].reject(new Error("nope"));
    await vi.advanceTimersByTimeAsync(0);
    expect(flushed).toBe(true); // flush never rejects; status() carries the error
    expect(s.status()).toBe("error");
    await vi.advanceTimersByTimeAsync(600);
    expect(save).toHaveBeenCalledTimes(1); // the timer was cancelled
  });
});
