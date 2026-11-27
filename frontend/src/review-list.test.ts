import { beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
});
vi.mock("./api", () => ({
  api: {
    preview: async (input: string) => ({ kind: "todo", text: input, tags: ["inbox"], dueAt: null, rrule: "", priority: "", human: "", alerts: null, warnings: null }),
  },
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
}));

import { ReviewList, saveSequential } from "./review-list";

const item = (text: string) => ({ id: `id-${text}`, text }) as never;

describe("saveSequential", () => {
  it("saves in order", async () => {
    const calls: string[] = [];
    const r = await saveSequential(["a", "b", "c"], async (t) => {
      calls.push(t);
      return item(t);
    });
    expect(calls).toEqual(["a", "b", "c"]);
    expect(r.saved.length).toBe(3);
    expect(r.failedAt).toBeNull();
  });

  it("stops at the first failure", async () => {
    const capture = vi.fn(async (t: string) => {
      if (t === "b") throw new Error("db locked");
      return item(t);
    });
    const r = await saveSequential(["a", "b", "c"], capture);
    expect(r.saved.length).toBe(1);
    expect(r.failedAt).toBe(1);
    expect(r.error).toBe("db locked");
    expect(capture).toHaveBeenCalledTimes(2);
    expect(capture).not.toHaveBeenCalledWith("c");
  });
});

describe("ReviewList", () => {
  let box: HTMLElement;
  let changes: number;
  let list: ReviewList;
  beforeEach(() => {
    box = document.createElement("div");
    document.body.append(box);
    changes = 0;
    list = new ReviewList(box, () => changes++);
  });

  it("add, removeAt, popLast, takeAt and restore keep items() right", () => {
    list.add("one");
    list.add("two");
    list.add("three");
    expect(list.items()).toEqual(["one", "two", "three"]);
    expect(box.querySelectorAll(".rv-row").length).toBe(3);
    list.removeAt(1);
    expect(list.items()).toEqual(["one", "three"]);
    expect(list.popLast()).toBe("three");
    expect(list.takeAt(0)).toBe("one");
    expect(list.items()).toEqual([]);
    expect(list.popLast()).toBeUndefined();
    list.restore(["x", "y"]);
    expect(list.items()).toEqual(["x", "y"]);
    list.clear();
    expect(list.items()).toEqual([]);
    expect(changes).toBeGreaterThan(0);
  });

  it("renders each row with a numbered remove button", () => {
    list.add("one");
    list.add("two");
    const labels = [...box.querySelectorAll<HTMLButtonElement>(".rv-remove")].map((b) => b.getAttribute("aria-label"));
    expect(labels).toEqual(["Remove task 1", "Remove task 2"]);
    box.querySelectorAll<HTMLButtonElement>(".rv-remove")[0].click();
    expect(list.items()).toEqual(["two"]);
    expect(box.querySelector(".rv-remove")!.getAttribute("aria-label")).toBe("Remove task 1");
  });

  it("shows a preview after add and an error on a row", async () => {
    list.add("call Sam #work");
    await new Promise((r) => setTimeout(r, 0));
    expect(box.querySelector(".chip-tag")).not.toBeNull();
    list.setError(0, "db locked");
    expect(box.querySelector(".rv-error")!.textContent).toBe("db locked");
  });

  it("keys: Delete removes, Enter edits, Esc goes back, arrows move", () => {
    const onEdit = vi.fn();
    const onBack = vi.fn();
    const l = new ReviewList(box, () => {}, { onEdit, onBack });
    l.add("a");
    l.add("b");
    const rows = box.querySelectorAll<HTMLElement>(".rv-row");
    const key = (n: HTMLElement, k: string) => n.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
    rows[0].focus();
    key(rows[0], "ArrowDown");
    expect(document.activeElement).toBe(rows[1]);
    key(rows[1], "ArrowUp");
    expect(document.activeElement).toBe(rows[0]);
    key(rows[0], "Enter");
    expect(onEdit).toHaveBeenCalledWith(0);
    key(rows[0], "Escape");
    expect(onBack).toHaveBeenCalled();
    key(rows[0], "Delete");
    expect(l.items()).toEqual(["b"]);
  });
});
